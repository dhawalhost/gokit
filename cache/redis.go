package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dhawalhost/gokit/config"
)

// RedisCache is a Cache backed by Redis (supports standalone, cluster, or sentinel).
type RedisCache struct {
	client    redis.UniversalClient
	coalescer *Coalescer
}

// NewRedis creates a new RedisCache from the given config.
// It automatically supports single-node Redis, Redis Cluster, and Redis Sentinel.
func NewRedis(cfg config.RedisConfig) (*RedisCache, error) {
	addrs := cfg.Addrs
	if len(addrs) == 0 && cfg.Addr != "" {
		addrs = []string{cfg.Addr}
	}

	opts := &redis.UniversalOptions{
		Addrs:        addrs,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		PoolSize:     cfg.PoolSize,
		PoolTimeout:  cfg.PoolTimeout,
		MasterName:   cfg.MasterName,
	}

	if cfg.Cluster {
		opts.IsClusterMode = true
		// Redis Cluster only supports database 0.
		opts.DB = 0
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

	client := redis.NewUniversalClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("cache: redis ping: %w", err)
	}
	return &RedisCache{
		client:    client,
		coalescer: NewCoalescer(),
	}, nil
}

// NewRedisFromClient creates a RedisCache from an existing redis.UniversalClient.
func NewRedisFromClient(client redis.UniversalClient) *RedisCache {
	return &RedisCache{
		client:    client,
		coalescer: NewCoalescer(),
	}
}

// Client returns the underlying redis.UniversalClient instance.
func (r *RedisCache) Client() redis.UniversalClient {
	return r.client
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

// Delete removes the given keys in a cluster-safe manner (avoiding CROSSSLOT errors).
func (r *RedisCache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if len(keys) == 1 {
		if err := r.client.Del(ctx, keys[0]).Err(); err != nil {
			return fmt.Errorf("cache: delete %q: %w", keys[0], err)
		}
		return nil
	}

	// For multiple keys, use a pipeline so keys distributed across different
	// Redis Cluster hash slots are routed to their respective shards without CROSSSLOT errors.
	pipe := r.client.Pipeline()
	for _, k := range keys {
		pipe.Del(ctx, k)
	}
	if _, err := pipe.Exec(ctx); err != nil {
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
// For Redis Cluster, it flushes all master nodes in the cluster.
func (r *RedisCache) Flush(ctx context.Context) error {
	if clusterClient, ok := r.client.(*redis.ClusterClient); ok {
		err := clusterClient.ForEachMaster(ctx, func(ctx context.Context, master *redis.Client) error {
			return master.FlushDB(ctx).Err()
		})
		if err != nil {
			return fmt.Errorf("cache: flush cluster: %w", err)
		}
		return nil
	}

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

// GetOrSet retrieves a cached value or executes fetch with singleflight deduplication
// to prevent cache stampedes under high concurrency.
func (r *RedisCache) GetOrSet(ctx context.Context, key string, ttl time.Duration, fetch FetcherFunc) (string, error) {
	return r.coalescer.Do(ctx, r, key, ttl, fetch)
}
