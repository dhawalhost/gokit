package rabbitmq

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dhawalhost/gokit/eventbus"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Header constant for mapping partition/ordering key into AMQP headers.
const (
	HeaderMessageKey = "x-message-key"
)

// Bus implements eventbus.Bus on top of RabbitMQ AMQP 0-9-1.
type Bus struct {
	cfg     Config
	conn    *amqp.Connection
	pubChan Channel
	mu      sync.Mutex
	subs    []*Subscription
	wg      sync.WaitGroup
	closed  atomic.Bool
	closeCh chan struct{}
}

// New creates a new RabbitMQ EventBus.
func New(opts ...Option) (*Bus, error) {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.URL == "" && cfg.ChannelFactory == nil {
		return nil, ErrURLRequired
	}

	b := &Bus{
		cfg:     cfg,
		closeCh: make(chan struct{}),
	}

	if cfg.ChannelFactory != nil {
		ch, err := cfg.ChannelFactory()
		if err != nil {
			return nil, fmt.Errorf("rabbitmq: create publisher channel: %w", err)
		}
		b.pubChan = ch
	} else {
		var conn *amqp.Connection
		var err error
		if cfg.TLS != nil {
			conn, err = amqp.DialTLS(cfg.URL, cfg.TLS)
		} else {
			conn, err = amqp.Dial(cfg.URL)
		}
		if err != nil {
			return nil, fmt.Errorf("rabbitmq: dial: %w", err)
		}
		b.conn = conn

		ch, err := conn.Channel()
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("rabbitmq: open channel: %w", err)
		}
		b.pubChan = ch

		b.cfg.ChannelFactory = func() (Channel, error) {
			return b.conn.Channel()
		}
	}

	// Declare exchange idempotently
	if b.cfg.Exchange != "" {
		err := b.pubChan.ExchangeDeclare(
			b.cfg.Exchange,
			b.cfg.ExchangeType,
			b.cfg.Durable,
			false, // auto-delete
			false, // internal
			false, // no-wait
			nil,
		)
		if err != nil {
			_ = b.pubChan.Close()
			if b.conn != nil {
				_ = b.conn.Close()
			}
			return nil, fmt.Errorf("rabbitmq: declare exchange %s: %w", b.cfg.Exchange, err)
		}
	}

	return b, nil
}

// Publish sends an event message to the RabbitMQ exchange routed by topic.
func (b *Bus) Publish(ctx context.Context, msg *eventbus.Message) error {
	if b.closed.Load() {
		return ErrBusClosed
	}
	if msg == nil || msg.Topic == "" {
		return ErrInvalidMessage
	}

	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}

	headers := make(amqp.Table, len(msg.Headers)+1)
	for k, v := range msg.Headers {
		headers[k] = v
	}
	if msg.Key != "" {
		headers[HeaderMessageKey] = msg.Key
	}

	contentType := "application/json"
	if ct, ok := msg.Headers["content-type"]; ok && ct != "" {
		contentType = ct
	}

	pub := amqp.Publishing{
		MessageId:    msg.ID,
		Timestamp:    msg.Timestamp,
		ContentType:  contentType,
		DeliveryMode: amqp.Persistent,
		Body:         msg.Payload,
		Headers:      headers,
	}

	err := b.pubChan.PublishWithContext(
		ctx,
		b.cfg.Exchange,
		msg.Topic,
		false, // mandatory
		false, // immediate
		pub,
	)
	if err != nil {
		return fmt.Errorf("rabbitmq: publish to %s: %w", msg.Topic, err)
	}

	return nil
}

// Subscribe listens to an AMQP queue bound to the topic pattern on the exchange.
func (b *Bus) Subscribe(ctx context.Context, topic string, handler eventbus.HandlerFunc, opts ...eventbus.SubscribeOption) (eventbus.Subscription, error) {
	if b.closed.Load() {
		return nil, ErrBusClosed
	}
	if topic == "" {
		return nil, ErrTopicRequired
	}
	if handler == nil {
		return nil, fmt.Errorf("%w: handler cannot be nil", ErrInvalidMessage)
	}

	options := eventbus.SubscribeOptions{
		ConsumerGroup: "gokit-workers",
		ConsumerName:  uuid.NewString(),
		AutoAck:       true,
		Concurrency:   1,
	}
	for _, opt := range opts {
		opt(&options)
	}

	ch, err := b.cfg.ChannelFactory()
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: create subscriber channel: %w", err)
	}

	if b.cfg.PrefetchCount > 0 {
		if err := ch.Qos(b.cfg.PrefetchCount, 0, false); err != nil {
			_ = ch.Close()
			return nil, fmt.Errorf("rabbitmq: set qos: %w", err)
		}
	}

	var queueName string
	var durable, autoDelete, exclusive bool

	if options.ConsumerGroup != "" {
		queueName = fmt.Sprintf("%s.%s", options.ConsumerGroup, topic)
		durable = b.cfg.Durable
		autoDelete = false
		exclusive = false
	} else {
		queueName = ""
		durable = false
		autoDelete = true
		exclusive = true
	}

	q, err := ch.QueueDeclare(
		queueName,
		durable,
		autoDelete,
		exclusive,
		false, // no-wait
		nil,
	)
	if err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("rabbitmq: declare queue: %w", err)
	}

	if err := ch.QueueBind(q.Name, topic, b.cfg.Exchange, false, nil); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("rabbitmq: bind queue %s to %s: %w", q.Name, topic, err)
	}

	subCtx, cancel := context.WithCancel(ctx)
	deliveries, err := ch.ConsumeWithContext(
		subCtx,
		q.Name,
		options.ConsumerName,
		false, // autoAck handled explicitly in worker
		exclusive,
		false, // no-local
		false, // no-wait
		nil,
	)
	if err != nil {
		cancel()
		_ = ch.Close()
		return nil, fmt.Errorf("rabbitmq: consume queue %s: %w", q.Name, err)
	}

	sub := &Subscription{
		topic:   topic,
		bus:     b,
		ch:      ch,
		cancel:  cancel,
		done:    make(chan struct{}),
		autoAck: options.AutoAck,
		requeue: b.cfg.RequeueOnNack,
	}

	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()

	workers := options.Concurrency
	if workers <= 0 {
		workers = 1
	}

	var workerWg sync.WaitGroup
	for i := 0; i < workers; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for d := range deliveries {
				evMsg := FromDelivery(d, topic)
				if err := handler(subCtx, evMsg); err == nil {
					if sub.autoAck {
						_ = d.Ack(false)
					}
				} else {
					if sub.autoAck {
						_ = d.Nack(false, sub.requeue)
					}
				}
			}
		}()
	}

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		<-subCtx.Done()
		_ = ch.Close()
		workerWg.Wait()
		close(sub.done)
	}()

	return sub, nil
}

// Close gracefully terminates all active subscriptions and the RabbitMQ connection.
func (b *Bus) Close() error {
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}

	close(b.closeCh)

	b.mu.Lock()
	for _, sub := range b.subs {
		sub.cancel()
	}
	b.mu.Unlock()

	b.wg.Wait()

	var firstErr error
	if b.pubChan != nil {
		if err := b.pubChan.Close(); err != nil {
			firstErr = err
		}
	}
	if b.conn != nil {
		if err := b.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// Subscription represents an active AMQP topic subscription.
type Subscription struct {
	topic   string
	bus     *Bus
	ch      Channel
	cancel  context.CancelFunc
	done    chan struct{}
	autoAck bool
	requeue bool
	closed  atomic.Bool
}

// Topic returns the subscribed topic pattern.
func (s *Subscription) Topic() string {
	return s.topic
}

// Unsubscribe cancels the consumer context and waits for workers to drain.
func (s *Subscription) Unsubscribe() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.cancel()
	<-s.done
	return nil
}

// FromDelivery converts an AMQP Delivery into an eventbus.Message.
func FromDelivery(d amqp.Delivery, fallbackTopic string) *eventbus.Message {
	msg := &eventbus.Message{
		ID:        d.MessageId,
		Topic:     d.RoutingKey,
		Payload:   d.Body,
		Timestamp: d.Timestamp,
		Headers:   make(map[string]string),
	}

	if msg.Topic == "" {
		msg.Topic = fallbackTopic
	}
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}

	for k, v := range d.Headers {
		switch k {
		case HeaderMessageKey:
			if s, ok := v.(string); ok {
				msg.Key = s
			}
		default:
			msg.Headers[k] = fmt.Sprint(v)
		}
	}

	return msg
}
