package multitenancy

import (
	"context"
	"fmt"
	"time"

	"github.com/dhawalhost/gokit/cache"
)

// ScopedKey prepends a tenant prefix to a raw cache or locking key.
func ScopedKey(tenantID, rawKey string) string {
	if tenantID == "" {
		tenantID = "default"
	}
	return fmt.Sprintf("tenant:%s:%s", tenantID, rawKey)
}

// ScopedKeyFromContext extracts the tenant from ctx and constructs the scoped key.
func ScopedKeyFromContext(ctx context.Context, rawKey string) string {
	tid, _ := FromContext(ctx)
	return ScopedKey(tid, rawKey)
}

// ScopedCache wraps a cache.Cache instance to enforce key-level tenant isolation.
type ScopedCache struct {
	underlying cache.Cache
	tenantID   string
}

// NewScopedCache creates a cache view bound to a specific tenant ID.
func NewScopedCache(c cache.Cache, tenantID string) *ScopedCache {
	return &ScopedCache{
		underlying: c,
		tenantID:   tenantID,
	}
}

// key returns the tenant-prefixed key.
func (s *ScopedCache) key(k string) string {
	return ScopedKey(s.tenantID, k)
}

// Get retrieves the value for key under this tenant namespace.
func (s *ScopedCache) Get(ctx context.Context, key string) (string, error) {
	return s.underlying.Get(ctx, s.key(key))
}

// Set stores the value under this tenant namespace.
func (s *ScopedCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	return s.underlying.Set(ctx, s.key(key), value, ttl)
}

// Delete removes the keys within this tenant namespace.
func (s *ScopedCache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	scoped := make([]string, len(keys))
	for i, k := range keys {
		scoped[i] = s.key(k)
	}
	return s.underlying.Delete(ctx, scoped...)
}

// Exists reports whether the key exists within this tenant namespace.
func (s *ScopedCache) Exists(ctx context.Context, key string) (bool, error) {
	return s.underlying.Exists(ctx, s.key(key))
}

// Flush deletes all keys in the underlying cache store.
func (s *ScopedCache) Flush(ctx context.Context) error {
	return s.underlying.Flush(ctx)
}

// ContextScopedCache dynamically derives tenant isolation from request context on each operation.
type ContextScopedCache struct {
	underlying cache.Cache
}

// NewContextScopedCache constructs a ContextScopedCache.
func NewContextScopedCache(c cache.Cache) *ContextScopedCache {
	return &ContextScopedCache{underlying: c}
}

// Get retrieves the value for key scoped by the tenant in ctx.
func (c *ContextScopedCache) Get(ctx context.Context, key string) (string, error) {
	return c.underlying.Get(ctx, ScopedKeyFromContext(ctx, key))
}

// Set stores the value scoped by the tenant in ctx.
func (c *ContextScopedCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	return c.underlying.Set(ctx, ScopedKeyFromContext(ctx, key), value, ttl)
}

// Delete removes the keys scoped by the tenant in ctx.
func (c *ContextScopedCache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	scoped := make([]string, len(keys))
	for i, k := range keys {
		scoped[i] = ScopedKeyFromContext(ctx, k)
	}
	return c.underlying.Delete(ctx, scoped...)
}

// Exists reports whether the key exists scoped by the tenant in ctx.
func (c *ContextScopedCache) Exists(ctx context.Context, key string) (bool, error) {
	return c.underlying.Exists(ctx, ScopedKeyFromContext(ctx, key))
}

// Flush deletes all keys in the underlying cache.
func (c *ContextScopedCache) Flush(ctx context.Context) error {
	return c.underlying.Flush(ctx)
}
