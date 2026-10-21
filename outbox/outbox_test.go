package outbox_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/outbox"
)

type mockPublisher struct {
	mu        sync.Mutex
	published []publishedItem
	failCount int
	failErr   error
}

type publishedItem struct {
	topic   string
	key     string
	payload []byte
	headers map[string]string
}

func (m *mockPublisher) Publish(_ context.Context, topic, key string, payload []byte, headers map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failCount > 0 {
		m.failCount--
		return m.failErr
	}

	m.published = append(m.published, publishedItem{
		topic:   topic,
		key:     key,
		payload: payload,
		headers: headers,
	})
	return nil
}

func (m *mockPublisher) getPublished() []publishedItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]publishedItem, len(m.published))
	copy(res, m.published)
	return res
}

func TestMemoryStore_BasicOperations(t *testing.T) {
	store := outbox.NewMemoryStore()
	ctx := context.Background()

	msg := &outbox.Message{
		Topic:   "orders.created",
		Key:     "ord-123",
		Payload: []byte(`{"amount":100}`),
		Headers: map[string]string{"source": "checkout"},
	}

	if err := store.Save(ctx, msg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Fetch pending
	pending, err := store.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("FetchPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending message, got %d", len(pending))
	}
	if pending[0].Topic != "orders.created" || pending[0].Key != "ord-123" {
		t.Errorf("unexpected pending message: %+v", pending[0])
	}

	// Mark Published
	if err := store.MarkPublished(ctx, msg.ID); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}

	// Fetch pending again - should be empty
	pending, err = store.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("FetchPending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending messages, got %d", len(pending))
	}

	// Cleanup
	future := time.Now().UTC().Add(time.Hour)
	deleted, err := store.Cleanup(ctx, future)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 message deleted, got %d", deleted)
	}
	if len(store.All()) != 0 {
		t.Errorf("expected store to be empty, got %d", len(store.All()))
	}
}

func TestRelay_ProcessBatch_Success(t *testing.T) {
	store := outbox.NewMemoryStore()
	pub := &mockPublisher{}
	relay := outbox.NewRelay(store, pub, outbox.RelayConfig{
		BatchSize: 10,
	})

	ctx := context.Background()
	_ = store.Save(ctx, &outbox.Message{
		ID:      "m1",
		Topic:   "user.signup",
		Key:     "usr-1",
		Payload: []byte("welcome"),
		Headers: map[string]string{"env": "test"},
	})
	_ = store.Save(ctx, &outbox.Message{
		ID:      "m2",
		Topic:   "user.signup",
		Key:     "usr-2",
		Payload: []byte("welcome2"),
	})

	count, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 processed messages, got %d", count)
	}

	published := pub.getPublished()
	if len(published) != 2 {
		t.Fatalf("expected 2 published messages, got %d", len(published))
	}
	if published[0].key != "usr-1" || string(published[0].payload) != "welcome" {
		t.Errorf("unexpected published message 0: %+v", published[0])
	}

	// Verify both are published in store
	all := store.All()
	for _, m := range all {
		if m.Status != outbox.StatusPublished {
			t.Errorf("message %s status=%s, want published", m.ID, m.Status)
		}
	}
}

func TestRelay_RetryAndMaxRetries(t *testing.T) {
	store := outbox.NewMemoryStore()
	pubErr := errors.New("broker unavailable")
	pub := &mockPublisher{
		failCount: 1,
		failErr:   pubErr,
	}
	relay := outbox.NewRelay(store, pub, outbox.RelayConfig{
		BatchSize:   10,
		MaxRetries:  2,
		BaseBackoff: 10 * time.Millisecond,
		MaxBackoff:  50 * time.Millisecond,
	})

	ctx := context.Background()
	msg := &outbox.Message{
		ID:      "retry-msg",
		Topic:   "payment.charge",
		Key:     "pay-1",
		Payload: []byte("payload"),
	}
	_ = store.Save(ctx, msg)

	// Attempt 1: fails
	count, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 processed, got %d", count)
	}

	// Message should now have retry_count=1, last_error set, scheduled in future
	messages := store.All()
	if len(messages) != 1 {
		t.Fatalf("expected 1 message in store, got %d", len(messages))
	}
	m := messages[0]
	if m.RetryCount != 1 {
		t.Errorf("expected retry_count=1, got %d", m.RetryCount)
	}
	if m.LastError != pubErr.Error() {
		t.Errorf("expected error %q, got %q", pubErr.Error(), m.LastError)
	}
	if m.Status != outbox.StatusPending {
		t.Errorf("expected status pending, got %s", m.Status)
	}

	// Wait for backoff to pass
	time.Sleep(50 * time.Millisecond)

	// Attempt 2: publisher succeeds now
	count, err = relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("ProcessBatch attempt 2: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 processed on attempt 2, got %d", count)
	}

	messages = store.All()
	if messages[0].Status != outbox.StatusPublished {
		t.Errorf("expected status published after recovery, got %s", messages[0].Status)
	}
}

func TestRelay_ExceedsMaxRetries_BecomesFailed(t *testing.T) {
	store := outbox.NewMemoryStore()
	pub := &mockPublisher{
		failCount: 10,
		failErr:   errors.New("fatal broker error"),
	}
	relay := outbox.NewRelay(store, pub, outbox.RelayConfig{
		BatchSize:   10,
		MaxRetries:  1, // Only 1 attempt before marking failed
		BaseBackoff: 10 * time.Millisecond,
	})

	ctx := context.Background()
	_ = store.Save(ctx, &outbox.Message{
		ID:      "fail-msg",
		Topic:   "test",
		Payload: []byte("data"),
	})

	_, _ = relay.ProcessBatch(ctx)

	messages := store.All()
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Status != outbox.StatusFailed {
		t.Errorf("expected status failed, got %s", messages[0].Status)
	}
}

func TestRelay_StartAndStop(t *testing.T) {
	store := outbox.NewMemoryStore()
	pub := &mockPublisher{}
	relay := outbox.NewRelay(store, pub, outbox.RelayConfig{
		PollInterval: 20 * time.Millisecond,
		Retention:    1 * time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	relay.Start(ctx)

	// Enqueue a message while relay is running
	_ = store.Save(ctx, &outbox.Message{
		ID:      "async-msg",
		Topic:   "live.test",
		Payload: []byte("data"),
	})

	// Wait for background poll loop to pick it up
	time.Sleep(100 * time.Millisecond)

	published := pub.getPublished()
	if len(published) != 1 {
		t.Errorf("expected message to be published by background relay, got %d", len(published))
	}

	// Stop cleanly
	relay.Stop()
}

func TestPublisherFunc(t *testing.T) {
	var called bool
	fn := outbox.PublisherFunc(func(_ context.Context, topic, _ string, _ []byte, _ map[string]string) error {
		if topic == "my.topic" {
			called = true
		}
		return nil
	})

	_ = fn.Publish(context.Background(), "my.topic", "k", []byte("p"), nil)
	if !called {
		t.Error("expected PublisherFunc to be invoked")
	}
}
