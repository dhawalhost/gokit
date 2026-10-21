// Package distlock provides distributed locking primitives for coordinated
// execution across distributed systems and microservices.
package distlock

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors returned by Locker and Lock operations.
var (
	// ErrLockHeld is returned when a lock cannot be acquired because another holder owns it.
	ErrLockHeld = errors.New("distlock: lock is already held")
	// ErrLockNotObtained is returned when all acquisition attempts fail.
	ErrLockNotObtained = errors.New("distlock: failed to obtain lock")
	// ErrLockReleased is returned when unlocking a lock that is already released or expired.
	ErrLockReleased = errors.New("distlock: lock already released or expired")
	// ErrTokenMismatch is returned when trying to release or extend a lock with an invalid owner token.
	ErrTokenMismatch = errors.New("distlock: lock token mismatch")
)

// Lock represents an acquired distributed lock.
type Lock interface {
	// Key returns the identifier of the resource being locked.
	Key() string
	// Token returns the unique ownership token associated with this lock acquisition.
	Token() string
	// TTL returns the initial time-to-live duration configured for this lock.
	TTL() time.Duration
	// Unlock releases the lock if and only if the ownership token still matches.
	Unlock(ctx context.Context) error
	// Extend extends the validity of the lock by the given duration.
	Extend(ctx context.Context, ttl time.Duration) error
}

// Locker defines the interface for creating and managing distributed locks.
type Locker interface {
	// Obtain attempts to acquire a lock for key with the given TTL and options.
	Obtain(ctx context.Context, key string, ttl time.Duration, opts ...Option) (Lock, error)
	// WithLock acquires a lock, executes fn, and guarantees release of the lock upon completion.
	WithLock(ctx context.Context, key string, ttl time.Duration, fn func(ctx context.Context) error, opts ...Option) error
}

// Options configures the lock acquisition behavior.
type Options struct {
	// Token is the unique identifier for the lock owner. If empty, a UUID is generated.
	Token string
	// Retries is the number of retry attempts to acquire the lock.
	Retries int
	// RetryDelay is the base delay between acquisition attempts.
	RetryDelay time.Duration
	// AutoRenew enables a background goroutine to periodically extend the lock.
	AutoRenew bool
	// AutoRenewInterval is the period between renewal requests.
	AutoRenewInterval time.Duration
}

// Option modifies Options.
type Option func(*Options)

// WithToken sets a custom ownership token.
func WithToken(token string) Option {
	return func(o *Options) {
		o.Token = token
	}
}

// WithRetry configures acquisition retries with jittered delay.
func WithRetry(retries int, delay time.Duration) Option {
	return func(o *Options) {
		o.Retries = retries
		o.RetryDelay = delay
	}
}

// WithAutoRenew configures background auto-renewal to keep the lock alive
// while long-running operations are executing.
func WithAutoRenew(interval time.Duration) Option {
	return func(o *Options) {
		o.AutoRenew = true
		o.AutoRenewInterval = interval
	}
}

// BaseLock provides common lock functionality including auto-renewal lifecycle.
type BaseLock struct {
	key        string
	token      string
	ttl        time.Duration
	renewStop  chan struct{}
	renewDone  chan struct{}
	renewMu    sync.Mutex
	isRenewing bool
}

// NewBaseLock initializes a BaseLock.
func NewBaseLock(key, token string, ttl time.Duration) *BaseLock {
	if token == "" {
		token = uuid.New().String()
	}
	return &BaseLock{
		key:   key,
		token: token,
		ttl:   ttl,
	}
}

// Key returns the locked resource key.
func (l *BaseLock) Key() string {
	return l.key
}

// Token returns the unique owner token.
func (l *BaseLock) Token() string {
	return l.token
}

// TTL returns the configured TTL.
func (l *BaseLock) TTL() time.Duration {
	return l.ttl
}

// StartAutoRenew starts a background goroutine that periodically calls renewFn.
func (l *BaseLock) StartAutoRenew(ctx context.Context, interval time.Duration, renewFn func(ctx context.Context, ttl time.Duration) error) {
	l.renewMu.Lock()
	defer l.renewMu.Unlock()

	if l.isRenewing {
		return
	}

	if interval <= 0 {
		interval = l.ttl / 3
		if interval <= 0 {
			interval = 100 * time.Millisecond
		}
	}

	l.renewStop = make(chan struct{})
	l.renewDone = make(chan struct{})
	l.isRenewing = true

	go func() {
		defer close(l.renewDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), l.ttl)
				_ = renewFn(renewCtx, l.ttl)
				cancel()
			case <-l.renewStop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// StopAutoRenew signals the renewal goroutine to stop and waits for it to exit.
func (l *BaseLock) StopAutoRenew() {
	l.renewMu.Lock()
	defer l.renewMu.Unlock()

	if !l.isRenewing {
		return
	}

	close(l.renewStop)
	<-l.renewDone
	l.isRenewing = false
}

// RetryDelayWithJitter calculates a jittered delay for acquisition retry attempts.
func RetryDelayWithJitter(base time.Duration) time.Duration {
	if base <= 0 {
		base = 20 * time.Millisecond
	}
	// Jitter between 50% and 150% of base
	half := int64(base / 2)
	if half <= 0 {
		return base
	}
	jitter := rand.Int63n(half * 2) //nolint:gosec // sufficient for retry jitter
	return time.Duration(int64(base) - half + jitter)
}
