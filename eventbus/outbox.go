package eventbus

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// OutboxPublisherAdapter adapts an eventbus.Publisher into an outbox-compatible Publisher.
type OutboxPublisherAdapter struct {
	publisher Publisher
}

// NewOutboxPublisher creates an adapter that forwards messages dispatched by
// the transactional outbox relay directly into the EventBus.
func NewOutboxPublisher(publisher Publisher) *OutboxPublisherAdapter {
	return &OutboxPublisherAdapter{publisher: publisher}
}

// Publish forwards the outbox message to the underlying eventbus Publisher.
func (a *OutboxPublisherAdapter) Publish(ctx context.Context, topic, key string, payload []byte, headers map[string]string) error {
	msg := &Message{
		ID:        uuid.NewString(),
		Topic:     topic,
		Key:       key,
		Payload:   payload,
		Headers:   headers,
		Timestamp: time.Now().UTC(),
	}
	return a.publisher.Publish(ctx, msg)
}
