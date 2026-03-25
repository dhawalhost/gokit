package ratelimit_test

import (
	"context"
	"testing"

	"github.com/dhawalhost/gokit/ratelimit"
)

func TestInMemoryAllowsUnderLimit(t *testing.T) {
	store := ratelimit.NewInMemoryStore()
	ctx := context.Background()

	ok, err := store.Allow(ctx, "user1", 10, 5)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !ok {
		t.Error("expected first request to be allowed")
	}
}

func TestInMemoryBlocksOverBurst(t *testing.T) {
	store := ratelimit.NewInMemoryStore()
	ctx := context.Background()

	// burst=2 means only 2 tokens available initially
	for i := 0; i < 2; i++ {
		ok, err := store.Allow(ctx, "user2", 0.001, 2)
		if err != nil {
			t.Fatalf("Allow[%d]: %v", i, err)
		}
		if !ok {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	// Third request with near-zero rps should be blocked.
	ok, err := store.Allow(ctx, "user2", 0.001, 2)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if ok {
		t.Error("expected third request to be blocked")
	}
}

func TestInMemorySeparateKeys(t *testing.T) {
	store := ratelimit.NewInMemoryStore()
	ctx := context.Background()

	// Exhaust key "a"
	for i := 0; i < 3; i++ {
		_, _ = store.Allow(ctx, "a", 0.001, 3)
	}
	okA, _ := store.Allow(ctx, "a", 0.001, 3)
	okB, _ := store.Allow(ctx, "b", 0.001, 3)

	if okA {
		t.Error("key 'a' should be blocked")
	}
	if !okB {
		t.Error("key 'b' should be allowed (separate limiter)")
	}
}
