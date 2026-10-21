package eventbus

import (
	"context"
	"io"
)

// HandlerFunc defines the function signature for consuming an EventBus Message.
type HandlerFunc func(ctx context.Context, msg *Message) error

// Publisher defines the contract for sending events to an EventBus.
type Publisher interface {
	Publish(ctx context.Context, msg *Message) error
}

// Subscription represents an active subscription to a topic.
type Subscription interface {
	// Topic returns the subscribed topic pattern.
	Topic() string
	// Unsubscribe stops receiving events for this subscription.
	Unsubscribe() error
}

// SubscribeOptions contains optional configuration for a subscription.
type SubscribeOptions struct {
	// ConsumerGroup defines the consumer group name for load-balanced delivery (if supported by broker).
	ConsumerGroup string
	// ConsumerName identifies this specific worker within the consumer group.
	ConsumerName string
	// AutoAck specifies whether messages are automatically acknowledged upon handler return without error.
	AutoAck bool
	// Concurrency is the number of concurrent worker goroutines processing messages (default: 1).
	Concurrency int
}

// SubscribeOption is a functional option for configuring a subscription.
type SubscribeOption func(*SubscribeOptions)

// WithConsumerGroup sets the consumer group name.
func WithConsumerGroup(group string) SubscribeOption {
	return func(o *SubscribeOptions) {
		o.ConsumerGroup = group
	}
}

// WithConsumerName sets the consumer name within the consumer group.
func WithConsumerName(name string) SubscribeOption {
	return func(o *SubscribeOptions) {
		o.ConsumerName = name
	}
}

// WithAutoAck configures whether successful handler execution automatically ACKs the message.
func WithAutoAck(autoAck bool) SubscribeOption {
	return func(o *SubscribeOptions) {
		o.AutoAck = autoAck
	}
}

// WithConcurrency sets the number of concurrent consumer workers for the subscription.
func WithConcurrency(workers int) SubscribeOption {
	return func(o *SubscribeOptions) {
		if workers > 0 {
			o.Concurrency = workers
		}
	}
}

// Subscriber defines the contract for subscribing to events on an EventBus.
type Subscriber interface {
	Subscribe(ctx context.Context, topic string, handler HandlerFunc, opts ...SubscribeOption) (Subscription, error)
}

// Bus unites Publisher, Subscriber, and io.Closer for an all-in-one EventBus abstraction.
type Bus interface {
	Publisher
	Subscriber
	io.Closer
}
