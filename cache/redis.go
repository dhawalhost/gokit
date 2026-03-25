package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/dhawalhost/gokit/config"
	"github.com/redis/go-redis/v9"
)

// RedisCache is a Cache backed by Redis.
type RedisCache struct {
	client *redis.Client
}

// NewRedis creates a new RedisCache from the given config.
func NewRedis(cfg config.RedisConfig) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("cache: redis ping: %w", err)
	}
	return &RedisCache{client: client}, nil
}

// HealthCheck pings the Redis server.
func (r *RedisCache) HealthCheck(ctx context.Context) error {
	if err := r.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("cache: redis ping: %w", err)
	}
	return nil
}

// Get retrieves the string value stored at key.
func (r *RedisCache) Get(ctx context.Context, key string) (string, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		return "", fmt.Errorf("cache: get %q: %w", key, err)
	}
	return val, nil
}

// Set stores value under key with the given TTL.
func (r *RedisCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	if err := r.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache: set %q: %w", key, err)
	}
	return nil
}

// Delete removes the given keys.
func (r *RedisCache) Delete(ctx context.Context, keys ...string) error {
	if err := r.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("cache: delete: %w", err)
	}
	return nil
}

// Exists reports whether key exists in Redis.
func (r *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	n, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("cache: exists %q: %w", key, err)
	}
	return n > 0, nil
}

// Flush deletes all keys in the current Redis database.
func (r *RedisCache) Flush(ctx context.Context) error {
	if err := r.client.FlushDB(ctx).Err(); err != nil {
		return fmt.Errorf("cache: flush: %w", err)
	}
	return nil
}
