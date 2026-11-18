// Package ratelimit provides rate-limiting backends for use with middleware.
package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Result describes the outcome of a rate-limit evaluation.
type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
	ResetAfter time.Duration
}

// Store is the interface implemented by rate-limit backends.
type Store interface {
	// Allow reports whether the request identified by key should be allowed,
	// given the sustained rate (requests per second) and burst size.
	Allow(ctx context.Context, key string, rps float64, burst int) (bool, error)
}

// DetailedStore is an optional extension of Store supporting weighted costs and rich quota metadata.
type DetailedStore interface {
	Store
	AllowN(ctx context.Context, key string, cost int, rps float64, burst int) (Result, error)
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// InMemoryStore is a thread-safe in-memory rate-limit store backed by
// golang.org/x/time/rate token buckets keyed by request key.
// It includes TTL-based cleanup to prevent memory leaks.
type InMemoryStore struct {
	mu          sync.RWMutex
	limiters    map[string]*limiterEntry
	cleanupTTL  time.Duration
	stopCleanup chan struct{}
	startOnce   sync.Once
	stopOnce    sync.Once
	stopped     bool
}

// NewInMemoryStore creates a new InMemoryStore with default 1-hour TTL.
func NewInMemoryStore() *InMemoryStore {
	return NewInMemoryStoreWithTTL(1 * time.Hour)
}

// NewInMemoryStoreWithTTL creates a new InMemoryStore with custom TTL for cleanup.
func NewInMemoryStoreWithTTL(ttl time.Duration) *InMemoryStore {
	s := &InMemoryStore{
		limiters:    make(map[string]*limiterEntry),
		cleanupTTL:  ttl,
		stopCleanup: make(chan struct{}),
	}
	return s
}

// startCleanup starts the background cleanup goroutine at most once.
func (s *InMemoryStore) startCleanup() {
	s.startOnce.Do(func() {
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}
		interval := s.cleanupTTL / 2
		if interval <= 0 {
			interval = time.Millisecond
		}
		ticker := time.NewTicker(interval)
		stopCh := s.stopCleanup
		s.mu.Unlock()

		go func() {
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					s.cleanup()
				case <-stopCh:
					return
				}
			}
		}()
	})
}

// cleanup removes limiters that haven't been used within the TTL period.
func (s *InMemoryStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for key, entry := range s.limiters {
		if now.Sub(entry.lastSeen) > s.cleanupTTL {
			delete(s.limiters, key)
		}
	}
}

// Stop stops the cleanup goroutine and releases resources safely and idempotently.
func (s *InMemoryStore) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		close(s.stopCleanup)
		s.mu.Unlock()
	})
}

// Allow reports whether key should be allowed under the given rate and burst.
func (s *InMemoryStore) Allow(ctx context.Context, key string, rps float64, burst int) (bool, error) {
	res, err := s.AllowN(ctx, key, 1, rps, burst)
	return res.Allowed, err
}

// AllowN consumes cost tokens if available and returns full quota metadata.
func (s *InMemoryStore) AllowN(_ context.Context, key string, cost int, rps float64, burst int) (Result, error) {
	if burst <= 0 {
		return Result{Limit: 0, Remaining: 0}, nil
	}
	if cost < 1 {
		cost = 1
	}

	// Start cleanup on first use (safe under concurrent callers via sync.Once)
	s.startCleanup()

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.limiters[key]
	if !exists {
		entry = &limiterEntry{
			limiter:  rate.NewLimiter(rate.Limit(rps), burst),
			lastSeen: now,
		}
		s.limiters[key] = entry
	} else {
		entry.lastSeen = now
		// Update limiter parameters if they changed
		if entry.limiter.Limit() != rate.Limit(rps) || entry.limiter.Burst() != burst {
			entry.limiter = rate.NewLimiter(rate.Limit(rps), burst)
		}
	}

	reservation := entry.limiter.ReserveN(now, cost)
	if !reservation.OK() {
		rem := clampTokens(entry.limiter.TokensAt(now))
		resetAfter := resetDuration(rps, burst, entry.limiter.TokensAt(now))
		return Result{
			Allowed:    false,
			Limit:      burst,
			Remaining:  rem,
			RetryAfter: resetAfter,
			ResetAfter: resetAfter,
		}, nil
	}

	delay := reservation.DelayFrom(now)
	if delay > 0 {
		reservation.CancelAt(now)
		tokens := entry.limiter.TokensAt(now)
		return Result{
			Allowed:    false,
			Limit:      burst,
			Remaining:  clampTokens(tokens),
			RetryAfter: delay,
			ResetAfter: resetDuration(rps, burst, tokens),
		}, nil
	}

	tokens := entry.limiter.TokensAt(now)
	return Result{
		Allowed:    true,
		Limit:      burst,
		Remaining:  clampTokens(tokens),
		ResetAfter: resetDuration(rps, burst, tokens),
	}, nil
}

func clampTokens(tokens float64) int {
	if tokens <= 0 {
		return 0
	}
	return int(math.Floor(tokens))
}

func resetDuration(rps float64, burst int, tokens float64) time.Duration {
	if rps <= 0 {
		return 0
	}
	deficit := float64(burst) - tokens
	if deficit <= 0 {
		return 0
	}
	return time.Duration(deficit / rps * float64(time.Second))
}
