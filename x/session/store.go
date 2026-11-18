package session

import (
	"context"
	"time"
)

// Store defines the persistent storage interface for session records.
type Store interface {
	// Load retrieves the session data associated with id.
	// Returns ErrSessionNotFound if the session does not exist or has expired.
	Load(ctx context.Context, id string) (*Data, error)

	// Save writes or updates the session data under id with the given time-to-live duration.
	Save(ctx context.Context, id string, data *Data, ttl time.Duration) error

	// Destroy removes the session data associated with id.
	Destroy(ctx context.Context, id string) error

	// Touch extends the expiration time of the session without modifying its contents.
	Touch(ctx context.Context, id string, ttl time.Duration) error
}
