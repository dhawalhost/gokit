package kafka_test

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/eventbus"
	"github.com/dhawalhost/gokit/eventbus/kafka"
	kafkago "github.com/segmentio/kafka-go"
)

type mockMessageWriter struct {
	mu       sync.Mutex
	messages []kafkago.Message
	closed   bool
	failErr  error
}

func (w *mockMessageWriter) WriteMessages(ctx context.Context, msgs ...kafkago.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failErr != nil {
		return w.failErr
	}
	w.messages = append(w.messages, msgs...)
	return nil
}

func (w *mockMessageWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return nil
}

type mockMessageReader struct {
	mu        sync.Mutex
	msgCh     chan kafkago.Message
	committed []kafkago.Message
	closed    bool
	failErr   error
}

func newMockReader() *mockMessageReader {
	return &mockMessageReader{
		msgCh: make(chan kafkago.Message, 100),
	}
}

func (r *mockMessageReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	if r.failErr != nil {
		return kafkago.Message{}, r.failErr
	}
	select {
	case <-ctx.Done():
		return kafkago.Message{}, ctx.Err()
	case msg, ok := <-r.msgCh:
		if !ok {
			return kafkago.Message{}, errors.New("reader closed")
		}
		return msg, nil
	}
}

func (r *mockMessageReader) CommitMessages(ctx context.Context, msgs ...kafkago.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.committed = append(r.committed, msgs...)
	return nil
}

func (r *mockMessageReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		close(r.msgCh)
	}
	return nil
}

func TestNew_Configuration(t *testing.T) {
	t.Run("fails without brokers or writer", func(t *testing.T) {
		_, err := kafka.New()
		if !errors.Is(err, kafka.ErrBrokersRequired) {
			t.Fatalf("expected ErrBrokersRequired, got: %v", err)
		}
	})

	t.Run("succeeds with brokers and functional options", func(t *testing.T) {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
		dialer := &kafkago.Dialer{Timeout: 5 * time.Second}

		b, err := kafka.New(
			kafka.WithBrokers("localhost:9092", "localhost:9093"),
			kafka.WithClientID("test-service"),
			kafka.WithBatchSize(250),
			kafka.WithBatchTimeout(50*time.Millisecond),
			kafka.WithWriteTimeout(3*time.Second),
			kafka.WithReadTimeout(8*time.Second),
			kafka.WithBalancer(&kafkago.LeastBytes{}),
			kafka.WithRequiredAcks(kafkago.RequireOne),
			kafka.WithCompression(kafkago.Snappy),
			kafka.WithTLS(tlsConfig),
			kafka.WithDialer(dialer),
		)
		if err != nil {
			t.Fatalf("failed to create bus: %v", err)
		}
		defer func() { _ = b.Close() }()

		sub, err := b.Subscribe(context.Background(), "default.topic", func(ctx context.Context, msg *eventbus.Message) error {
			return nil
		})
		if err != nil {
			t.Fatalf("failed to subscribe with defaultReaderFactory: %v", err)
		}
		_ = sub.Unsubscribe()
	})
}

func TestMessageConversion(t *testing.T) {
	original := &eventbus.Message{
		ID:      "evt_1001",
		Topic:   "orders.created",
		Payload: []byte(`{"order_id":"101"}`),
		Key:     "tenant_abc",
		Headers: map[string]string{
			"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			"x-actor-id":  "usr_99",
		},
		Timestamp: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
	}

	kmsg := kafka.ToKafkaMessage(original)
	if string(kmsg.Key) != "tenant_abc" {
		t.Errorf("expected Key tenant_abc, got: %s", string(kmsg.Key))
	}
	if kmsg.Topic != "orders.created" {
		t.Errorf("expected Topic orders.created, got: %s", kmsg.Topic)
	}

	converted := kafka.FromKafkaMessage(kmsg, "fallback")
	if converted.ID != original.ID {
		t.Errorf("expected ID %s, got: %s", original.ID, converted.ID)
	}
	if converted.Topic != original.Topic {
		t.Errorf("expected Topic %s, got: %s", original.Topic, converted.Topic)
	}
	if converted.Key != original.Key {
		t.Errorf("expected Key %s, got: %s", original.Key, converted.Key)
	}
	if string(converted.Payload) != string(original.Payload) {
		t.Errorf("expected Payload %s, got: %s", string(original.Payload), string(converted.Payload))
	}
	if converted.Headers["traceparent"] != original.Headers["traceparent"] {
		t.Errorf("missing header traceparent")
	}

	t.Run("fallback when metadata headers are absent", func(t *testing.T) {
		bare := kafkago.Message{
			Key:   []byte("part_key_123"),
			Value: []byte("simple"),
		}
		res := kafka.FromKafkaMessage(bare, "default_topic")
		if res.Topic != "default_topic" {
			t.Errorf("expected default_topic, got: %s", res.Topic)
		}
		if res.Key != "part_key_123" {
			t.Errorf("expected part_key_123, got: %s", res.Key)
		}
		if res.ID != "part_key_123" {
			t.Errorf("expected ID part_key_123 from key fallback, got: %s", res.ID)
		}
	})
}

func TestBus_Publish(t *testing.T) {
	ctx := context.Background()

	t.Run("publish validation", func(t *testing.T) {
		writer := &mockMessageWriter{}
		bus, _ := kafka.New(kafka.WithWriter(writer))
		defer func() { _ = bus.Close() }()

		if err := bus.Publish(ctx, nil); !errors.Is(err, kafka.ErrInvalidMessage) {
			t.Errorf("expected ErrInvalidMessage on nil msg, got: %v", err)
		}

		if err := bus.Publish(ctx, &eventbus.Message{}); !errors.Is(err, kafka.ErrInvalidMessage) {
			t.Errorf("expected ErrInvalidMessage on empty topic, got: %v", err)
		}

		writer.failErr = errors.New("network down")
		err := bus.Publish(ctx, &eventbus.Message{Topic: "orders.paid", Payload: []byte("ok")})
		if err == nil {
			t.Errorf("expected publish error from writer")
		}
	})

	t.Run("publish success", func(t *testing.T) {
		writer := &mockMessageWriter{}
		bus, _ := kafka.New(kafka.WithWriter(writer))
		defer func() { _ = bus.Close() }()

		msg := &eventbus.Message{
			Topic:   "users.signup",
			Payload: []byte(`{"user":"dhawal"}`),
		}
		if err := bus.Publish(ctx, msg); err != nil {
			t.Fatalf("unexpected publish error: %v", err)
		}

		if len(writer.messages) != 1 {
			t.Fatalf("expected 1 message written, got: %d", len(writer.messages))
		}
		if string(writer.messages[0].Value) != `{"user":"dhawal"}` {
			t.Errorf("unexpected message payload: %s", string(writer.messages[0].Value))
		}
	})

	t.Run("publish on closed bus fails", func(t *testing.T) {
		writer := &mockMessageWriter{}
		bus, _ := kafka.New(kafka.WithWriter(writer))
		_ = bus.Close()

		err := bus.Publish(ctx, &eventbus.Message{Topic: "test", Payload: []byte("test")})
		if !errors.Is(err, kafka.ErrBusClosed) {
			t.Errorf("expected ErrBusClosed, got: %v", err)
		}
	})
}

func TestBus_Subscribe(t *testing.T) {
	ctx := context.Background()

	t.Run("validation", func(t *testing.T) {
		writer := &mockMessageWriter{}
		bus, _ := kafka.New(kafka.WithWriter(writer))
		defer func() { _ = bus.Close() }()

		_, err := bus.Subscribe(ctx, "", func(c context.Context, m *eventbus.Message) error { return nil })
		if !errors.Is(err, kafka.ErrTopicRequired) {
			t.Errorf("expected ErrTopicRequired, got: %v", err)
		}

		_, err = bus.Subscribe(ctx, "orders", nil)
		if !errors.Is(err, kafka.ErrInvalidMessage) {
			t.Errorf("expected ErrInvalidMessage on nil handler, got: %v", err)
		}

		_ = bus.Close()
		_, err = bus.Subscribe(ctx, "orders", func(c context.Context, m *eventbus.Message) error { return nil })
		if !errors.Is(err, kafka.ErrBusClosed) {
			t.Errorf("expected ErrBusClosed, got: %v", err)
		}
	})

	t.Run("successful consumption and auto-ack", func(t *testing.T) {
		writer := &mockMessageWriter{}
		reader := newMockReader()

		bus, _ := kafka.New(
			kafka.WithWriter(writer),
			kafka.WithReaderFactory(func(topic, groupID string, cfg kafka.Config) (kafka.MessageReader, error) {
				return reader, nil
			}),
		)
		defer func() { _ = bus.Close() }()

		var received []*eventbus.Message
		var mu sync.Mutex
		doneCh := make(chan struct{})

		sub, err := bus.Subscribe(ctx, "orders.status", func(c context.Context, m *eventbus.Message) error {
			mu.Lock()
			received = append(received, m)
			if len(received) == 2 {
				close(doneCh)
			}
			mu.Unlock()
			return nil
		}, eventbus.WithConsumerGroup("order-processors"), eventbus.WithAutoAck(true))
		if err != nil {
			t.Fatalf("failed to subscribe: %v", err)
		}

		if sub.Topic() != "orders.status" {
			t.Errorf("expected topic orders.status, got: %s", sub.Topic())
		}

		// Push two messages into mock reader
		reader.msgCh <- kafkago.Message{
			Topic: "orders.status",
			Key:   []byte("ord_1"),
			Value: []byte("pending"),
		}
		reader.msgCh <- kafkago.Message{
			Topic: "orders.status",
			Key:   []byte("ord_2"),
			Value: []byte("shipped"),
		}

		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for messages")
		}

		reader.mu.Lock()
		commits := len(reader.committed)
		reader.mu.Unlock()
		if commits != 2 {
			t.Errorf("expected 2 auto-acknowledged messages, got: %d", commits)
		}

		if err := sub.Unsubscribe(); err != nil {
			t.Errorf("unsubscribe failed: %v", err)
		}
	})

	t.Run("manual ack when auto-ack is false", func(t *testing.T) {
		writer := &mockMessageWriter{}
		reader := newMockReader()

		bus, _ := kafka.New(
			kafka.WithWriter(writer),
			kafka.WithReaderFactory(func(topic, groupID string, cfg kafka.Config) (kafka.MessageReader, error) {
				return reader, nil
			}),
		)
		defer func() { _ = bus.Close() }()

		receivedCh := make(chan struct{})
		sub, err := bus.Subscribe(ctx, "invoices", func(c context.Context, m *eventbus.Message) error {
			close(receivedCh)
			return nil
		}, eventbus.WithAutoAck(false))
		if err != nil {
			t.Fatalf("failed to subscribe: %v", err)
		}

		reader.msgCh <- kafkago.Message{
			Topic: "invoices",
			Value: []byte("invoice_data"),
		}

		select {
		case <-receivedCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for message")
		}

		time.Sleep(50 * time.Millisecond)
		reader.mu.Lock()
		commits := len(reader.committed)
		reader.mu.Unlock()
		if commits != 0 {
			t.Errorf("expected 0 commits when auto-ack is false, got: %d", commits)
		}

		_ = sub.Unsubscribe()
	})

	t.Run("concurrency processing", func(t *testing.T) {
		writer := &mockMessageWriter{}
		reader := newMockReader()

		bus, _ := kafka.New(
			kafka.WithWriter(writer),
			kafka.WithReaderFactory(func(topic, groupID string, cfg kafka.Config) (kafka.MessageReader, error) {
				return reader, nil
			}),
		)
		defer func() { _ = bus.Close() }()

		const total = 20
		var count atomic.Int32
		doneCh := make(chan struct{})

		sub, err := bus.Subscribe(ctx, "concurrent.events", func(c context.Context, m *eventbus.Message) error {
			time.Sleep(10 * time.Millisecond)
			if count.Add(1) == total {
				close(doneCh)
			}
			return nil
		}, eventbus.WithConcurrency(4))
		if err != nil {
			t.Fatalf("subscribe failed: %v", err)
		}

		for i := 0; i < total; i++ {
			reader.msgCh <- kafkago.Message{
				Topic: "concurrent.events",
				Value: []byte("ping"),
			}
		}

		select {
		case <-doneCh:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for concurrent processing, handled: %d", count.Load())
		}

		_ = sub.Unsubscribe()
	})

	t.Run("reader factory error propagation", func(t *testing.T) {
		writer := &mockMessageWriter{}
		bus, _ := kafka.New(
			kafka.WithWriter(writer),
			kafka.WithReaderFactory(func(topic, groupID string, cfg kafka.Config) (kafka.MessageReader, error) {
				return nil, errors.New("cannot create reader")
			}),
		)
		defer func() { _ = bus.Close() }()

		_, err := bus.Subscribe(ctx, "topic", func(c context.Context, m *eventbus.Message) error { return nil })
		if err == nil {
			t.Errorf("expected error from reader factory")
		}
	})
}
