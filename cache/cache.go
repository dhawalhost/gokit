// Package cache defines the Cache interface for key-value caching.
package cache

import (
	"context"
	"time"
)

// Cache is the common interface for caching backends.
type Cache interface {
	// Get retrieves the string value for key.
	Get(ctx context.Context, key string) (string, error)
	// Set stores value under key with the given TTL.
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	// Delete removes the given keys.
	Delete(ctx context.Context, keys ...string) error
	// Exists reports whether key exists.
	Exists(ctx context.Context, key string) (bool, error)
	// Flush deletes all keys in the store.
	Flush(ctx context.Context) error
}
