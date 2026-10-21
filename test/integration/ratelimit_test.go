//go:build integration
// +build integration

package integration_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/ratelimit"
	"github.com/redis/go-redis/v9"
)

func getTestRedisClient(t *testing.T) *redis.Client {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set, skipping integration test")
	}

	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("failed to connect to Redis: %v", err)
	}

	return client
}

func TestInMemoryStoreRateLimit(t *testing.T) {
	store := ratelimit.NewInMemoryStoreWithTTL(5 * time.Second)
	defer store.Stop()

	ctx := context.Background()
	key := "test-user"
	rps := 2.0
	burst := 3

	for i := 0; i < 3; i++ {
		allowed, err := store.Allow(ctx, key, rps, burst)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !allowed {
			t.Errorf("request %d should be allowed", i)
		}
	}

	allowed, err := store.Allow(ctx, key, rps, burst)
	if err != nil {
		t.Fatalf("request 4: %v", err)
	}
	if allowed {
		t.Error("request 4 should be denied")
	}

	time.Sleep(600 * time.Millisecond)
	allowed, _ = store.Allow(ctx, key, rps, burst)
	if !allowed {
		t.Error("request after wait should be allowed")
	}
}

func TestInMemoryStoreCleanup(t *testing.T) {
	ttl := 100 * time.Millisecond
	store := ratelimit.NewInMemoryStoreWithTTL(ttl)
	defer store.Stop()

	ctx := context.Background()

	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("user-%d", i)
		store.Allow(ctx, key, 10, 20)
	}

	time.Sleep(ttl * 3)

	allowed, err := store.Allow(ctx, "new-user", 10, 20)
	if err != nil {
		t.Errorf("store should still work after cleanup: %v", err)
	}
	if !allowed {
		t.Error("new request should be allowed")
	}
}

func TestRedisStoreRateLimit(t *testing.T) {
	client := getTestRedisClient(t)
	defer client.Close()

	store := ratelimit.NewRedisStore(client, 1*time.Second)
	ctx := context.Background()
	key := "test-redis-user"
	burst := 3

	defer client.Del(ctx, "rl:"+key)

	for i := 0; i < 3; i++ {
		allowed, err := store.Allow(ctx, key, 0, burst)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !allowed {
			t.Errorf("request %d should be allowed (burst)", i)
		}
	}

	allowed, err := store.Allow(ctx, key, 0, burst)
	if err != nil {
		t.Fatalf("request 4: %v", err)
	}
	if allowed {
		t.Error("request 4 should be denied")
	}

	time.Sleep(1100 * time.Millisecond)
	allowed, _ = store.Allow(ctx, key, 0, burst)
	if !allowed {
		t.Error("request after window should be allowed")
	}
}

func TestRedisStoreConcurrent(t *testing.T) {
	client := getTestRedisClient(t)
	defer client.Close()

	store := ratelimit.NewRedisStore(client, 1*time.Second)
	ctx := context.Background()
	key := "concurrent-user"
	burst := 10

	defer client.Del(ctx, "rl:"+key)

	var wg sync.WaitGroup
	allowed := 0
	denied := 0
	var mu sync.Mutex

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _ := store.Allow(ctx, key, 0, burst)
			mu.Lock()
			if ok {
				allowed++
			} else {
				denied++
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	if allowed > burst {
		t.Errorf("expected max %d allowed, got %d", burst, allowed)
	}
	if allowed+denied != 20 {
		t.Errorf("expected 20 total requests, got %d", allowed+denied)
	}
}
