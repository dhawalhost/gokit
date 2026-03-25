// Package ratelimit provides rate-limiting backends for use with middleware.
package ratelimit

import (
	"context"
	"sync"

	"golang.org/x/time/rate"
)

// Store is the interface implemented by rate-limit backends.
type Store interface {
	// Allow reports whether the request identified by key should be allowed,
	// given the sustained rate (requests per second) and burst size.
	Allow(ctx context.Context, key string, rps float64, burst int) (bool, error)
}

// InMemoryStore is a thread-safe in-memory rate-limit store backed by
// golang.org/x/time/rate token buckets keyed by request key.
type InMemoryStore struct {
	mu      sync.Mutex
	limiters sync.Map // map[string]*rate.Limiter
}

// NewInMemoryStore creates a new InMemoryStore.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{}
}

// Allow reports whether key should be allowed under the given rate and burst.
func (s *InMemoryStore) Allow(_ context.Context, key string, rps float64, burst int) (bool, error) {
	val, _ := s.limiters.LoadOrStore(key, rate.NewLimiter(rate.Limit(rps), burst))
	limiter := val.(*rate.Limiter)

	// Update the limiter if parameters changed.
	s.mu.Lock()
	if limiter.Limit() != rate.Limit(rps) || limiter.Burst() != burst {
		limiter = rate.NewLimiter(rate.Limit(rps), burst)
		s.limiters.Store(key, limiter)
	}
	s.mu.Unlock()

	return limiter.Allow(), nil
}
