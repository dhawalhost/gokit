package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/dhawalhost/gokit/ratelimit"
)

func newRedisStore(t *testing.T) (*ratelimit.RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	return ratelimit.NewRedisStore(client, 1*time.Second), mr
}

func TestRedisStoreAllow(t *testing.T) {
	tests := []struct {
		name    string
		burst   int
		calls   int
		wantOks []bool
	}{
		{
			name:    "first request allowed",
			burst:   5,
			calls:   1,
			wantOks: []bool{true},
		},
		{
			name:    "allows up to burst",
			burst:   3,
			calls:   3,
			wantOks: []bool{true, true, true},
		},
		{
			name:    "blocks on burst+1",
			burst:   2,
			calls:   3,
			wantOks: []bool{true, true, false},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newRedisStore(t)
			ctx := context.Background()
			for i, want := range tc.wantOks {
				got, err := store.Allow(ctx, "key", 10, tc.burst)
				if err != nil {
					t.Fatalf("call %d Allow: %v", i, err)
				}
				if got != want {
					t.Errorf("call %d: expected ok=%v, got %v", i, want, got)
				}
			}
		})
	}
}

func TestRedisStoreSeparateKeys(t *testing.T) {
	store, _ := newRedisStore(t)
	ctx := context.Background()

	const burst = 2
	for i := 0; i < burst; i++ {
		_, _ = store.Allow(ctx, "keyA", 10, burst)
	}
	okA, _ := store.Allow(ctx, "keyA", 10, burst)
	okB, _ := store.Allow(ctx, "keyB", 10, burst)

	if okA {
		t.Error("keyA should be blocked after exhausting burst")
	}
	if !okB {
		t.Error("keyB should be allowed (independent key)")
	}
}

func TestRedisStoreAllowWhenDown(t *testing.T) {
	store, mr := newRedisStore(t)
	mr.Close()

	_, err := store.Allow(context.Background(), "key", 10, 5)
	if err == nil {
		t.Fatal("expected error when Redis is down")
	}
}

func TestRedisStoreWindowExpiry(t *testing.T) {
	store, mr := newRedisStore(t)
	ctx := context.Background()

	ok, err := store.Allow(ctx, "expkey", 10, 1)
	if err != nil || !ok {
		t.Fatalf("first Allow should succeed: ok=%v err=%v", ok, err)
	}
	ok, _ = store.Allow(ctx, "expkey", 10, 1)
	if ok {
		t.Fatal("second Allow should be blocked")
	}

	mr.FastForward(2 * time.Second)

	ok, err = store.Allow(ctx, "expkey", 10, 1)
	if err != nil {
		t.Fatalf("Allow after expiry: %v", err)
	}
	if !ok {
		t.Error("expected request allowed after window expired")
	}
}
