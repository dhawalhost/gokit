package distlock

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

type memoryLockEntry struct {
	token     string
	expiresAt time.Time
}

// InMemoryLocker provides an in-memory Locker implementation for unit testing and local development.
type InMemoryLocker struct {
	mu            sync.Mutex
	locks         map[string]memoryLockEntry
	fencingTokens map[string]int64
}

// NewInMemoryLocker creates a new thread-safe InMemoryLocker.
func NewInMemoryLocker() *InMemoryLocker {
	return &InMemoryLocker{
		locks:         make(map[string]memoryLockEntry),
		fencingTokens: make(map[string]int64),
	}
}

// MemoryLock represents an acquired in-memory lock.
type MemoryLock struct {
	*BaseLock
	locker *InMemoryLocker
}

// Obtain attempts to acquire an in-memory lock for key.
func (m *InMemoryLocker) Obtain(ctx context.Context, key string, ttl time.Duration, opts ...Option) (Lock, error) {
	options := Options{
		Token:      uuid.New().String(),
		Retries:    0,
		RetryDelay: 10 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(&options)
	}

	maxAttempts := options.Retries + 1

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if ok, fToken := m.tryAcquire(key, options.Token, ttl); ok {
			base := NewBaseLock(key, options.Token, ttl, fToken)
			lock := &MemoryLock{
				BaseLock: base,
				locker:   m,
			}

			if options.AutoRenew {
				lock.StartAutoRenew(ctx, options.AutoRenewInterval, lock.Extend)
			}

			return lock, nil
		}

		if attempt < options.Retries {
			delay := RetryDelayWithJitter(options.RetryDelay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	return nil, ErrLockHeld
}

func (m *InMemoryLocker) tryAcquire(key, token string, ttl time.Duration) (bool, int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	entry, exists := m.locks[key]

	// If entry exists and has not expired, it cannot be acquired
	if exists && entry.expiresAt.After(now) {
		return false, 0
	}

	m.locks[key] = memoryLockEntry{
		token:     token,
		expiresAt: now.Add(ttl),
	}
	m.fencingTokens[key]++
	return true, m.fencingTokens[key]
}

// Unlock releases the in-memory lock if token matches.
func (l *MemoryLock) Unlock(_ context.Context) error {
	l.StopAutoRenew()

	l.locker.mu.Lock()
	defer l.locker.mu.Unlock()

	entry, exists := l.locker.locks[l.key]
	if !exists {
		return ErrLockReleased
	}

	if entry.token != l.token {
		return ErrTokenMismatch
	}

	delete(l.locker.locks, l.key)
	return nil
}

// Extend extends the in-memory lock's TTL if token matches and not expired.
func (l *MemoryLock) Extend(_ context.Context, ttl time.Duration) error {
	l.locker.mu.Lock()
	defer l.locker.mu.Unlock()

	now := time.Now()
	entry, exists := l.locker.locks[l.key]
	if !exists || entry.expiresAt.Before(now) {
		return ErrLockReleased
	}

	if entry.token != l.token {
		return ErrTokenMismatch
	}

	entry.expiresAt = now.Add(ttl)
	l.locker.locks[l.key] = entry
	return nil
}

// WithLock acquires an in-memory lock, runs fn, and guarantees release of the lock.
func (m *InMemoryLocker) WithLock(ctx context.Context, key string, ttl time.Duration, fn func(ctx context.Context) error, opts ...Option) error {
	lock, err := m.Obtain(ctx, key, ttl, opts...)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Unlock(context.Background())
	}()

	return fn(ctx)
}
