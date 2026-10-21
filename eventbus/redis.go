package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// RedisBusConfig configures an event bus backed by Redis Streams.
type RedisBusConfig struct {
	// Client is the Redis UniversalClient (standalone, sentinel, or cluster).
	Client redis.UniversalClient
	// StreamPrefix is prepended to the topic name to form the Redis stream key (default: "events:").
	StreamPrefix string
	// DefaultGroup is the default consumer group name when not specified in SubscribeOption (default: "gokit-workers").
	DefaultGroup string
	// MaxLen caps the maximum length of the Redis stream via approximate trimming (default: 10000, 0 to disable).
	MaxLen int64
	// BlockTimeout is the blocking poll duration for XREADGROUP (default: 2s).
	BlockTimeout time.Duration
	// BatchSize is the max messages retrieved per XREADGROUP call (default: 10).
	BatchSize int64
}

type redisSub struct {
	topic  string
	bus    *RedisBus
	cancel context.CancelFunc
	closed atomic.Bool
}

func (s *redisSub) Topic() string {
	return s.topic
}

func (s *redisSub) Unsubscribe() error {
	if s.closed.CompareAndSwap(false, true) {
		s.cancel()
	}
	return nil
}

// RedisBus provides an EventBus backed by Redis Streams and Consumer Groups.
type RedisBus struct {
	cfg     RedisBusConfig
	client  redis.UniversalClient
	mu      sync.Mutex
	subs    []*redisSub
	wg      sync.WaitGroup
	closeCh chan struct{}
	closed  atomic.Bool
}

// NewRedisBus constructs a new Redis Streams EventBus.
func NewRedisBus(cfg RedisBusConfig) (*RedisBus, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("eventbus: redis client is required")
	}
	if cfg.StreamPrefix == "" {
		cfg.StreamPrefix = "events:"
	}
	if cfg.DefaultGroup == "" {
		cfg.DefaultGroup = "gokit-workers"
	}
	if cfg.BlockTimeout <= 0 {
		cfg.BlockTimeout = 2 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
	}
	if cfg.MaxLen <= 0 {
		cfg.MaxLen = 10000
	}

	return &RedisBus{
		cfg:     cfg,
		client:  cfg.Client,
		closeCh: make(chan struct{}),
	}, nil
}

// streamKey returns the fully qualified Redis stream key for a topic.
func (b *RedisBus) streamKey(topic string) string {
	return b.cfg.StreamPrefix + topic
}

// Publish writes an event to the Redis Stream corresponding to the message topic.
func (b *RedisBus) Publish(ctx context.Context, msg *Message) error {
	if b.closed.Load() {
		return fmt.Errorf("eventbus: bus is closed")
	}
	if msg == nil {
		return fmt.Errorf("eventbus: nil message")
	}

	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}

	headersJSON, err := json.Marshal(msg.Headers)
	if err != nil {
		return fmt.Errorf("eventbus: marshal headers: %w", err)
	}

	values := map[string]any{
		"id":        msg.ID,
		"topic":     msg.Topic,
		"key":       msg.Key,
		"payload":   string(msg.Payload),
		"headers":   string(headersJSON),
		"timestamp": msg.Timestamp.Format(time.RFC3339Nano),
	}

	streamKey := b.streamKey(msg.Topic)

	xaddArgs := &redis.XAddArgs{
		Stream: streamKey,
		ID:     "*",
		Values: values,
	}
	if b.cfg.MaxLen > 0 {
		xaddArgs.MaxLen = b.cfg.MaxLen
		xaddArgs.Approx = true
	}

	return b.client.XAdd(ctx, xaddArgs).Err()
}

// Subscribe listens to a topic's Redis Stream using a Consumer Group.
func (b *RedisBus) Subscribe(ctx context.Context, topic string, handler HandlerFunc, opts ...SubscribeOption) (Subscription, error) {
	if b.closed.Load() {
		return nil, fmt.Errorf("eventbus: bus is closed")
	}
	if handler == nil {
		return nil, fmt.Errorf("eventbus: nil handler")
	}

	options := SubscribeOptions{
		ConsumerGroup: b.cfg.DefaultGroup,
		ConsumerName:  uuid.NewString(),
		AutoAck:       true,
		Concurrency:   1,
	}
	for _, opt := range opts {
		opt(&options)
	}

	streamKey := b.streamKey(topic)

	// Ensure the consumer group exists (MKSTREAM creates stream if it doesn't exist yet)
	err := b.client.XGroupCreateMkStream(ctx, streamKey, options.ConsumerGroup, "$").Err()
	if err != nil && !isBusyGroupErr(err) {
		return nil, fmt.Errorf("eventbus: create consumer group: %w", err)
	}

	subCtx, cancel := context.WithCancel(ctx)
	sub := &redisSub{
		topic:  topic,
		bus:    b,
		cancel: cancel,
	}

	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()

	workers := options.Concurrency
	if workers <= 0 {
		workers = 1
	}

	for i := 0; i < workers; i++ {
		workerName := fmt.Sprintf("%s-%d", options.ConsumerName, i)
		b.wg.Add(1)
		go b.pollStream(subCtx, streamKey, topic, options.ConsumerGroup, workerName, options.AutoAck, handler)
	}

	return sub, nil
}

func (b *RedisBus) pollStream(ctx context.Context, streamKey, topic, group, consumer string, autoAck bool, handler HandlerFunc) {
	defer b.wg.Done()

	for {
		select {
		case <-b.closeCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		entries, err := b.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{streamKey, ">"},
			Count:    b.cfg.BatchSize,
			Block:    b.cfg.BlockTimeout,
		}).Result()

		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, redis.Nil) {
				continue
			}
			select {
			case <-b.closeCh:
				return
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}

		for _, stream := range entries {
			for _, xmsg := range stream.Messages {
				msg := parseStreamMessage(topic, xmsg)

				err := handler(ctx, msg)
				if err == nil && autoAck {
					_ = b.client.XAck(ctx, streamKey, group, xmsg.ID).Err()
				}
			}
		}
	}
}

func parseStreamMessage(topic string, xmsg redis.XMessage) *Message {
	msg := &Message{
		ID:        xmsg.ID,
		Topic:     topic,
		Timestamp: time.Now().UTC(),
	}

	if idVal, ok := xmsg.Values["id"].(string); ok && idVal != "" {
		msg.ID = idVal
	}
	if topicVal, ok := xmsg.Values["topic"].(string); ok && topicVal != "" {
		msg.Topic = topicVal
	}
	if keyVal, ok := xmsg.Values["key"].(string); ok {
		msg.Key = keyVal
	}
	if payloadVal, ok := xmsg.Values["payload"].(string); ok {
		msg.Payload = []byte(payloadVal)
	}
	if tsVal, ok := xmsg.Values["timestamp"].(string); ok {
		if parsedTs, err := time.Parse(time.RFC3339Nano, tsVal); err == nil {
			msg.Timestamp = parsedTs
		}
	}
	if headersVal, ok := xmsg.Values["headers"].(string); ok && headersVal != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(headersVal), &headers); err == nil {
			msg.Headers = headers
		}
	}

	return msg
}

func isBusyGroupErr(err error) bool {
	return strings.Contains(err.Error(), "BUSYGROUP")
}

// Close gracefully terminates all active stream subscriber pollers.
func (b *RedisBus) Close() error {
	if b.closed.CompareAndSwap(false, true) {
		close(b.closeCh)

		b.mu.Lock()
		for _, s := range b.subs {
			_ = s.Unsubscribe()
		}
		b.subs = nil
		b.mu.Unlock()

		b.wg.Wait()
	}
	return nil
}
