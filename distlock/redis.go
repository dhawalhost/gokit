package distlock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	defaultKeyPrefix = "distlock:"

	// Lua script for atomic unlock: checks token ownership before deleting.
	unlockLuaScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end
`

	// Lua script for atomic TTL extension: checks token ownership before updating expiry.
	extendLuaScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("pexpire", KEYS[1], ARGV[2])
else
    return 0
end
`
)

// RedisLocker implements Locker using Redis as the distributed state store (standalone, cluster, or sentinel).
type RedisLocker struct {
	client    redis.UniversalClient
	keyPrefix string
}

// RedisOption configures a RedisLocker.
type RedisOption func(*RedisLocker)

// WithKeyPrefix sets a custom prefix for Redis lock keys.
func WithKeyPrefix(prefix string) RedisOption {
	return func(r *RedisLocker) {
		r.keyPrefix = prefix
	}
}

// NewRedisLocker creates a new RedisLocker instance.
func NewRedisLocker(client redis.UniversalClient, opts ...RedisOption) *RedisLocker {
	locker := &RedisLocker{
		client:    client,
		keyPrefix: defaultKeyPrefix,
	}
	for _, opt := range opts {
		opt(locker)
	}
	return locker
}

// RedisLock represents an acquired Redis-backed distributed lock.
type RedisLock struct {
	*BaseLock
	client  redis.UniversalClient
	fullKey string
}

// Obtain attempts to acquire a distributed lock in Redis.
func (r *RedisLocker) Obtain(ctx context.Context, key string, ttl time.Duration, opts ...Option) (Lock, error) {
	options := Options{
		Token:      uuid.New().String(),
		Retries:    0,
		RetryDelay: 20 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(&options)
	}

	fullKey := r.keyPrefix + key
	maxAttempts := options.Retries + 1

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Try atomic SET key token NX PX ttl
		setErr := r.client.SetArgs(ctx, fullKey, options.Token, redis.SetArgs{
			Mode: "NX",
			TTL:  ttl,
		}).Err()

		if setErr == nil {
			base := NewBaseLock(key, options.Token, ttl)
			lock := &RedisLock{
				BaseLock: base,
				client:   r.client,
				fullKey:  fullKey,
			}

			if options.AutoRenew {
				lock.StartAutoRenew(ctx, options.AutoRenewInterval, lock.Extend)
			}

			return lock, nil
		}

		if !errors.Is(setErr, redis.Nil) {
			return nil, fmt.Errorf("distlock: redis set error: %w", setErr)
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

// Unlock releases the lock atomically in Redis if the token matches.
func (l *RedisLock) Unlock(ctx context.Context) error {
	l.StopAutoRenew()

	res, err := l.client.Eval(ctx, unlockLuaScript, []string{l.fullKey}, l.token).Result()
	if err != nil {
		return fmt.Errorf("distlock: redis unlock script error: %w", err)
	}

	deleted, ok := res.(int64)
	if !ok || deleted == 0 {
		return ErrLockReleased
	}

	return nil
}

// Extend updates the lock TTL atomically in Redis if the token matches.
func (l *RedisLock) Extend(ctx context.Context, ttl time.Duration) error {
	ttlMs := int64(ttl / time.Millisecond)
	res, err := l.client.Eval(ctx, extendLuaScript, []string{l.fullKey}, l.token, ttlMs).Result()
	if err != nil {
		return fmt.Errorf("distlock: redis extend script error: %w", err)
	}

	extended, ok := res.(int64)
	if !ok || extended == 0 {
		return ErrLockReleased
	}

	return nil
}

// WithLock acquires a lock, runs fn, and guarantees release of the lock.
func (r *RedisLocker) WithLock(ctx context.Context, key string, ttl time.Duration, fn func(ctx context.Context) error, opts ...Option) error {
	lock, err := r.Obtain(ctx, key, ttl, opts...)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Unlock(context.Background())
	}()

	return fn(ctx)
}
