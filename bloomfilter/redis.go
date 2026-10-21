package bloomfilter

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is a distributed Bloom filter that stores bits inside a single
// Redis string key using SETBIT / GETBIT. It uses the same double-hashing
// strategy as Filter so both backends are interchangeable for membership tests
// when they share the same m and k parameters.
type RedisStore struct {
	client *redis.Client
	key    string
	m      uint
	k      uint
}

// NewRedisStore creates a RedisStore sized for expectedItems elements at the
// given falsePositiveRate (0 < p < 1), storing bits under key.
func NewRedisStore(client *redis.Client, key string, expectedItems uint, falsePositiveRate float64) (*RedisStore, error) {
	if client == nil {
		return nil, ErrNilRedisClient
	}
	if key == "" {
		return nil, ErrEmptyKey
	}
	if expectedItems == 0 {
		return nil, ErrExpectedItemsZero
	}
	if falsePositiveRate <= 0 || falsePositiveRate >= 1 {
		return nil, ErrInvalidFalsePositiveRate
	}
	m := optimalBitSize(expectedItems, falsePositiveRate)
	k := optimalHashCount(m, expectedItems)
	return &RedisStore{
		client: client,
		key:    key,
		m:      m,
		k:      k,
	}, nil
}

// Add inserts data into the Redis-backed filter.
// All k SETBIT commands are pipelined in a single round-trip.
func (r *RedisStore) Add(ctx context.Context, data []byte) error {
	h1, h2 := hashes(data)
	pipe := r.client.Pipeline()
	for i := uint(0); i < r.k; i++ {
		idx := (h1 + uint64(i)*h2) % uint64(r.m)
		pipe.SetBit(ctx, r.key, safeInt64FromUint64(idx), 1)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("bloomfilter: redis add: %w", err)
	}
	return nil
}

// AddString inserts a string into the Redis-backed filter.
func (r *RedisStore) AddString(ctx context.Context, s string) error {
	return r.Add(ctx, []byte(s))
}

// Contains reports whether data is possibly present in the Redis-backed filter.
// All k GETBIT commands are pipelined in a single round-trip.
func (r *RedisStore) Contains(ctx context.Context, data []byte) (bool, error) {
	h1, h2 := hashes(data)
	pipe := r.client.Pipeline()
	cmds := make([]*redis.IntCmd, r.k)
	for i := uint(0); i < r.k; i++ {
		idx := (h1 + uint64(i)*h2) % uint64(r.m)
		cmds[i] = pipe.GetBit(ctx, r.key, safeInt64FromUint64(idx))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("bloomfilter: redis contains: %w", err)
	}
	for _, cmd := range cmds {
		if cmd.Val() == 0 {
			return false, nil
		}
	}
	return true, nil
}

// ContainsString reports whether the string is possibly in the Redis-backed filter.
func (r *RedisStore) ContainsString(ctx context.Context, s string) (bool, error) {
	return r.Contains(ctx, []byte(s))
}

// Expire sets a TTL on the underlying Redis key, useful for time-windowed
// deduplication (e.g. "have I processed this event in the last 24 hours?").
func (r *RedisStore) Expire(ctx context.Context, ttl time.Duration) error {
	if err := r.client.Expire(ctx, r.key, ttl).Err(); err != nil {
		return fmt.Errorf("bloomfilter: redis expire: %w", err)
	}
	return nil
}

// Delete removes the underlying Redis key, effectively resetting the filter.
func (r *RedisStore) Delete(ctx context.Context) error {
	if err := r.client.Del(ctx, r.key).Err(); err != nil {
		return fmt.Errorf("bloomfilter: redis delete: %w", err)
	}
	return nil
}

// BitSize returns the number of bits allocated for this filter.
func (r *RedisStore) BitSize() uint { return r.m }

// HashFunctions returns the number of hash functions used.
func (r *RedisStore) HashFunctions() uint { return r.k }

func safeInt64FromUint64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}
