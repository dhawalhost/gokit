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
}
