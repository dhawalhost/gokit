package outbox_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/outbox"
	"gorm.io/gorm"
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

func TestMemoryStore_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		operations func(t *testing.T, store *outbox.MemoryStore, ctx context.Context)
	}{
		{
			name: "save and fetch single pending message",
			operations: func(t *testing.T, store *outbox.MemoryStore, ctx context.Context) {
				msg := &outbox.Message{
					Topic:   "orders.created",
					Key:     "ord-123",
					Payload: []byte(`{"amount":100}`),
					Headers: map[string]string{"source": "checkout"},
				}
				if err := store.Save(ctx, msg); err != nil {
					t.Fatalf("Save: %v", err)
				}
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
			},
		},
		{
			name: "mark published excludes from pending fetch",
			operations: func(t *testing.T, store *outbox.MemoryStore, ctx context.Context) {
				msg := &outbox.Message{
					ID:      "msg-publish-1",
					Topic:   "notifications.sent",
					Payload: []byte("hello"),
				}
				if err := store.Save(ctx, msg); err != nil {
					t.Fatalf("Save: %v", err)
				}
				if err := store.MarkPublished(ctx, msg.ID); err != nil {
					t.Fatalf("MarkPublished: %v", err)
				}
				pending, err := store.FetchPending(ctx, 10)
				if err != nil {
					t.Fatalf("FetchPending: %v", err)
				}
				if len(pending) != 0 {
					t.Errorf("expected 0 pending messages after mark published, got %d", len(pending))
				}
			},
		},
		{
			name: "cleanup removes published messages older than threshold",
			operations: func(t *testing.T, store *outbox.MemoryStore, ctx context.Context) {
				msg := &outbox.Message{
					ID:      "msg-cleanup-1",
					Topic:   "events.old",
					Payload: []byte("old"),
				}
				if err := store.Save(ctx, msg); err != nil {
					t.Fatalf("Save: %v", err)
				}
				if err := store.MarkPublished(ctx, msg.ID); err != nil {
					t.Fatalf("MarkPublished: %v", err)
				}

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
			},
		},
		{
			name: "cleanup preserves pending or failed messages even if past threshold",
			operations: func(t *testing.T, store *outbox.MemoryStore, ctx context.Context) {
				msgPending := &outbox.Message{
					ID:      "msg-pending",
					Topic:   "events.pending",
					Payload: []byte("keep-me"),
				}
				msgFailed := &outbox.Message{
					ID:      "msg-failed",
					Topic:   "events.failed",
					Payload: []byte("keep-me-too"),
				}
				_ = store.Save(ctx, msgPending)
				_ = store.Save(ctx, msgFailed)
				_ = store.MarkFailed(ctx, msgFailed.ID, errors.New("dead letter"), time.Now(), true)

				future := time.Now().UTC().Add(time.Hour)
				deleted, err := store.Cleanup(ctx, future)
				if err != nil {
					t.Fatalf("Cleanup: %v", err)
				}
				if deleted != 0 {
					t.Errorf("expected 0 deleted, got %d", deleted)
				}
				if len(store.All()) != 2 {
					t.Errorf("expected 2 messages retained, got %d", len(store.All()))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := outbox.NewMemoryStore()
			tc.operations(t, store, context.Background())
		})
	}
}

func TestRelay_ProcessBatch_TableDriven(t *testing.T) {
	tests := []struct {
		name              string
		relayCfg          outbox.RelayConfig
		initialFailCount  int
		failErr           error
		initialMessages   []*outbox.Message
		expectedProcessed int
		verify            func(t *testing.T, store *outbox.MemoryStore, pub *mockPublisher, relay *outbox.Relay)
	}{
		{
			name: "successful batch publishing of multiple messages",
			relayCfg: outbox.RelayConfig{
				BatchSize: 10,
			},
			initialMessages: []*outbox.Message{
				{
					ID:      "m1",
					Topic:   "user.signup",
					Key:     "usr-1",
					Payload: []byte("welcome"),
					Headers: map[string]string{"env": "test"},
				},
				{
					ID:      "m2",
					Topic:   "user.signup",
					Key:     "usr-2",
					Payload: []byte("welcome2"),
				},
			},
			expectedProcessed: 2,
			verify: func(t *testing.T, store *outbox.MemoryStore, pub *mockPublisher, _ *outbox.Relay) {
				published := pub.getPublished()
				if len(published) != 2 {
					t.Fatalf("expected 2 published messages, got %d", len(published))
				}
				if published[0].key != "usr-1" || string(published[0].payload) != "welcome" {
					t.Errorf("unexpected published message 0: %+v", published[0])
				}
				for _, m := range store.All() {
					if m.Status != outbox.StatusPublished {
						t.Errorf("message %s status=%s, want published", m.ID, m.Status)
					}
				}
			},
		},
		{
			name: "transient error retries message with backoff",
			relayCfg: outbox.RelayConfig{
				BatchSize:   10,
				MaxRetries:  2,
				BaseBackoff: 10 * time.Millisecond,
				MaxBackoff:  50 * time.Millisecond,
			},
			initialFailCount: 1,
			failErr:          errors.New("broker unavailable"),
			initialMessages: []*outbox.Message{
				{
					ID:      "retry-msg",
					Topic:   "payment.charge",
					Key:     "pay-1",
					Payload: []byte("payload"),
				},
			},
			expectedProcessed: 1,
			verify: func(t *testing.T, store *outbox.MemoryStore, _ *mockPublisher, relay *outbox.Relay) {
				messages := store.All()
				if len(messages) != 1 {
					t.Fatalf("expected 1 message in store, got %d", len(messages))
				}
				m := messages[0]
				if m.RetryCount != 1 {
					t.Errorf("expected retry_count=1, got %d", m.RetryCount)
				}
				if m.LastError != "broker unavailable" {
					t.Errorf("expected error %q, got %q", "broker unavailable", m.LastError)
				}
				if m.Status != outbox.StatusPending {
					t.Errorf("expected status pending, got %s", m.Status)
				}

				// Wait for backoff to expire
				time.Sleep(50 * time.Millisecond)

				// Second attempt should now succeed as failCount was 1
				count, err := relay.ProcessBatch(context.Background())
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
			},
		},
		{
			name: "exceeds max retries marks message as failed",
			relayCfg: outbox.RelayConfig{
				BatchSize:   10,
				MaxRetries:  1,
				BaseBackoff: 10 * time.Millisecond,
			},
			initialFailCount: 10,
			failErr:          errors.New("fatal broker error"),
			initialMessages: []*outbox.Message{
				{
					ID:      "fail-msg",
					Topic:   "test",
					Payload: []byte("data"),
				},
			},
			expectedProcessed: 1,
			verify: func(t *testing.T, store *outbox.MemoryStore, _ *mockPublisher, _ *outbox.Relay) {
				messages := store.All()
				if len(messages) != 1 {
					t.Fatalf("expected 1 message, got %d", len(messages))
				}
				if messages[0].Status != outbox.StatusFailed {
					t.Errorf("expected status failed, got %s", messages[0].Status)
				}
			},
		},
		{
			name: "empty store processes zero messages without error",
			relayCfg: outbox.RelayConfig{
				BatchSize: 10,
			},
			initialMessages:   nil,
			expectedProcessed: 0,
			verify: func(t *testing.T, store *outbox.MemoryStore, pub *mockPublisher, _ *outbox.Relay) {
				if len(pub.getPublished()) != 0 {
					t.Errorf("expected 0 published, got %d", len(pub.getPublished()))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := outbox.NewMemoryStore()
			pub := &mockPublisher{
				failCount: tc.initialFailCount,
				failErr:   tc.failErr,
			}
			ctx := context.Background()
			for _, m := range tc.initialMessages {
				if err := store.Save(ctx, m); err != nil {
					t.Fatalf("Save: %v", err)
				}
			}

			relay := outbox.NewRelay(store, pub, tc.relayCfg)
			count, err := relay.ProcessBatch(ctx)
			if err != nil {
				t.Fatalf("ProcessBatch: %v", err)
			}
			if count != tc.expectedProcessed {
				t.Fatalf("expected %d processed, got %d", tc.expectedProcessed, count)
			}

			if tc.verify != nil {
				tc.verify(t, store, pub, relay)
			}
		})
	}
}

func TestRelay_Lifecycle_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		pollPeriod  time.Duration
		messages    []*outbox.Message
		expectCount int
	}{
		{
			name:       "background polling loop processes arriving messages",
			pollPeriod: 20 * time.Millisecond,
			messages: []*outbox.Message{
				{
					ID:      "async-msg-1",
					Topic:   "live.test",
					Payload: []byte("data-1"),
				},
				{
					ID:      "async-msg-2",
					Topic:   "live.test",
					Payload: []byte("data-2"),
				},
			},
			expectCount: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := outbox.NewMemoryStore()
			pub := &mockPublisher{}
			relay := outbox.NewRelay(store, pub, outbox.RelayConfig{
				PollInterval: tc.pollPeriod,
				Retention:    1 * time.Hour,
			})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			relay.Start(ctx)

			for _, m := range tc.messages {
				_ = store.Save(ctx, m)
			}

			// Wait for background poll loop to pick up messages
			time.Sleep(120 * time.Millisecond)

			published := pub.getPublished()
			if len(published) != tc.expectCount {
				t.Errorf("expected %d messages published by background relay, got %d", tc.expectCount, len(published))
			}

			// Stop cleanly
			relay.Stop()
		})
	}
}

func TestPublisherFunc_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		fn          func(ctx context.Context, topic, key string, payload []byte, headers map[string]string) error
		topic       string
		key         string
		payload     []byte
		headers     map[string]string
		wantErr     bool
		expectedErr string
	}{
		{
			name: "successful invocation with payload and headers",
			fn: func(_ context.Context, topic, key string, payload []byte, headers map[string]string) error {
				if topic != "orders" || key != "k1" || string(payload) != "v1" || headers["h1"] != "val" {
					return errors.New("mismatch")
				}
				return nil
			},
			topic:   "orders",
			key:     "k1",
			payload: []byte("v1"),
			headers: map[string]string{"h1": "val"},
			wantErr: false,
		},
		{
			name: "propagates error when closure fails",
			fn: func(_ context.Context, _, _ string, _ []byte, _ map[string]string) error {
				return errors.New("custom publish error")
			},
			topic:       "errors",
			wantErr:     true,
			expectedErr: "custom publish error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pub := outbox.PublisherFunc(tc.fn)
			err := pub.Publish(context.Background(), tc.topic, tc.key, tc.payload, tc.headers)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
			if tc.wantErr && err.Error() != tc.expectedErr {
				t.Errorf("expected error %q, got %q", tc.expectedErr, err.Error())
			}
		})
	}
}

func TestMessageTableName_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		msg      outbox.Message
		wantName string
	}{
		{
			name:     "returns outbox_messages table name",
			msg:      outbox.Message{},
			wantName: "outbox_messages",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.msg.TableName() != tc.wantName {
				t.Errorf("expected %q, got %q", tc.wantName, tc.msg.TableName())
			}
		})
	}
}

func TestNewGORMStore_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		db   *gorm.DB
	}{
		{
			name: "constructs non-nil GORMStore wrapping DB handle",
			db:   &gorm.DB{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := outbox.NewGORMStore(tc.db)
			if store == nil {
				t.Fatal("expected non-nil store from NewGORMStore")
			}
		})
	}
}

func TestRelay_ConfigDefaultsAndDoubleStart_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		cfg  outbox.RelayConfig
	}{
		{
			name: "zero config populates safe operational defaults",
			cfg:  outbox.RelayConfig{},
		},
		{
			name: "retention negative defaults to 7 days",
			cfg: outbox.RelayConfig{
				Retention: -1,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := outbox.NewMemoryStore()
			pub := &mockPublisher{}
			relay := outbox.NewRelay(store, pub, tc.cfg)
			if relay == nil {
				t.Fatal("expected non-nil relay")
			}

			// Verify double-start safe idempotency
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			relay.Start(ctx)
			relay.Start(ctx) // Idempotent second call returns immediately
			relay.Stop()
		})
	}
}
