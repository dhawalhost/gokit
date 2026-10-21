package outbox

import (
	"context"
)

// Publisher defines the contract for sending messages to a message broker (e.g. Kafka, RabbitMQ, SQS, Redis Stream).
type Publisher interface {
	Publish(ctx context.Context, topic, key string, payload []byte, headers map[string]string) error
}

// PublisherFunc is an adapter allowing the use of ordinary functions as Publishers.
type PublisherFunc func(ctx context.Context, topic, key string, payload []byte, headers map[string]string) error

// Publish calls f(ctx, topic, key, payload, headers).
func (f PublisherFunc) Publish(ctx context.Context, topic, key string, payload []byte, headers map[string]string) error {
	return f(ctx, topic, key, payload, headers)
}
