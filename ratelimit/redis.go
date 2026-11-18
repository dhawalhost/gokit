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

// gcraScript implements Generic Cell Rate Algorithm (GCRA) with O(1) memory per key,
// variable request costs, and server-side TIME so client clock skew cannot bypass rate limits.
// KEYS[1]: the TAT state key.
// ARGV[1]: emission interval in microseconds.
// ARGV[2]: burst offset (emission_interval * burst) in microseconds.
// ARGV[3]: token cost (>= 1).
// Returns: {allowed (0|1), remaining_tokens, retry_after_us, reset_after_us}.
const gcraScript = `
local key = KEYS[1]
local emission_interval = tonumber(ARGV[1])
local burst_offset = tonumber(ARGV[2])
local cost = tonumber(ARGV[3]) or 1
if cost < 1 then
  cost = 1
end

local t = redis.call("TIME")
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])

local tat = tonumber(redis.call("GET", key))
if not tat or tat < now then
  tat = now
end

local increment = emission_interval * cost
local new_tat = tat + increment
local allow_at = new_tat - burst_offset

local allowed = 1
local effective_tat = new_tat
local retry_after = 0
if allow_at > now then
  allowed = 0
  effective_tat = tat
  retry_after = allow_at - now
else
  local ttl_ms = math.floor((new_tat - now + 999) / 1000)
  if ttl_ms < 1 then
    ttl_ms = 1
  end
  redis.call("SET", key, tostring(new_tat), "PX", ttl_ms)
end

local diff = (now + burst_offset) - effective_tat
local remaining = 0
if diff > 0 then
  remaining = math.floor(diff / emission_interval)
end
local reset_after = effective_tat - now
if reset_after < 0 then
  reset_after = 0
end

return {allowed, remaining, math.floor(retry_after), math.floor(reset_after)}
`

// Allow reports whether the request should be permitted under GCRA rate limiting.
func (s *RedisStore) Allow(ctx context.Context, key string, rps float64, burst int) (bool, error) {
	res, err := s.AllowN(ctx, key, 1, rps, burst)
	return res.Allowed, err
}

// AllowN evaluates variable-cost GCRA rate limiting in Redis and returns full quota metadata.
func (s *RedisStore) AllowN(ctx context.Context, key string, cost int, rps float64, burst int) (Result, error) {
	if burst <= 0 {
		return Result{Limit: 0, Remaining: 0}, nil
	}
	if cost < 1 {
		cost = 1
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

	vals, err := s.client.Eval(ctx, gcraScript,
		[]string{fmt.Sprintf("rl:%s", key)},
		emissionIntervalUs, burstOffsetUs, cost,
	).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: redis eval: %w", err)
	}
	if len(vals) < 4 {
		return Result{}, fmt.Errorf("ratelimit: unexpected lua result length %d", len(vals))
	}

	return Result{
		Allowed:    vals[0] == 1,
		Limit:      burst,
		Remaining:  int(vals[1]),
		RetryAfter: time.Duration(vals[2]) * time.Microsecond,
		ResetAfter: time.Duration(vals[3]) * time.Microsecond,
	}, nil
}
