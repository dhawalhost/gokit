package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is a rate-limit store that uses a Redis sliding-window counter
// implemented with a Lua script.
type RedisStore struct {
	client         *redis.Client
	windowDuration time.Duration
}

// NewRedisStore creates a RedisStore backed by the given Redis client and window duration.
func NewRedisStore(client *redis.Client, windowDuration time.Duration) *RedisStore {
	return &RedisStore{client: client, windowDuration: windowDuration}
}

// slidingWindowScript implements a sliding-window counter using sorted sets.
// KEYS[1]: the sorted set key
// ARGV[1]: current timestamp in milliseconds
// ARGV[2]: window start (current - windowMs)
// ARGV[3]: max requests (burst)
// ARGV[4]: window duration in milliseconds (for key expiry)
const slidingWindowScript = `
local key    = KEYS[1]
local now    = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit  = tonumber(ARGV[3])
local expiry = tonumber(ARGV[4])

redis.call("ZREMRANGEBYSCORE", key, "-inf", window)
local count = redis.call("ZCARD", key)
if count < limit then
  redis.call("ZADD", key, now, now)
  redis.call("PEXPIRE", key, expiry)
  return 1
end
return 0
`

// Allow reports whether the request should be permitted under the sliding window.
func (s *RedisStore) Allow(ctx context.Context, key string, _ float64, burst int) (bool, error) {
	nowMs := time.Now().UnixMilli()
	windowMs := nowMs - s.windowDuration.Milliseconds()

	result, err := s.client.Eval(ctx, slidingWindowScript,
		[]string{fmt.Sprintf("rl:%s", key)},
		nowMs, windowMs, burst, s.windowDuration.Milliseconds(),
	).Int()
	if err != nil {
		return false, fmt.Errorf("ratelimit: redis eval: %w", err)
	}
	return result == 1, nil
}
