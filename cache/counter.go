package cache

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// ShardedCounter solves high-contention lock bottlenecks on hot keys
// by striping increments across N independent slots and aggregating on read.
type ShardedCounter struct {
	client redis.UniversalClient
	slots  int
}

// NewShardedCounter creates a new ShardedCounter with the given number of slots (e.g. 16 or 32).
func NewShardedCounter(client redis.UniversalClient, numSlots int) *ShardedCounter {
	if numSlots <= 0 {
		numSlots = 16
	}
	return &ShardedCounter{
		client: client,
		slots:  numSlots,
	}
}

// Incr increments the counter by delta, picking a random slot to eliminate contention.
func (sc *ShardedCounter) Incr(ctx context.Context, key string, delta int64) (int64, error) {
	slot := rand.Intn(sc.slots) //nolint:gosec // pseudo-random distribution is sufficient
	slotKey := fmt.Sprintf("%s:slot:%d", key, slot)

	return sc.client.IncrBy(ctx, slotKey, delta).Result()
}

// Get aggregates and returns the sum of all slots for the given counter key.
func (sc *ShardedCounter) Get(ctx context.Context, key string) (int64, error) {
	slotKeys := make([]string, sc.slots)
	for i := 0; i < sc.slots; i++ {
		slotKeys[i] = fmt.Sprintf("%s:slot:%d", key, i)
	}

	vals, err := sc.client.MGet(ctx, slotKeys...).Result()
	if err != nil {
		return 0, fmt.Errorf("sharded_counter: mget %q: %w", key, err)
	}

	var total int64
	for _, v := range vals {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			parsed, parseErr := strconv.ParseInt(s, 10, 64)
			if parseErr == nil {
				total += parsed
			}
		}
	}

	return total, nil
}

// Reset removes all slot keys for the counter.
func (sc *ShardedCounter) Reset(ctx context.Context, key string) error {
	slotKeys := make([]string, sc.slots)
	for i := 0; i < sc.slots; i++ {
		slotKeys[i] = fmt.Sprintf("%s:slot:%d", key, i)
	}

	return sc.client.Del(ctx, slotKeys...).Err()
}

// decrementLua atomically checks if key >= amount and decrements, or returns -1 if insufficient.
var decrementLua = redis.NewScript(`
local current = redis.call('get', KEYS[1])
if not current then
    return -1
end
local curNum = tonumber(current)
local amount = tonumber(ARGV[1])
local floor = tonumber(ARGV[2])
if curNum - amount < floor then
    return -1
end
return redis.call('decrby', KEYS[1], amount)
`)

// AtomicDecrementWithFloor atomically decrements key by amount, guaranteeing the value does not drop below floor.
// Used for flash sale inventory reservations to prevent overselling without distributed locking overhead.
func AtomicDecrementWithFloor(ctx context.Context, client redis.UniversalClient, key string, amount int64, floor int64) (int64, error) {
	res, err := decrementLua.Run(ctx, client, []string{key}, amount, floor).Result()
	if err != nil {
		return 0, fmt.Errorf("sharded_counter: decrement: %w", err)
	}

	remaining, ok := res.(int64)
	if !ok || remaining < 0 {
		return 0, ErrInsufficientQuantity
	}
	return remaining, nil
}
