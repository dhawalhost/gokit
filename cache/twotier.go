package cache

import (
	"context"
	"sync"
	"time"
)

type l1Entry struct {
	value     string
	expiresAt time.Time
}

// TwoTierConfig configures the two-tier caching strategy.
type TwoTierConfig struct {
	// L1TTL is the maximum TTL for entries stored in the local L1 memory cache.
	// Defaults to 5 seconds to prevent stale drift across distributed instances.
	L1TTL time.Duration

	// L1MaxEntries limits the number of items stored in memory to prevent OOM.
	// Defaults to 10,000 entries.
	L1MaxEntries int
}

// TwoTierCache combines an in-process in-memory L1 cache (sub-microsecond access)
// with a shared distributed L2 cache (e.g. Redis) to absorb hot key spikes.
type TwoTierCache struct {
	l2        Cache
	cfg       TwoTierConfig
	mu        sync.RWMutex
	l1        map[string]l1Entry
	coalescer *Coalescer
}

// NewTwoTier creates a two-tier L1 (in-memory) + L2 (distributed) Cache.
func NewTwoTier(l2 Cache, cfg TwoTierConfig) *TwoTierCache {
	if cfg.L1TTL <= 0 {
		cfg.L1TTL = 5 * time.Second
	}
	if cfg.L1MaxEntries <= 0 {
		cfg.L1MaxEntries = 10000
	}

	return &TwoTierCache{
		l2:        l2,
		cfg:       cfg,
		l1:        make(map[string]l1Entry),
		coalescer: NewCoalescer(),
	}
}

// Get retrieves the value for key, checking L1 first, falling back to L2.
func (t *TwoTierCache) Get(ctx context.Context, key string) (string, error) {
	// 1. Check L1 in-memory cache
	t.mu.RLock()
	entry, found := t.l1[key]
	t.mu.RUnlock()

	if found && time.Now().Before(entry.expiresAt) {
		return entry.value, nil
	}

	// 2. Fall back to L2 distributed cache
	val, err := t.l2.Get(ctx, key)
	if err != nil {
		return "", err
	}

	// 3. Backfill L1 with bounded TTL
	t.setL1(key, val, t.cfg.L1TTL)
	return val, nil
}

// Set stores the key-value pair in both L1 and L2.
func (t *TwoTierCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	// Write to L2
	if err := t.l2.Set(ctx, key, value, ttl); err != nil {
		return err
	}

	// Write to L1 with min(ttl, L1TTL)
	l1TTL := t.cfg.L1TTL
	if ttl > 0 && ttl < l1TTL {
		l1TTL = ttl
	}

	valStr := serializeValue(value)
	t.setL1(key, valStr, l1TTL)
	return nil
}

// Delete removes the key from both L1 and L2.
func (t *TwoTierCache) Delete(ctx context.Context, keys ...string) error {
	t.mu.Lock()
	for _, k := range keys {
		delete(t.l1, k)
	}
	t.mu.Unlock()

	return t.l2.Delete(ctx, keys...)
}

// Exists checks if key exists in L1 or L2.
func (t *TwoTierCache) Exists(ctx context.Context, key string) (bool, error) {
	t.mu.RLock()
	entry, found := t.l1[key]
	t.mu.RUnlock()

	if found && time.Now().Before(entry.expiresAt) {
		return true, nil
	}

	return t.l2.Exists(ctx, key)
}

// Flush clears all keys from both L1 and L2.
func (t *TwoTierCache) Flush(ctx context.Context) error {
	t.mu.Lock()
	t.l1 = make(map[string]l1Entry)
	t.mu.Unlock()

	return t.l2.Flush(ctx)
}

// InvalidateL1 deletes a key from local L1 memory (e.g. upon receiving a pub/sub event).
func (t *TwoTierCache) InvalidateL1(key string) {
	t.mu.Lock()
	delete(t.l1, key)
	t.mu.Unlock()
}

// GetOrSet retrieves a value or invokes fetcher with singleflight stampede prevention across L1+L2.
func (t *TwoTierCache) GetOrSet(ctx context.Context, key string, ttl time.Duration, fetch FetcherFunc) (string, error) {
	return t.coalescer.Do(ctx, t, key, ttl, fetch)
}

func (t *TwoTierCache) setL1(key, val string, ttl time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Simple eviction if capacity reached
	if len(t.l1) >= t.cfg.L1MaxEntries {
		now := time.Now()
		// Evict expired entries first
		for k, e := range t.l1 {
			if now.After(e.expiresAt) {
				delete(t.l1, k)
			}
		}
		// If still at capacity, clear 10% randomly
		if len(t.l1) >= t.cfg.L1MaxEntries {
			count := 0
			limit := t.cfg.L1MaxEntries / 10
			for k := range t.l1 {
				delete(t.l1, k)
				count++
				if count >= limit {
					break
				}
			}
		}
	}

	t.l1[key] = l1Entry{
		value:     val,
		expiresAt: time.Now().Add(ttl),
	}
}

func serializeValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return ""
}
