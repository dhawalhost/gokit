package kafka

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dhawalhost/gokit/eventbus"
	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
)

// Header constants used for mapping eventbus metadata to Kafka headers.
const (
	HeaderMessageID    = "x-message-id"
	HeaderTopic        = "x-topic"
	HeaderPartitionKey = "x-partition-key"
)

// Bus implements eventbus.Bus on top of Apache Kafka.
type Bus struct {
	cfg     Config
	writer  MessageWriter
	mu      sync.Mutex
	subs    []*Subscription
	wg      sync.WaitGroup
	closed  atomic.Bool
	closeCh chan struct{}
}

// New creates a new Kafka-backed EventBus.
func New(opts ...Option) (*Bus, error) {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	if len(cfg.Brokers) == 0 && cfg.Writer == nil {
		return nil, ErrBrokersRequired
	}

	b := &Bus{
		cfg:     cfg,
		closeCh: make(chan struct{}),
	}

	if cfg.Writer != nil {
		b.writer = cfg.Writer
	} else {
		var transport kafkago.RoundTripper
		if cfg.TLS != nil || cfg.Dialer != nil {
			transport = &kafkago.Transport{
				TLS: cfg.TLS,
				Dial: func(ctx context.Context, network string, address string) (net.Conn, error) {
					if cfg.Dialer != nil {
						return cfg.Dialer.DialContext(ctx, network, address)
					}
					return (&net.Dialer{}).DialContext(ctx, network, address)
				},
			}
		}

		b.writer = &kafkago.Writer{
			Addr:         kafkago.TCP(cfg.Brokers...),
			Balancer:     cfg.Balancer,
			RequiredAcks: cfg.RequiredAcks,
			MaxAttempts:  cfg.MaxAttempts,
			BatchSize:    cfg.BatchSize,
			BatchTimeout: cfg.BatchTimeout,
			WriteTimeout: cfg.WriteTimeout,
			ReadTimeout:  cfg.ReadTimeout,
			Compression:  cfg.Compression,
			Transport:    transport,
		}
	}

	if b.cfg.ReaderFactory == nil {
		b.cfg.ReaderFactory = defaultReaderFactory
	}

	return b, nil
}

func defaultReaderFactory(topic string, groupID string, conf Config) (MessageReader, error) {
	rConfig := kafkago.ReaderConfig{
		Brokers:        conf.Brokers,
		Topic:          topic,
		GroupID:        groupID,
		MaxAttempts:    conf.MaxAttempts,
		QueueCapacity:  conf.BatchSize,
		CommitInterval: 0,
		Dialer:         conf.Dialer,
	}
	if conf.ReadTimeout > 0 {
		rConfig.MaxWait = conf.ReadTimeout
	}
	return kafkago.NewReader(rConfig), nil
}

// Publish writes an event message to Kafka.
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

	kmsg := ToKafkaMessage(msg)
	if err := b.writer.WriteMessages(ctx, kmsg); err != nil {
		return fmt.Errorf("kafka: publish to %s: %w", msg.Topic, err)
	}

	return nil
}

// Subscribe listens to a Kafka topic.
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

	reader, err := b.cfg.ReaderFactory(topic, options.ConsumerGroup, b.cfg)
	if err != nil {
		return nil, fmt.Errorf("kafka: create reader: %w", err)
	}

	subCtx, cancel := context.WithCancel(ctx)
	sub := &Subscription{
		topic:   topic,
		bus:     b,
		reader:  reader,
		cancel:  cancel,
		done:    make(chan struct{}),
		autoAck: options.AutoAck,
	}

	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()

	workers := options.Concurrency
	if workers <= 0 {
		workers = 1
	}

	workCh := make(chan kafkago.Message, options.Concurrency*10)

	// Worker goroutines
	var workerWg sync.WaitGroup
	for i := 0; i < workers; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for kmsg := range workCh {
				evMsg := FromKafkaMessage(kmsg, topic)
				if err := handler(subCtx, evMsg); err == nil && sub.autoAck {
					_ = sub.reader.CommitMessages(subCtx, kmsg)
				}
			}
		}()
	}

	// Fetch loop
	b.wg.Add(1)
	go func() {
		defer func() {
			close(workCh)
			workerWg.Wait()
			close(sub.done)
			b.wg.Done()
		}()

		for {
			select {
			case <-subCtx.Done():
				return
			case <-b.closeCh:
				return
			default:
			}

			kmsg, err := sub.reader.FetchMessage(subCtx)
			if err != nil {
				if errors.Is(err, context.Canceled) || b.closed.Load() {
					return
				}
				// Backoff slightly on transient fetch error
				select {
				case <-time.After(20 * time.Millisecond):
				case <-subCtx.Done():
					return
				case <-b.closeCh:
					return
				}
				continue
			}

			select {
			case workCh <- kmsg:
			case <-subCtx.Done():
				return
			case <-b.closeCh:
				return
			}
		}
	}()

	return sub, nil
}

// Close gracefully closes all active subscriptions and the message writer.
func (b *Bus) Close() error {
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}

	close(b.closeCh)

	b.mu.Lock()
	for _, sub := range b.subs {
		sub.cancel()
		if sub.reader != nil {
			_ = sub.reader.Close()
		}
	}
	b.mu.Unlock()

	b.wg.Wait()

	if b.writer != nil {
		return b.writer.Close()
	}

	return nil
}

// Subscription represents an active topic subscription on Kafka.
type Subscription struct {
	topic   string
	bus     *Bus
	reader  MessageReader
	cancel  context.CancelFunc
	done    chan struct{}
	autoAck bool
	closed  atomic.Bool
}

// Topic returns the subscribed Kafka topic.
func (s *Subscription) Topic() string {
	return s.topic
}

// Unsubscribe stops reading from Kafka and closes the reader.
func (s *Subscription) Unsubscribe() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	s.cancel()
	var err error
	if s.reader != nil {
		err = s.reader.Close()
	}
	<-s.done
	return err
}

// ToKafkaMessage converts an eventbus.Message into a segmentio kafkago.Message.
func ToKafkaMessage(msg *eventbus.Message) kafkago.Message {
	headers := make([]kafkago.Header, 0, len(msg.Headers)+3)
	headers = append(headers,
		kafkago.Header{Key: HeaderMessageID, Value: []byte(msg.ID)},
		kafkago.Header{Key: HeaderTopic, Value: []byte(msg.Topic)},
	)
	if msg.Key != "" {
		headers = append(headers, kafkago.Header{Key: HeaderPartitionKey, Value: []byte(msg.Key)})
	}

	for k, v := range msg.Headers {
		headers = append(headers, kafkago.Header{Key: k, Value: []byte(v)})
	}

	key := []byte(msg.Key)
	if len(key) == 0 {
		key = []byte(msg.ID)
	}

	return kafkago.Message{
		Topic:   msg.Topic,
		Key:     key,
		Value:   msg.Payload,
		Headers: headers,
		Time:    msg.Timestamp,
	}
}

// FromKafkaMessage converts a segmentio kafkago.Message into an eventbus.Message.
func FromKafkaMessage(kmsg kafkago.Message, fallbackTopic string) *eventbus.Message {
	msg := &eventbus.Message{
		Payload:   kmsg.Value,
		Timestamp: kmsg.Time,
		Headers:   make(map[string]string),
	}

	if kmsg.Topic != "" {
		msg.Topic = kmsg.Topic
	} else {
		msg.Topic = fallbackTopic
	}

	for _, h := range kmsg.Headers {
		key := h.Key
		val := string(h.Value)
		switch key {
		case HeaderMessageID:
			msg.ID = val
		case HeaderTopic:
			if msg.Topic == "" {
				msg.Topic = val
			}
		case HeaderPartitionKey:
			msg.Key = val
		default:
			msg.Headers[key] = val
		}
	}

	if msg.ID == "" {
		msg.ID = string(kmsg.Key)
		if msg.ID == "" {
			msg.ID = uuid.NewString()
		}
	}
	if msg.Key == "" && len(kmsg.Key) > 0 {
		msg.Key = string(kmsg.Key)
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}

	return msg
}
