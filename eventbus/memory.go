package eventbus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryBusConfig configures an in-memory event bus.
type MemoryBusConfig struct {
	// BufferSize is the channel buffer size per subscription (default: 100).
	BufferSize int
}

type memorySub struct {
	id      uint64
	topic   string
	handler HandlerFunc
	ch      chan *Message
	bus     *MemoryBus
	closed  atomic.Bool
}

func (s *memorySub) Topic() string {
	return s.topic
}

func (s *memorySub) Unsubscribe() error {
	if s.closed.CompareAndSwap(false, true) {
		s.bus.removeSub(s)
		close(s.ch)
	}
	return nil
}

// MemoryBus provides a high-performance in-memory EventBus implementation.
type MemoryBus struct {
	cfg   MemoryBusConfig
	mu    sync.RWMutex
	subs  map[string][]*memorySub
	subID uint64

	wg      sync.WaitGroup
	closeCh chan struct{}
	closed  atomic.Bool
}

// NewMemoryBus creates a new in-memory EventBus.
func NewMemoryBus(cfg ...MemoryBusConfig) *MemoryBus {
	var c MemoryBusConfig
	if len(cfg) > 0 {
		c = cfg[0]
	}
	if c.BufferSize <= 0 {
		c.BufferSize = 100
	}

	return &MemoryBus{
		cfg:     c,
		subs:    make(map[string][]*memorySub),
		closeCh: make(chan struct{}),
	}
}

// Publish dispatches a message to all subscribers matching the message topic.
func (b *MemoryBus) Publish(ctx context.Context, msg *Message) error {
	if b.closed.Load() {
		return fmt.Errorf("eventbus: bus is closed")
	}
	if msg == nil {
		return fmt.Errorf("eventbus: nil message")
	}

	// Stamp timestamp if missing
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}

	b.mu.RLock()
	var targets []*memorySub
	for pattern, subList := range b.subs {
		if MatchTopic(pattern, msg.Topic) {
			targets = append(targets, subList...)
		}
	}
	b.mu.RUnlock()

	for _, sub := range targets {
		if sub.closed.Load() {
			continue
		}
		select {
		case <-b.closeCh:
			return fmt.Errorf("eventbus: bus closed during publish")
		case <-ctx.Done():
			return ctx.Err()
		case sub.ch <- msg:
		default:
			// If buffer is full, spawn worker so it does not block the publisher
			b.wg.Add(1)
			go func(s *memorySub, m *Message) {
				defer b.wg.Done()
				select {
				case <-b.closeCh:
				case <-ctx.Done():
				case s.ch <- m:
				}
			}(sub, msg)
		}
	}

	return nil
}

// Subscribe registers a handler for events matching the given topic pattern.
func (b *MemoryBus) Subscribe(ctx context.Context, topic string, handler HandlerFunc, opts ...SubscribeOption) (Subscription, error) {
	if b.closed.Load() {
		return nil, fmt.Errorf("eventbus: bus is closed")
	}
	if handler == nil {
		return nil, fmt.Errorf("eventbus: nil handler")
	}

	var options SubscribeOptions
	for _, opt := range opts {
		opt(&options)
	}

	workers := 1
	if options.Concurrency > 1 {
		workers = options.Concurrency
	}

	sub := &memorySub{
		id:      atomic.AddUint64(&b.subID, 1),
		topic:   topic,
		handler: handler,
		ch:      make(chan *Message, b.cfg.BufferSize),
		bus:     b,
	}

	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], sub)
	b.mu.Unlock()

	// Launch worker pool for this subscription
	for i := 0; i < workers; i++ {
		b.wg.Add(1)
		go b.runWorker(ctx, sub)
	}

	return sub, nil
}

func (b *MemoryBus) runWorker(ctx context.Context, sub *memorySub) {
	defer b.wg.Done()
	for {
		select {
		case <-b.closeCh:
			return
		case <-ctx.Done():
			return
		case msg, ok := <-sub.ch:
			if !ok {
				return
			}
			// Execute handler with subscriber context
			_ = sub.handler(ctx, msg)
		}
	}
}

func (b *MemoryBus) removeSub(target *memorySub) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subList := b.subs[target.topic]
	for i, s := range subList {
		if s.id == target.id {
			b.subs[target.topic] = append(subList[:i], subList[i+1:]...)
			break
		}
	}
	if len(b.subs[target.topic]) == 0 {
		delete(b.subs, target.topic)
	}
}

// Close gracefully stops the memory bus and waits for in-flight messages.
func (b *MemoryBus) Close() error {
	if b.closed.CompareAndSwap(false, true) {
		close(b.closeCh)

		b.mu.Lock()
		for _, subList := range b.subs {
			for _, sub := range subList {
				if sub.closed.CompareAndSwap(false, true) {
					close(sub.ch)
				}
			}
		}
		b.subs = make(map[string][]*memorySub)
		b.mu.Unlock()

		b.wg.Wait()
	}
	return nil
}

// MatchTopic checks whether a topic matches a subscribed pattern.
// Supported patterns:
//   - "*" matches all topics or single token if separated by dot
//   - "orders.*" matches "orders.created", "orders.updated"
//   - "orders.#" or "orders.>" matches "orders.created", "orders.v1.created"
func MatchTopic(pattern, topic string) bool {
	if pattern == topic || pattern == "#" || pattern == ">" {
		return true
	}

	patternParts := strings.Split(pattern, ".")
	topicParts := strings.Split(topic, ".")

	return matchParts(patternParts, topicParts)
}

func matchParts(pattern, topic []string) bool {
	if len(pattern) == 0 {
		return len(topic) == 0
	}

	pHead := pattern[0]
	if pHead == "#" || pHead == ">" {
		// Wildcard matches 0 or more remaining segments
		return true
	}

	if len(topic) == 0 {
		return false
	}

	tHead := topic[0]
	if pHead == "*" || pHead == tHead {
		return matchParts(pattern[1:], topic[1:])
	}

	return false
}
