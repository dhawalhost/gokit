package rabbitmq_test

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/eventbus"
	"github.com/dhawalhost/gokit/eventbus/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

type mockAMQPChannel struct {
	mu            sync.Mutex
	closed        bool
	published     []amqp.Publishing
	exchanges     []string
	queues        []string
	boundQueues   []string
	deliveryCh    chan amqp.Delivery
	failExchange  bool
	failPublish   bool
	failQos       bool
	failQueueDecl bool
	failQueueBind bool
	failConsume   bool
}

func newMockAMQPChannel() *mockAMQPChannel {
	return &mockAMQPChannel{
		deliveryCh: make(chan amqp.Delivery, 100),
	}
}

func (m *mockAMQPChannel) PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPublish {
		return errors.New("amqp: publish failure")
	}
	m.published = append(m.published, msg)
	return nil
}

func (m *mockAMQPChannel) ConsumeWithContext(ctx context.Context, queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failConsume {
		return nil, errors.New("amqp: consume failure")
	}
	return m.deliveryCh, nil
}

func (m *mockAMQPChannel) QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failQueueDecl {
		return amqp.Queue{}, errors.New("amqp: queue declare failure")
	}
	if name == "" {
		name = "amq.gen-random123"
	}
	m.queues = append(m.queues, name)
	return amqp.Queue{Name: name}, nil
}

func (m *mockAMQPChannel) QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failQueueBind {
		return errors.New("amqp: queue bind failure")
	}
	m.boundQueues = append(m.boundQueues, name)
	return nil
}

func (m *mockAMQPChannel) ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failExchange {
		return errors.New("amqp: exchange declare failure")
	}
	m.exchanges = append(m.exchanges, name)
	return nil
}

func (m *mockAMQPChannel) Qos(prefetchCount, prefetchSize int, global bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failQos {
		return errors.New("amqp: qos failure")
	}
	return nil
}

func (m *mockAMQPChannel) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.deliveryCh)
	}
	return nil
}

type mockAcknowledger struct {
	mu     sync.Mutex
	acked  int
	nacked int
}

func (a *mockAcknowledger) Ack(tag uint64, multiple bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.acked++
	return nil
}

func (a *mockAcknowledger) Nack(tag uint64, multiple bool, requeue bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nacked++
	return nil
}

func (a *mockAcknowledger) Reject(tag uint64, requeue bool) error {
	return a.Nack(tag, false, requeue)
}

func TestNew_Configuration(t *testing.T) {
	t.Run("fails without URL or ChannelFactory", func(t *testing.T) {
		_, err := rabbitmq.New()
		if !errors.Is(err, rabbitmq.ErrURLRequired) {
			t.Fatalf("expected ErrURLRequired, got: %v", err)
		}
	})

	t.Run("fails on exchange declare error", func(t *testing.T) {
		ch := newMockAMQPChannel()
		ch.failExchange = true

		_, err := rabbitmq.New(
			rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) {
				return ch, nil
			}),
		)
		if err == nil {
			t.Fatalf("expected error declaring exchange")
		}
	})

	t.Run("fails when channel factory returns error", func(t *testing.T) {
		_, err := rabbitmq.New(
			rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) {
				return nil, errors.New("cannot create channel")
			}),
		)
		if err == nil {
			t.Fatalf("expected error from channel factory")
		}
	})

	t.Run("fails on dial error", func(t *testing.T) {
		_, err := rabbitmq.New(
			rabbitmq.WithURL("amqp://127.0.0.1:65530/"),
		)
		if err == nil {
			t.Fatalf("expected dial error")
		}

		_, err = rabbitmq.New(
			rabbitmq.WithURL("amqps://127.0.0.1:65530/"),
			rabbitmq.WithTLS(&tls.Config{InsecureSkipVerify: true}),
		)
		if err == nil {
			t.Fatalf("expected dial TLS error")
		}
	})

	t.Run("succeeds with functional options", func(t *testing.T) {
		ch := newMockAMQPChannel()
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13}

		bus, err := rabbitmq.New(
			rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) {
				return ch, nil
			}),
			rabbitmq.WithExchange("my_events"),
			rabbitmq.WithExchangeType("topic"),
			rabbitmq.WithDurable(true),
			rabbitmq.WithPrefetchCount(100),
			rabbitmq.WithRequeueOnNack(false),
			rabbitmq.WithTLS(tlsCfg),
			rabbitmq.WithURL("amqps://localhost:5671"),
		)
		if err != nil {
			t.Fatalf("failed to create bus: %v", err)
		}
		defer func() { _ = bus.Close() }()
	})
}

func TestBus_Publish(t *testing.T) {
	ctx := context.Background()

	t.Run("validation", func(t *testing.T) {
		ch := newMockAMQPChannel()
		bus, _ := rabbitmq.New(rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) { return ch, nil }))
		defer func() { _ = bus.Close() }()

		if err := bus.Publish(ctx, nil); !errors.Is(err, rabbitmq.ErrInvalidMessage) {
			t.Errorf("expected ErrInvalidMessage on nil msg, got: %v", err)
		}

		if err := bus.Publish(ctx, &eventbus.Message{}); !errors.Is(err, rabbitmq.ErrInvalidMessage) {
			t.Errorf("expected ErrInvalidMessage on empty topic, got: %v", err)
		}

		ch.failPublish = true
		if err := bus.Publish(ctx, &eventbus.Message{Topic: "orders.created", Payload: []byte("1")}); err == nil {
			t.Errorf("expected publish error from channel")
		}

		_ = bus.Close()
		if err := bus.Publish(ctx, &eventbus.Message{Topic: "orders.created", Payload: []byte("1")}); !errors.Is(err, rabbitmq.ErrBusClosed) {
			t.Errorf("expected ErrBusClosed, got: %v", err)
		}
	})

	t.Run("successful publish", func(t *testing.T) {
		ch := newMockAMQPChannel()
		bus, _ := rabbitmq.New(rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) { return ch, nil }))
		defer func() { _ = bus.Close() }()

		msg := &eventbus.Message{
			Topic:   "payments.processed",
			Key:     "tenant_xyz",
			Payload: []byte(`{"id":"pay_1"}`),
			Headers: map[string]string{
				"content-type": "application/json",
				"traceparent":  "00-abc-01",
			},
		}

		if err := bus.Publish(ctx, msg); err != nil {
			t.Fatalf("publish failed: %v", err)
		}

		if len(ch.published) != 1 {
			t.Fatalf("expected 1 published message, got: %d", len(ch.published))
		}
		pub := ch.published[0]
		if string(pub.Body) != `{"id":"pay_1"}` {
			t.Errorf("unexpected body: %s", string(pub.Body))
		}
		if pub.Headers[rabbitmq.HeaderMessageKey] != "tenant_xyz" {
			t.Errorf("missing partition key in headers")
		}
	})
}

func TestBus_Subscribe(t *testing.T) {
	ctx := context.Background()

	t.Run("validation", func(t *testing.T) {
		ch := newMockAMQPChannel()
		bus, _ := rabbitmq.New(rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) { return ch, nil }))
		defer func() { _ = bus.Close() }()

		_, err := bus.Subscribe(ctx, "", func(c context.Context, m *eventbus.Message) error { return nil })
		if !errors.Is(err, rabbitmq.ErrTopicRequired) {
			t.Errorf("expected ErrTopicRequired, got: %v", err)
		}

		_, err = bus.Subscribe(ctx, "topic", nil)
		if !errors.Is(err, rabbitmq.ErrInvalidMessage) {
			t.Errorf("expected ErrInvalidMessage on nil handler, got: %v", err)
		}

		_ = bus.Close()
		_, err = bus.Subscribe(ctx, "topic", func(c context.Context, m *eventbus.Message) error { return nil })
		if !errors.Is(err, rabbitmq.ErrBusClosed) {
			t.Errorf("expected ErrBusClosed, got: %v", err)
		}
	})

	t.Run("successful consumption and auto-ack", func(t *testing.T) {
		pubCh := newMockAMQPChannel()
		subCh := newMockAMQPChannel()

		var channels = []rabbitmq.Channel{pubCh, subCh}
		var chanIdx int

		bus, _ := rabbitmq.New(
			rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) {
				ch := channels[chanIdx]
				chanIdx++
				return ch, nil
			}),
		)
		defer func() { _ = bus.Close() }()

		acker := &mockAcknowledger{}
		doneCh := make(chan struct{})

		sub, err := bus.Subscribe(ctx, "inventory.*", func(c context.Context, m *eventbus.Message) error {
			if string(m.Payload) == "item_reserved" {
				close(doneCh)
			}
			return nil
		}, eventbus.WithConsumerGroup("inventory-workers"), eventbus.WithAutoAck(true))
		if err != nil {
			t.Fatalf("subscribe failed: %v", err)
		}

		if sub.Topic() != "inventory.*" {
			t.Errorf("unexpected topic: %s", sub.Topic())
		}

		// Inject delivery into subscriber channel
		subCh.deliveryCh <- amqp.Delivery{
			MessageId:    "msg_101",
			RoutingKey:   "inventory.reserved",
			Body:         []byte("item_reserved"),
			Timestamp:    time.Now().UTC(),
			Acknowledger: acker,
		}

		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for delivery")
		}

		time.Sleep(50 * time.Millisecond)
		acker.mu.Lock()
		acks := acker.acked
		acker.mu.Unlock()
		if acks != 1 {
			t.Errorf("expected 1 ack, got: %d", acks)
		}

		if err := sub.Unsubscribe(); err != nil {
			t.Errorf("unsubscribe failed: %v", err)
		}
	})

	t.Run("nack on handler error", func(t *testing.T) {
		pubCh := newMockAMQPChannel()
		subCh := newMockAMQPChannel()

		var channels = []rabbitmq.Channel{pubCh, subCh}
		var chanIdx int

		bus, _ := rabbitmq.New(
			rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) {
				ch := channels[chanIdx]
				chanIdx++
				return ch, nil
			}),
		)
		defer func() { _ = bus.Close() }()

		acker := &mockAcknowledger{}
		doneCh := make(chan struct{})

		sub, err := bus.Subscribe(ctx, "tasks", func(c context.Context, m *eventbus.Message) error {
			close(doneCh)
			return errors.New("handler failed")
		}, eventbus.WithAutoAck(true))
		if err != nil {
			t.Fatalf("subscribe failed: %v", err)
		}

		subCh.deliveryCh <- amqp.Delivery{
			RoutingKey:   "tasks",
			Body:         []byte("fail_me"),
			Acknowledger: acker,
		}

		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for delivery")
		}

		time.Sleep(50 * time.Millisecond)
		acker.mu.Lock()
		nacks := acker.nacked
		acker.mu.Unlock()
		if nacks != 1 {
			t.Errorf("expected 1 nack, got: %d", nacks)
		}

		_ = sub.Unsubscribe()
	})

	t.Run("concurrency processing", func(t *testing.T) {
		pubCh := newMockAMQPChannel()
		subCh := newMockAMQPChannel()

		var channels = []rabbitmq.Channel{pubCh, subCh}
		var chanIdx int

		bus, _ := rabbitmq.New(
			rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) {
				ch := channels[chanIdx]
				chanIdx++
				return ch, nil
			}),
		)
		defer func() { _ = bus.Close() }()

		const total = 10
		var count atomic.Int32
		doneCh := make(chan struct{})

		acker := &mockAcknowledger{}
		sub, err := bus.Subscribe(ctx, "bulk", func(c context.Context, m *eventbus.Message) error {
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
			subCh.deliveryCh <- amqp.Delivery{
				RoutingKey:   "bulk",
				Body:         []byte("batch"),
				Acknowledger: acker,
			}
		}

		select {
		case <-doneCh:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for concurrent processing, count: %d", count.Load())
		}

		_ = sub.Unsubscribe()
	})

	t.Run("channel step failure branches", func(t *testing.T) {
		// Qos failure
		chQos := newMockAMQPChannel()
		chQos.failQos = true
		b, _ := rabbitmq.New(rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) { return chQos, nil }))
		_, err := b.Subscribe(ctx, "t", func(c context.Context, m *eventbus.Message) error { return nil })
		if err == nil {
			t.Errorf("expected qos error")
		}

		// QueueDeclare failure
		chQD := newMockAMQPChannel()
		chQD.failQueueDecl = true
		b2, _ := rabbitmq.New(rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) { return chQD, nil }))
		_, err = b2.Subscribe(ctx, "t", func(c context.Context, m *eventbus.Message) error { return nil })
		if err == nil {
			t.Errorf("expected queue declare error")
		}

		// QueueBind failure
		chQB := newMockAMQPChannel()
		chQB.failQueueBind = true
		b3, _ := rabbitmq.New(rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) { return chQB, nil }))
		_, err = b3.Subscribe(ctx, "t", func(c context.Context, m *eventbus.Message) error { return nil })
		if err == nil {
			t.Errorf("expected queue bind error")
		}

		// Consume failure
		// Consume failure
		chC := newMockAMQPChannel()
		chC.failConsume = true
		b4, _ := rabbitmq.New(rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) { return chC, nil }))
		_, err = b4.Subscribe(ctx, "t", func(c context.Context, m *eventbus.Message) error { return nil })
		if err == nil {
			t.Errorf("expected consume error")
		}

		// ChannelFactory failure on Subscribe
		var callCount int
		b5, _ := rabbitmq.New(rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) {
			callCount++
			if callCount > 1 {
				return nil, errors.New("cannot create subscriber channel")
			}
			return newMockAMQPChannel(), nil
		}))
		_, err = b5.Subscribe(ctx, "t", func(c context.Context, m *eventbus.Message) error { return nil })
		if err == nil {
			t.Errorf("expected subscriber channel creation error")
		}
	})

	t.Run("idempotent close and unsubscribe", func(t *testing.T) {
		ch := newMockAMQPChannel()
		bus, _ := rabbitmq.New(rabbitmq.WithChannelFactory(func() (rabbitmq.Channel, error) { return ch, nil }))

		sub, err := bus.Subscribe(ctx, "topic", func(c context.Context, m *eventbus.Message) error { return nil })
		if err != nil {
			t.Fatalf("failed to subscribe: %v", err)
		}

		if err := sub.Unsubscribe(); err != nil {
			t.Errorf("unsubscribe failed: %v", err)
		}
		if err := sub.Unsubscribe(); err != nil {
			t.Errorf("second unsubscribe failed: %v", err)
		}

		if err := bus.Close(); err != nil {
			t.Errorf("close failed: %v", err)
		}
		if err := bus.Close(); err != nil {
			t.Errorf("second close failed: %v", err)
		}
	})
}

func TestFromDelivery(t *testing.T) {
	d := amqp.Delivery{
		MessageId:  "d_123",
		RoutingKey: "orders.placed",
		Body:       []byte("payload"),
		Headers: amqp.Table{
			rabbitmq.HeaderMessageKey: "order_partition_key",
			"x-custom-attr":           "val1",
		},
	}

	msg := rabbitmq.FromDelivery(d, "fallback")
	if msg.ID != "d_123" {
		t.Errorf("expected ID d_123, got: %s", msg.ID)
	}
	if msg.Topic != "orders.placed" {
		t.Errorf("expected Topic orders.placed, got: %s", msg.Topic)
	}
	if msg.Key != "order_partition_key" {
		t.Errorf("expected Key order_partition_key, got: %s", msg.Key)
	}
	if msg.Headers["x-custom-attr"] != "val1" {
		t.Errorf("missing header x-custom-attr")
	}

	// Fallback topic and ID
	dEmpty := amqp.Delivery{}
	msgEmpty := rabbitmq.FromDelivery(dEmpty, "fallback_topic")
	if msgEmpty.Topic != "fallback_topic" {
		t.Errorf("expected fallback_topic, got: %s", msgEmpty.Topic)
	}
	if msgEmpty.ID == "" {
		t.Errorf("expected generated UUID ID")
	}
}
