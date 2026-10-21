package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dhawalhost/gokit/config"
)

// RedisCache is a Cache backed by Redis.
type RedisCache struct {
	client *redis.Client
}

// NewRedis creates a new RedisCache from the given config.
func NewRedis(cfg config.RedisConfig) (*RedisCache, error) {
	opts := &redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		PoolSize:     cfg.PoolSize,
		PoolTimeout:  cfg.PoolTimeout,
	}

	// Set default timeouts if not configured
	if opts.DialTimeout == 0 {
		opts.DialTimeout = 5 * time.Second
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = 3 * time.Second
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = 3 * time.Second
	}
	if opts.PoolSize == 0 {
		opts.PoolSize = 10
	}
	if opts.PoolTimeout == 0 {
		opts.PoolTimeout = 4 * time.Second
	}

	client := redis.NewClient(opts)
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
func (r *RedisCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
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

// Close closes the Redis client connection.
func (r *RedisCache) Close() error {
	if err := r.client.Close(); err != nil {
		return fmt.Errorf("cache: close: %w", err)
	}
	return nil
}
