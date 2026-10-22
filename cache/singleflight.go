package cache

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"time"

	"golang.org/x/sync/singleflight"
)

var (
	// ErrNotFound indicates that the requested resource does not exist in the source of truth.
	ErrNotFound = errors.New("cache: resource not found")

	// ErrInsufficientQuantity is returned when an atomic decrement would breach the allowed floor.
	ErrInsufficientQuantity = errors.New("cache: insufficient quantity (floor limit reached)")
)

// NegativeCacheSentinel is stored in the cache tier when a key is not found.
const NegativeCacheSentinel = "__GOKIT_NOT_FOUND__"

// FetcherFunc defines a callback function that queries the source of truth (e.g. database)
// when a cache miss occurs.
type FetcherFunc func(ctx context.Context) (string, error)

// JitterTTL adds a random jitter (0 to maxFraction, e.g. 0.10 for 10%) to a base TTL
// to prevent synchronized mass expiration (thundering herds at the top of the minute).
func JitterTTL(base time.Duration, maxFraction float64) time.Duration {
	if maxFraction <= 0 {
		maxFraction = 0.10
	}
	jitter := float64(base) * (rand.Float64() * maxFraction) //nolint:gosec
	return base + time.Duration(jitter)
}

// Coalescer prevents cache stampedes (thundering herd problem) by collapsing
// concurrent duplicate requests for the same key into a single execution.
type Coalescer struct {
	group singleflight.Group
}

// NewCoalescer constructs a new Coalescer for request deduplication.
func NewCoalescer() *Coalescer {
	return &Coalescer{}
}

// Do executes the fetcher function only once for concurrent requests with the same key.
func (c *Coalescer) Do(ctx context.Context, store Cache, key string, ttl time.Duration, fetch FetcherFunc) (string, error) {
	// 1. Fast path: check cache first
	val, err := store.Get(ctx, key)
	if err == nil {
		if val == NegativeCacheSentinel {
			return "", ErrNotFound
		}
		return val, nil
	}

	// 2. Coalesce concurrent fetches
	result, err, _ := c.group.Do(key, func() (any, error) {
		// Double-check cache in case an earlier coalesced execution completed
		if v, checkErr := store.Get(ctx, key); checkErr == nil {
			if v == NegativeCacheSentinel {
				return "", ErrNotFound
			}
			return v, nil
		}

		// Fetch from source of truth
		freshVal, fetchErr := fetch(ctx)
		if fetchErr != nil {
			if errors.Is(fetchErr, ErrNotFound) {
				// Negative cache to protect DB against scraping / missing ID attacks
				_ = store.Set(ctx, key, NegativeCacheSentinel, 30*time.Second)
				return "", ErrNotFound
			}
			return "", fetchErr
		}

		_ = store.Set(ctx, key, freshVal, ttl)
		return freshVal, nil
	})

	if err != nil {
		return "", err
	}

	return result.(string), nil
}

type swrEnvelope struct {
	Value        string `json:"v"`
	SoftExpireAt int64  `json:"s"`
}

// DoStaleWhileRevalidate implements the Stale-While-Revalidate pattern .
// If the key is within softTTL, it returns immediately.
// If between softTTL and hardTTL, it immediately returns the stale value to the client
// with 0 added latency while asynchronously refreshing the cache in the background.
func (c *Coalescer) DoStaleWhileRevalidate(ctx context.Context, store Cache, key string, softTTL, hardTTL time.Duration, fetch FetcherFunc) (string, error) {
	raw, err := store.Get(ctx, key)
	now := time.Now().UnixNano()

	if err == nil {
		var env swrEnvelope
		if jsonErr := json.Unmarshal([]byte(raw), &env); jsonErr == nil {
			// Fresh hit: within soft TTL
			if now < env.SoftExpireAt {
				return env.Value, nil
			}

			go func() { //nolint:gosec // intentional detached background cache revalidation
				bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				_, _, _ = c.group.Do(key, func() (any, error) {
					newVal, fErr := fetch(bgCtx)
					if fErr == nil {
						newEnv := swrEnvelope{
							Value:        newVal,
							SoftExpireAt: time.Now().Add(softTTL).UnixNano(),
						}
						data, _ := json.Marshal(newEnv)
						_ = store.Set(bgCtx, key, string(data), hardTTL)
					}
					return nil, nil
				})
			}()

			return env.Value, nil
		}
		// If unmarshal fails, treat as raw value
		return raw, nil
	}

	// Complete cache miss: synchronous coalesced fetch
	result, err, _ := c.group.Do(key, func() (any, error) {
		freshVal, fErr := fetch(ctx)
		if fErr != nil {
			return "", fErr
		}

		env := swrEnvelope{
			Value:        freshVal,
			SoftExpireAt: time.Now().Add(softTTL).UnixNano(),
		}
		data, _ := json.Marshal(env)
		_ = store.Set(ctx, key, string(data), hardTTL)
		return freshVal, nil
	})

	if err != nil {
		return "", err
	}
	return result.(string), nil
}
