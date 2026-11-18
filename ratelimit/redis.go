package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is a rate-limit store that uses the Generic Cell Rate Algorithm (GCRA)
// implemented as a constant-space Lua script using Redis server TIME
// (supports standalone, cluster, and sentinel).
type RedisStore struct {
	client         redis.UniversalClient
	windowDuration time.Duration
}

// NewRedisStore creates a RedisStore backed by the given Redis client and window duration.
func NewRedisStore(client redis.UniversalClient, windowDuration time.Duration) *RedisStore {
	return &RedisStore{client: client, windowDuration: windowDuration}
}

// gcraScript implements Generic Cell Rate Algorithm (GCRA) with O(1) memory per key
// and server-side TIME so client clock skew cannot bypass rate limits.
// KEYS[1]: the TAT state key.
// ARGV[1]: emission interval in microseconds.
// ARGV[2]: burst offset (emission_interval * burst) in microseconds.
const gcraScript = `
local key = KEYS[1]
local emission_interval = tonumber(ARGV[1])
local burst_offset = tonumber(ARGV[2])

local t = redis.call("TIME")
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])

local tat = tonumber(redis.call("GET", key))
if not tat or tat < now then
  tat = now
end

local new_tat = tat + emission_interval
local allow_at = new_tat - burst_offset
if allow_at > now then
  return 0
end

local ttl_ms = math.floor((new_tat - now + 999) / 1000)
if ttl_ms < 1 then
  ttl_ms = 1
end
redis.call("SET", key, tostring(new_tat), "PX", ttl_ms)
return 1
`

// Allow reports whether the request should be permitted under GCRA rate limiting.
func (s *RedisStore) Allow(ctx context.Context, key string, rps float64, burst int) (bool, error) {
	if burst <= 0 {
		return false, nil
	}

	var emissionIntervalUs int64
	switch {
	case rps > 0:
		emissionIntervalUs = int64(float64(time.Second/time.Microsecond) / rps)
	case s.windowDuration > 0:
		emissionIntervalUs = s.windowDuration.Microseconds() / int64(burst)
	default:
		emissionIntervalUs = int64(time.Second/time.Microsecond) / int64(burst)
	}
	if emissionIntervalUs < 1 {
		emissionIntervalUs = 1
	}
	burstOffsetUs := emissionIntervalUs * int64(burst)

	result, err := s.client.Eval(ctx, gcraScript,
		[]string{fmt.Sprintf("rl:%s", key)},
		emissionIntervalUs, burstOffsetUs,
	).Int()
	if err != nil {
		return false, fmt.Errorf("ratelimit: redis eval: %w", err)
	}
	return result == 1, nil
}
