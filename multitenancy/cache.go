package multitenancy

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	mu         sync.Mutex
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

// ClearPrefix removes keys matching prefix inside this tenant namespace.
func (s *ScopedCache) ClearPrefix(ctx context.Context, prefix string) error {
	if prefix == "" {
		return errors.New("multitenancy: clear prefix must not be empty")
	}
	clearer, ok := s.underlying.(cache.PrefixClearer)
	if !ok {
		return errors.New("multitenancy: underlying cache does not implement ClearPrefix")
	}
	return clearer.ClearPrefix(ctx, s.key(prefix))
}

// ClearTenant removes all cache keys belonging to this tenant namespace.
func (s *ScopedCache) ClearTenant(ctx context.Context) error {
	clearer, ok := s.underlying.(cache.PrefixClearer)
	if !ok {
		return errors.New("multitenancy: underlying cache does not implement ClearPrefix")
	}
	return clearer.ClearPrefix(ctx, s.key(""))
}

// Flush deletes all keys in the underlying cache store.
//
// Deprecated: Flush wipes the entire underlying cache across all tenants; use ClearTenant or ClearPrefix instead.
func (s *ScopedCache) Flush(ctx context.Context) error {
	flusher, ok := s.underlying.(interface{ Flush(context.Context) error })
	if !ok {
		return errors.New("multitenancy: underlying cache does not implement Flush")
	}
	return flusher.Flush(ctx)
}

// SetNX atomically sets key under this tenant namespace if it does not already exist.
func (s *ScopedCache) SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	scopedKey := s.key(key)
	if locker, ok := s.underlying.(cache.AtomicLocker); ok {
		return locker.SetNX(ctx, scopedKey, value, ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exists, err := s.underlying.Exists(ctx, scopedKey)
	if err != nil || exists {
		return false, err
	}
	if err := s.underlying.Set(ctx, scopedKey, value, ttl); err != nil {
		return false, err
	}
	return true, nil
}

// CompareAndDelete atomically deletes key under this tenant namespace if its value matches expectedValue.
func (s *ScopedCache) CompareAndDelete(ctx context.Context, key string, expectedValue string) (bool, error) {
	scopedKey := s.key(key)
	if locker, ok := s.underlying.(cache.AtomicLocker); ok {
		return locker.CompareAndDelete(ctx, scopedKey, expectedValue)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	val, err := s.underlying.Get(ctx, scopedKey)
	if err != nil || val != expectedValue {
		return false, nil
	}
	if err := s.underlying.Delete(ctx, scopedKey); err != nil {
		return false, err
	}
	return true, nil
}

// ContextScopedCache dynamically derives tenant isolation from request context on each operation.
type ContextScopedCache struct {
	underlying cache.Cache
	mu         sync.Mutex
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

// ClearPrefix removes keys matching prefix inside the tenant namespace derived from ctx.
func (c *ContextScopedCache) ClearPrefix(ctx context.Context, prefix string) error {
	if prefix == "" {
		return errors.New("multitenancy: clear prefix must not be empty")
	}
	clearer, ok := c.underlying.(cache.PrefixClearer)
	if !ok {
		return errors.New("multitenancy: underlying cache does not implement ClearPrefix")
	}
	return clearer.ClearPrefix(ctx, ScopedKeyFromContext(ctx, prefix))
}

// Flush deletes all keys in the underlying cache.
//
// Deprecated: Flush wipes the entire underlying cache across all tenants; use ClearPrefix instead.
func (c *ContextScopedCache) Flush(ctx context.Context) error {
	flusher, ok := c.underlying.(interface{ Flush(context.Context) error })
	if !ok {
		return errors.New("multitenancy: underlying cache does not implement Flush")
	}
	return flusher.Flush(ctx)
}

// SetNX atomically sets key scoped by the tenant in ctx if it does not already exist.
func (c *ContextScopedCache) SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	scopedKey := ScopedKeyFromContext(ctx, key)
	if locker, ok := c.underlying.(cache.AtomicLocker); ok {
		return locker.SetNX(ctx, scopedKey, value, ttl)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	exists, err := c.underlying.Exists(ctx, scopedKey)
	if err != nil || exists {
		return false, err
	}
	if err := c.underlying.Set(ctx, scopedKey, value, ttl); err != nil {
		return false, err
	}
	return true, nil
}

// CompareAndDelete atomically deletes key scoped by the tenant in ctx if its value matches expectedValue.
func (c *ContextScopedCache) CompareAndDelete(ctx context.Context, key string, expectedValue string) (bool, error) {
	scopedKey := ScopedKeyFromContext(ctx, key)
	if locker, ok := c.underlying.(cache.AtomicLocker); ok {
		return locker.CompareAndDelete(ctx, scopedKey, expectedValue)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	val, err := c.underlying.Get(ctx, scopedKey)
	if err != nil || val != expectedValue {
		return false, nil
	}
	if err := c.underlying.Delete(ctx, scopedKey); err != nil {
		return false, err
	}
	return true, nil
}
