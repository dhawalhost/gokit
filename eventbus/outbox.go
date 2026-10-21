package eventbus

import (
	"context"
	"time"

	"github.com/dhawalhost/gokit/outbox"
	"github.com/google/uuid"
)

// OutboxPublisherAdapter adapts an eventbus.Publisher into an outbox.Publisher.
type OutboxPublisherAdapter struct {
	publisher Publisher
}

// NewOutboxPublisher creates an outbox.Publisher that forwards messages dispatched by
// the transactional outbox relay directly into the EventBus.
func NewOutboxPublisher(publisher Publisher) outbox.Publisher {
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
