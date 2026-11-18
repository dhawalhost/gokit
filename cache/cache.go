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
}

// PrefixClearer is an optional interface implemented by Cache backends that support
// deleting keys matching a prefix without affecting unrelated keys in the store.
type PrefixClearer interface {
	// ClearPrefix removes all keys matching prefix (e.g. "cache:users:").
	ClearPrefix(ctx context.Context, prefix string) error
}

// AtomicLocker is an optional interface implemented by Cache backends that support
// atomic compare-and-set locking primitives.
type AtomicLocker interface {
	// SetNX stores value under key with ttl only if key does not already exist.
	SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error)
	// CompareAndDelete deletes key only if its current stored value equals expectedValue.
	CompareAndDelete(ctx context.Context, key string, expectedValue string) (bool, error)
}
