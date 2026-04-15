// Package ratelimit provides rate-limiting backends for use with middleware.
package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Store is the interface implemented by rate-limit backends.
type Store interface {
	// Allow reports whether the request identified by key should be allowed,
	// given the sustained rate (requests per second) and burst size.
	Allow(ctx context.Context, key string, rps float64, burst int) (bool, error)
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// InMemoryStore is a thread-safe in-memory rate-limit store backed by
// golang.org/x/time/rate token buckets keyed by request key.
// It includes TTL-based cleanup to prevent memory leaks.
type InMemoryStore struct {
	mu             sync.RWMutex
	limiters       map[string]*limiterEntry
	cleanupTTL     time.Duration
	cleanupTicker  *time.Ticker
	stopCleanup    chan struct{}
	cleanupStarted bool
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

// startCleanup starts the background cleanup goroutine if not already started.
func (s *InMemoryStore) startCleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cleanupStarted {
		return
	}

	s.cleanupTicker = time.NewTicker(s.cleanupTTL / 2)
	s.cleanupStarted = true

	go func() {
		for {
			select {
			case <-s.cleanupTicker.C:
				s.cleanup()
			case <-s.stopCleanup:
				return
			}
		}
	}()
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

// Stop stops the cleanup goroutine and releases resources.
func (s *InMemoryStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cleanupStarted {
		close(s.stopCleanup)
		s.cleanupTicker.Stop()
		s.cleanupStarted = false
	}
}

// Allow reports whether key should be allowed under the given rate and burst.
func (s *InMemoryStore) Allow(_ context.Context, key string, rps float64, burst int) (bool, error) {
	// Start cleanup on first use
	if !s.cleanupStarted {
		s.startCleanup()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.limiters[key]
	if !exists {
		entry = &limiterEntry{
			limiter:  rate.NewLimiter(rate.Limit(rps), burst),
			lastSeen: time.Now(),
		}
		s.limiters[key] = entry
	} else {
		entry.lastSeen = time.Now()
		// Update limiter parameters if they changed
		if entry.limiter.Limit() != rate.Limit(rps) || entry.limiter.Burst() != burst {
			entry.limiter = rate.NewLimiter(rate.Limit(rps), burst)
		}
	}

	return entry.limiter.Allow(), nil
}
