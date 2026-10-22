package eventbus_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dhawalhost/gokit/eventbus"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestMessage(t *testing.T) {
	tests := []struct {
		name       string
		topic      string
		payload    any
		key        string
		headers    map[string]string
		wantErr    bool
		wantKey    string
		wantHeader string
	}{
		{
			name:       "raw bytes message",
			topic:      "orders.created",
			payload:    []byte(`{"order_id":"123"}`),
			key:        "tenant-1",
			headers:    map[string]string{"env": "prod"},
			wantErr:    false,
			wantKey:    "tenant-1",
			wantHeader: "prod",
		},
		{
			name:       "json message serialization",
			topic:      "users.registered",
			payload:    map[string]string{"email": "test@example.com"},
			key:        "user-99",
			headers:    map[string]string{"region": "us-east"},
			wantErr:    false,
			wantKey:    "user-99",
			wantHeader: "us-east",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var msg *eventbus.Message
			var err error

			switch p := tc.payload.(type) {
			case []byte:
				msg = eventbus.NewMessage(tc.topic, p).
					WithKey(tc.key).
					WithHeaders(tc.headers)
			default:
				msg, err = eventbus.NewJSONMessage(tc.topic, p)
				if err != nil {
					t.Fatalf("unexpected NewJSONMessage error: %v", err)
				}
				msg.WithKey(tc.key).WithHeaders(tc.headers)
			}

			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if msg.Key != tc.wantKey {
				t.Errorf("expected key %s, got %s", tc.wantKey, msg.Key)
			}
			if msg.Headers["env"] != tc.headers["env"] && tc.headers["env"] != "" {
				t.Errorf("header mismatch: %v", msg.Headers)
			}
			if msg.ID == "" {
				t.Errorf("expected auto-generated ID, got empty")
			}
			if msg.Timestamp.IsZero() {
				t.Errorf("expected non-zero timestamp")
			}

			// Test Unmarshal
			var unmarshaled map[string]string
			if err := msg.Unmarshal(&unmarshaled); err != nil {
				t.Errorf("Unmarshal failed: %v", err)
			}
		})
	}
}

func TestMatchTopic(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		topic   string
		want    bool
	}{
		{
			name:    "exact match",
			pattern: "orders.created",
			topic:   "orders.created",
			want:    true,
		},
		{
			name:    "mismatch",
			pattern: "orders.created",
			topic:   "orders.updated",
			want:    false,
		},
		{
			name:    "single token wildcard match",
			pattern: "orders.*",
			topic:   "orders.created",
			want:    true,
		},
		{
			name:    "single token wildcard mismatch nested",
			pattern: "orders.*",
			topic:   "orders.v1.created",
			want:    false,
		},
		{
			name:    "multi token wildcard match",
			pattern: "orders.#",
			topic:   "orders.v1.created",
			want:    true,
		},
		{
			name:    "multi token wildcard gt match",
			pattern: "orders.>",
			topic:   "orders.v2.us.created",
			want:    true,
		},
		{
			name:    "global wildcard all",
			pattern: "#",
			topic:   "any.random.topic",
			want:    true,
		},
		{
			name:    "prefix mismatch",
			pattern: "invoices.*",
			topic:   "orders.created",
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := eventbus.MatchTopic(tc.pattern, tc.topic)
			if got != tc.want {
				t.Errorf("MatchTopic(%q, %q) = %v; want %v", tc.pattern, tc.topic, got, tc.want)
			}
		})
	}
}

func TestMemoryBus(t *testing.T) {
	tests := []struct {
		name         string
		subTopic     string
		pubTopic     string
		payload      string
		shouldHandle bool
	}{
		{
			name:         "exact topic delivery",
			subTopic:     "payments.completed",
			pubTopic:     "payments.completed",
			payload:      "payment-123",
			shouldHandle: true,
		},
		{
			name:         "wildcard topic delivery",
			subTopic:     "payments.*",
			pubTopic:     "payments.refunded",
			payload:      "refund-456",
			shouldHandle: true,
		},
		{
			name:         "mismatched topic no delivery",
			subTopic:     "payments.completed",
			pubTopic:     "orders.completed",
			payload:      "order-789",
			shouldHandle: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bus := eventbus.NewMemoryBus(eventbus.MemoryBusConfig{BufferSize: 10})
			defer bus.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			received := make(chan *eventbus.Message, 1)

			sub, err := bus.Subscribe(ctx, tc.subTopic, func(ctx context.Context, msg *eventbus.Message) error {
				received <- msg
				return nil
			})
			if err != nil {
				t.Fatalf("Subscribe error: %v", err)
			}
			defer sub.Unsubscribe()

			msg := eventbus.NewMessage(tc.pubTopic, []byte(tc.payload))
			if err := bus.Publish(ctx, msg); err != nil {
				t.Fatalf("Publish error: %v", err)
			}

			if tc.shouldHandle {
				select {
				case m := <-received:
					if string(m.Payload) != tc.payload {
						t.Errorf("expected payload %s, got %s", tc.payload, string(m.Payload))
					}
				case <-time.After(500 * time.Millisecond):
					t.Fatalf("timed out waiting for message")
				}
			} else {
				select {
				case m := <-received:
					t.Fatalf("unexpected message received: %v", m)
				case <-time.After(100 * time.Millisecond):
					// Expected no message
				}
			}
		})
	}

	t.Run("fanout and unsubscribe", func(t *testing.T) {
		bus := eventbus.NewMemoryBus()
		defer bus.Close()

		ctx := context.Background()
		var count atomic.Int32

		sub1, err := bus.Subscribe(ctx, "notifications", func(ctx context.Context, msg *eventbus.Message) error {
			count.Add(1)
			return nil
		})
		if err != nil {
			t.Fatalf("sub1 error: %v", err)
		}

		sub2, err := bus.Subscribe(ctx, "notifications", func(ctx context.Context, msg *eventbus.Message) error {
			count.Add(1)
			return nil
		})
		if err != nil {
			t.Fatalf("sub2 error: %v", err)
		}

		// First publish: both receive
		_ = bus.Publish(ctx, eventbus.NewMessage("notifications", []byte("hello")))
		time.Sleep(50 * time.Millisecond)

		if got := count.Load(); got != 2 {
			t.Errorf("expected 2 receipts from fanout, got %d", got)
		}

		// Unsubscribe sub1
		_ = sub1.Unsubscribe()
		_ = sub2.Topic()

		// Second publish: only sub2 receives
		_ = bus.Publish(ctx, eventbus.NewMessage("notifications", []byte("world")))
		time.Sleep(50 * time.Millisecond)

		if got := count.Load(); got != 3 {
			t.Errorf("expected 3 total receipts after unsubscribe, got %d", got)
		}
	})

	t.Run("closed bus errors", func(t *testing.T) {
		bus := eventbus.NewMemoryBus()
		_ = bus.Close()

		err := bus.Publish(context.Background(), eventbus.NewMessage("test", []byte("data")))
		if err == nil {
			t.Errorf("expected publish error on closed bus, got nil")
		}

		_, err = bus.Subscribe(context.Background(), "test", func(ctx context.Context, msg *eventbus.Message) error {
			return nil
		})
		if err == nil {
			t.Errorf("expected subscribe error on closed bus, got nil")
		}
	})
}

func TestMiddlewares(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T) eventbus.HandlerFunc
		expectErr bool
	}{
		{
			name: "recovery middleware converts panic to error",
			setup: func(t *testing.T) eventbus.HandlerFunc {
				rawHandler := func(ctx context.Context, msg *eventbus.Message) error {
					panic("something went critically wrong")
				}
				return eventbus.RecoveryMiddleware()(rawHandler)
			},
			expectErr: true,
		},
		{
			name: "retry middleware succeeds on eventual attempt",
			setup: func(t *testing.T) eventbus.HandlerFunc {
				var attempts int
				rawHandler := func(ctx context.Context, msg *eventbus.Message) error {
					attempts++
					if attempts < 3 {
						return errors.New("transient error")
					}
					return nil
				}
				return eventbus.RetryMiddleware(3, 5*time.Millisecond)(rawHandler)
			},
			expectErr: false,
		},
		{
			name: "retry middleware fails after exhausting max retries",
			setup: func(t *testing.T) eventbus.HandlerFunc {
				rawHandler := func(ctx context.Context, msg *eventbus.Message) error {
					return errors.New("permanent failure")
				}
				return eventbus.RetryMiddleware(2, 5*time.Millisecond)(rawHandler)
			},
			expectErr: true,
		},
		{
			name: "dead letter middleware forwards failed messages to dlq",
			setup: func(t *testing.T) eventbus.HandlerFunc {
				dlqBus := eventbus.NewMemoryBus()
				t.Cleanup(func() { dlqBus.Close() })

				var dlqReceived atomic.Bool
				_, _ = dlqBus.Subscribe(context.Background(), "dead-letters", func(ctx context.Context, msg *eventbus.Message) error {
					if msg.Headers["x-death-reason"] != "" && msg.Headers["x-original-topic"] == "orders.created" {
						dlqReceived.Store(true)
					}
					return nil
				})

				rawHandler := func(ctx context.Context, msg *eventbus.Message) error {
					return errors.New("poison pill")
				}

				mw := eventbus.DeadLetterMiddleware(dlqBus, "dead-letters")
				handler := mw(rawHandler)

				// Wrap to verify dlq receipt afterwards
				return func(ctx context.Context, msg *eventbus.Message) error {
					err := handler(ctx, msg)
					time.Sleep(50 * time.Millisecond)
					if !dlqReceived.Load() {
						t.Errorf("expected message to be sent to dead-letters topic")
					}
					return err
				}
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := tc.setup(t)
			msg := eventbus.NewMessage("orders.created", []byte("test"))
			err := handler(context.Background(), msg)

			if tc.expectErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.expectErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	t.Run("tracing middleware and propagation", func(t *testing.T) {
		tp := sdktrace.NewTracerProvider()
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(propagation.TraceContext{})

		tracer := tp.Tracer("test")
		ctx, span := tracer.Start(context.Background(), "publisher.span")
		defer span.End()

		msg := eventbus.NewMessage("events.audit", []byte("payload"))
		eventbus.InjectTracing(ctx, msg)

		if msg.Headers["traceparent"] == "" {
			t.Fatalf("expected traceparent header to be injected")
		}

		var traceContextReceived bool
		handler := eventbus.TracingMiddleware(tracer)(func(ctx context.Context, msg *eventbus.Message) error {
			if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
				traceContextReceived = true
			}
			return nil
		})

		if err := handler(context.Background(), msg); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if !traceContextReceived {
			t.Errorf("expected consumer span to have valid trace context extracted")
		}
	})
}

func TestOutboxAdapter(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer bus.Close()

	adapter := eventbus.NewOutboxPublisher(bus)

	var wg sync.WaitGroup
	wg.Add(1)

	var received *eventbus.Message
	_, err := bus.Subscribe(context.Background(), "invoices.issued", func(ctx context.Context, msg *eventbus.Message) error {
		received = msg
		wg.Done()
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	headers := map[string]string{"tenant_id": "tenant-xyz"}
	err = adapter.Publish(context.Background(), "invoices.issued", "inv-100", []byte("invoice-data"), headers)
	if err != nil {
		t.Fatalf("outbox adapter Publish error: %v", err)
	}

	wg.Wait()

	if received == nil {
		t.Fatalf("expected message to be received by bus")
	}
	if received.Key != "inv-100" {
		t.Errorf("expected key inv-100, got %s", received.Key)
	}
	if received.Headers["tenant_id"] != "tenant-xyz" {
		t.Errorf("expected tenant_id tenant-xyz, got %s", received.Headers["tenant_id"])
	}
}

func TestRedisBus(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	bus, err := eventbus.NewRedisBus(eventbus.RedisBusConfig{
		Client:       client,
		StreamPrefix: "test-stream:",
		BlockTimeout: 50 * time.Millisecond,
		BatchSize:    5,
	})
	if err != nil {
		t.Fatalf("NewRedisBus error: %v", err)
	}
	defer bus.Close()

	t.Run("publish and subscribe using consumer group", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		var received atomic.Int32
		var lastPayload string
		var mu sync.Mutex

		sub, err := bus.Subscribe(ctx, "tasks.created", func(ctx context.Context, msg *eventbus.Message) error {
			mu.Lock()
			lastPayload = string(msg.Payload)
			mu.Unlock()
			received.Add(1)
			return nil
		}, eventbus.WithConsumerGroup("test-workers"))

		if err != nil {
			t.Fatalf("Subscribe error: %v", err)
		}
		defer sub.Unsubscribe()

		msg := eventbus.NewMessage("tasks.created", []byte("task-1"))
		if err := bus.Publish(ctx, msg); err != nil {
			t.Fatalf("Publish error: %v", err)
		}

		// Wait for message consumption
		deadline := time.Now().Add(1 * time.Second)
		for time.Now().Before(deadline) {
			if received.Load() > 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}

		if got := received.Load(); got != 1 {
			t.Errorf("expected 1 message processed, got %d", got)
		}
		mu.Lock()
		if lastPayload != "task-1" {
			t.Errorf("expected payload task-1, got %s", lastPayload)
		}
		mu.Unlock()
	})

	t.Run("second subscription with same group handles BUSYGROUP and sub.Topic", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		sub2, err := bus.Subscribe(ctx, "tasks.created", func(_ context.Context, _ *eventbus.Message) error {
			return nil
		}, eventbus.WithConsumerGroup("test-workers"), eventbus.WithConsumerName("worker-2"), eventbus.WithConcurrency(2), eventbus.WithAutoAck(true))

		if err != nil {
			t.Fatalf("expected BUSYGROUP to be handled cleanly, got: %v", err)
		}
		defer sub2.Unsubscribe()

		if sub2.Topic() != "tasks.created" {
			t.Errorf("expected topic tasks.created, got %s", sub2.Topic())
		}
	})
}

func TestSubscribeOptions_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		opts     []eventbus.SubscribeOption
		validate func(t *testing.T, o *eventbus.SubscribeOptions)
	}{
		{
			name: "custom options configured correctly",
			opts: []eventbus.SubscribeOption{
				eventbus.WithConsumerGroup("grp-1"),
				eventbus.WithConsumerName("worker-alpha"),
				eventbus.WithAutoAck(true),
				eventbus.WithConcurrency(4),
			},
			validate: func(t *testing.T, o *eventbus.SubscribeOptions) {
				if o.ConsumerGroup != "grp-1" {
					t.Errorf("expected grp-1, got %s", o.ConsumerGroup)
				}
				if o.ConsumerName != "worker-alpha" {
					t.Errorf("expected worker-alpha, got %s", o.ConsumerName)
				}
				if !o.AutoAck {
					t.Error("expected AutoAck to be true")
				}
				if o.Concurrency != 4 {
					t.Errorf("expected Concurrency 4, got %d", o.Concurrency)
				}
			},
		},
		{
			name: "negative concurrency ignored",
			opts: []eventbus.SubscribeOption{
				eventbus.WithConcurrency(-1),
			},
			validate: func(t *testing.T, o *eventbus.SubscribeOptions) {
				if o.Concurrency != 0 {
					t.Errorf("expected Concurrency 0, got %d", o.Concurrency)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var o eventbus.SubscribeOptions
			for _, opt := range tc.opts {
				opt(&o)
			}
			tc.validate(t, &o)
		})
	}
}

func TestChainMiddleware_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		order     []string
		wantOrder string
	}{
		{
			name:      "chains middlewares in outer-to-inner order",
			order:     nil,
			wantOrder: "m1->m2->handler",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var steps []string
			m1 := func(next eventbus.HandlerFunc) eventbus.HandlerFunc {
				return func(ctx context.Context, msg *eventbus.Message) error {
					steps = append(steps, "m1")
					return next(ctx, msg)
				}
			}
			m2 := func(next eventbus.HandlerFunc) eventbus.HandlerFunc {
				return func(ctx context.Context, msg *eventbus.Message) error {
					steps = append(steps, "m2")
					return next(ctx, msg)
				}
			}

			chain := eventbus.Chain(m1, m2)
			handler := chain(func(_ context.Context, _ *eventbus.Message) error {
				steps = append(steps, "handler")
				return nil
			})

			_ = handler(context.Background(), eventbus.NewMessage("test", nil))

			got := ""
			for i, s := range steps {
				if i > 0 {
					got += "->"
				}
				got += s
			}
			if got != tc.wantOrder {
				t.Errorf("expected execution order %q, got %q", tc.wantOrder, got)
			}
		})
	}
}

func TestHeaderCarrier_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		initial  eventbus.HeaderCarrier
		setKey   string
		setVal   string
		wantKeys []string
	}{
		{
			name:     "Keys, Get, and Set work correctly",
			initial:  eventbus.HeaderCarrier{"trace-id": "123", "span-id": "456"},
			setKey:   "user-id",
			setVal:   "u-789",
			wantKeys: []string{"trace-id", "span-id", "user-id"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.initial.Set(tc.setKey, tc.setVal)
			if got := tc.initial.Get(tc.setKey); got != tc.setVal {
				t.Errorf("expected %q, got %q", tc.setVal, got)
			}
			keys := tc.initial.Keys()
			if len(keys) != len(tc.wantKeys) {
				t.Fatalf("expected %d keys, got %d", len(tc.wantKeys), len(keys))
			}
		})
	}
}

func TestMessage_Unmarshal_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{
			name:    "valid JSON unmarshals successfully",
			payload: []byte(`{"user":"bob"}`),
			wantErr: false,
		},
		{
			name:    "empty payload returns error",
			payload: nil,
			wantErr: true,
		},
		{
			name:    "invalid JSON returns error",
			payload: []byte(`{invalid`),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := eventbus.NewMessage("test", tc.payload)
			var target map[string]string
			err := msg.Unmarshal(&target)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
			if !tc.wantErr && target["user"] != "bob" {
				t.Errorf("expected bob, got %s", target["user"])
			}
		})
	}
}

func TestEventBus_ClosedAndMessageEdgeCases_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "NewJSONMessage error and WithHeader on nil headers map",
			run: func(t *testing.T) {
				// Invalid json payload
				if _, err := eventbus.NewJSONMessage("topic", make(chan int)); err == nil {
					t.Error("expected error when marshaling channel")
				}

				// WithHeader when Headers is nil
				msg := &eventbus.Message{Topic: "topic"}
				msg = msg.WithHeader("X-Key", "value")
				if msg.Headers["X-Key"] != "value" {
					t.Errorf("expected header X-Key to be value, got %s", msg.Headers["X-Key"])
				}

				// WithHeaders when Headers is nil
				msg2 := &eventbus.Message{Topic: "topic"}
				msg2 = msg2.WithHeaders(map[string]string{"H1": "V1", "H2": "V2"})
				if msg2.Headers["H1"] != "V1" || msg2.Headers["H2"] != "V2" {
					t.Errorf("expected headers set, got %+v", msg2.Headers)
				}
			},
		},
		{
			name: "MemoryBus error after Close and auto ID generation",
			run: func(t *testing.T) {
				bus := eventbus.NewMemoryBus()
				ctx := context.Background()

				// Nil message error
				if err := bus.Publish(ctx, nil); err == nil {
					t.Error("expected error when publishing nil message")
				}

				// Auto timestamp assignment when Timestamp is zero
				msg := &eventbus.Message{Topic: "test.topic", Payload: []byte("data")}
				if err := bus.Publish(ctx, msg); err != nil {
					t.Fatalf("Publish error: %v", err)
				}
				if msg.Timestamp.IsZero() {
					t.Error("expected Timestamp to be auto-stamped on Publish")
				}

				// Close bus
				if err := bus.Close(); err != nil {
					t.Fatalf("Close error: %v", err)
				}

				// Publish after Close
				if err := bus.Publish(ctx, msg); err == nil {
					t.Error("expected error when publishing on closed MemoryBus")
				}

				// Subscribe after Close
				if _, err := bus.Subscribe(ctx, "test.topic", func(_ context.Context, _ *eventbus.Message) error { return nil }); err == nil {
					t.Error("expected error when subscribing on closed MemoryBus")
				}
			},
		},
		{
			name: "RedisBus zero config defaults and closed error",
			run: func(t *testing.T) {
				// Nil client returns error
				if _, err := eventbus.NewRedisBus(eventbus.RedisBusConfig{Client: nil}); err == nil {
					t.Error("expected error when Client is nil")
				}

				mr := miniredis.RunT(t)
				rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				defer rdb.Close()

				// Tests zero config defaults (BatchSize=10, BlockTimeout=2s, StreamPrefix, DefaultGroup)
				rb, err := eventbus.NewRedisBus(eventbus.RedisBusConfig{Client: rdb})
				if err != nil {
					t.Fatalf("NewRedisBus failed: %v", err)
				}
				ctx := context.Background()

				// Nil message error
				if err := rb.Publish(ctx, nil); err == nil {
					t.Error("expected error when publishing nil message on RedisBus")
				}

				// Auto ID assignment
				msgNoID := &eventbus.Message{Topic: "test.topic", Payload: []byte("foo")}
				_ = rb.Publish(ctx, msgNoID)
				if msgNoID.ID == "" {
					t.Error("expected ID to be assigned on RedisBus Publish")
				}

				if err := rb.Close(); err != nil {
					t.Fatalf("Close error: %v", err)
				}

				msg := eventbus.NewMessage("test.topic", []byte("foo"))
				if err := rb.Publish(ctx, msg); err == nil {
					t.Error("expected error when publishing on closed RedisBus")
				}

				if _, err := rb.Subscribe(ctx, "test.topic", func(_ context.Context, _ *eventbus.Message) error { return nil }); err == nil {
					t.Error("expected error when subscribing on closed RedisBus")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
