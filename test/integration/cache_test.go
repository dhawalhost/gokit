//go:build integration
// +build integration

package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/cache"
	"github.com/dhawalhost/gokit/config"
)

func getTestRedisAddr(t *testing.T) string {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set, skipping integration test")
	}
	return addr
}

func TestRedisCacheConnection(t *testing.T) {
	addr := getTestRedisAddr(t)

	cfg := config.RedisConfig{
		Addr:         addr,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}

	cache, err := cache.NewRedis(cfg)
	if err != nil {
		t.Fatalf("failed to connect to Redis: %v", err)
	}
	defer cache.Close()

	ctx := context.Background()
	if err := cache.HealthCheck(ctx); err != nil {
		t.Errorf("health check failed: %v", err)
	}
}

func TestRedisCacheOperations(t *testing.T) {
	addr := getTestRedisAddr(t)
	cfg := config.RedisConfig{Addr: addr}

	cache, err := cache.NewRedis(cfg)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer cache.Close()

	ctx := context.Background()

	// Clean up test keys
	defer cache.Delete(ctx, "test:key1", "test:key2")

	t.Run("Set and Get", func(t *testing.T) {
		key := "test:key1"
		value := "test-value"
		ttl := 10 * time.Second

		if err := cache.Set(ctx, key, value, ttl); err != nil {
			t.Fatalf("failed to set: %v", err)
		}

		got, err := cache.Get(ctx, key)
		if err != nil {
			t.Fatalf("failed to get: %v", err)
		}

		if got != value {
			t.Errorf("expected %q, got %q", value, got)
		}
	})

	t.Run("Exists", func(t *testing.T) {
		key := "test:key2"
		cache.Set(ctx, key, "value", 10*time.Second)

		exists, err := cache.Exists(ctx, key)
		if err != nil {
			t.Fatalf("failed to check exists: %v", err)
		}
		if !exists {
			t.Error("expected key to exist")
		}

		exists, err = cache.Exists(ctx, "nonexistent:key")
		if err != nil {
			t.Fatalf("failed to check exists: %v", err)
		}
		if exists {
			t.Error("expected key not to exist")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		key := "test:delete"
		cache.Set(ctx, key, "value", 10*time.Second)

		if err := cache.Delete(ctx, key); err != nil {
			t.Fatalf("failed to delete: %v", err)
		}

		exists, _ := cache.Exists(ctx, key)
		if exists {
			t.Error("expected key to be deleted")
		}
	})

	t.Run("TTL Expiration", func(t *testing.T) {
		key := "test:ttl"
		ttl := 1 * time.Second

		cache.Set(ctx, key, "value", ttl)

		// Wait for expiration
		time.Sleep(2 * time.Second)

		exists, _ := cache.Exists(ctx, key)
		if exists {
			t.Error("expected key to be expired")
		}
	})
}

func TestRedisCacheClose(t *testing.T) {
	addr := getTestRedisAddr(t)
	cfg := config.RedisConfig{Addr: addr}

	cache, err := cache.NewRedis(cfg)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	if err := cache.Close(); err != nil {
		t.Errorf("failed to close: %v", err)
	}

	// Operations after close should fail
	ctx := context.Background()
	err = cache.Set(ctx, "key", "value", time.Second)
	if err == nil {
		t.Error("expected error after close")
	}
}
