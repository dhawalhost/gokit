package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/ratelimit"
)

func newStore() *ratelimit.InMemoryStore {
	return ratelimit.NewInMemoryStoreWithTTL(100 * time.Millisecond)
}

func TestInMemoryAllow(t *testing.T) {
	tests := []struct {
		name    string
		rps     float64
		burst   int
		calls   int
		wantOks []bool
	}{
		{
			name:    "first request allowed",
			rps:     10,
			burst:   5,
			calls:   1,
			wantOks: []bool{true},
		},
		{
			name:    "stays allowed within burst",
			rps:     100,
			burst:   3,
			calls:   3,
			wantOks: []bool{true, true, true},
		},
		{
			name:    "blocks after burst exhausted",
			rps:     0.001,
			burst:   2,
			calls:   3,
			wantOks: []bool{true, true, false},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore()
			ctx := context.Background()
			for i, want := range tc.wantOks {
				got, err := store.Allow(ctx, "key", tc.rps, tc.burst)
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

func TestInMemorySeparateKeys(t *testing.T) {
	store := newStore()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, _ = store.Allow(ctx, "a", 0.001, 3)
	}
	okA, _ := store.Allow(ctx, "a", 0.001, 3)
	okB, _ := store.Allow(ctx, "b", 0.001, 3)

	if okA {
		t.Error("key 'a' should be blocked after exhausting burst")
	}
	if !okB {
		t.Error("key 'b' should be allowed (independent limiter)")
	}
}

func TestInMemoryStop(t *testing.T) {
	store := newStore()
	ctx := context.Background()

	_, _ = store.Allow(ctx, "x", 10, 5)

	store.Stop()
	store.Stop()
}

func TestInMemoryStopBeforeFirstAllow(t *testing.T) {
	store := newStore()
	store.Stop()
}

func TestInMemoryCleanupEvictsStaleEntries(t *testing.T) {
	store := ratelimit.NewInMemoryStoreWithTTL(20 * time.Millisecond)
	ctx := context.Background()

	_, _ = store.Allow(ctx, "stale", 10, 5)

	time.Sleep(80 * time.Millisecond)

	ok, err := store.Allow(ctx, "stale", 10, 5)
	if err != nil {
		t.Fatalf("Allow after cleanup: %v", err)
	}
	if !ok {
		t.Error("expected stale entry to be evicted and new request allowed")
	}
	store.Stop()
}

func TestInMemoryStartCleanupIdempotent(t *testing.T) {
	store := newStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, _ = store.Allow(ctx, "k", 100, 10)
	}
	store.Stop()
}

func TestNewInMemoryStore(t *testing.T) {
	store := ratelimit.NewInMemoryStore()
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	ctx := context.Background()
	ok, err := store.Allow(ctx, "key", 10, 5)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !ok {
		t.Error("expected first request to be allowed")
	}
	store.Stop()
}

func TestAllowLimiterParamUpdate(t *testing.T) {
	store := ratelimit.NewInMemoryStoreWithTTL(time.Minute)
	ctx := context.Background()

	_, err := store.Allow(ctx, "key", 10, 5)
	if err != nil {
		t.Fatalf("first Allow: %v", err)
	}
	_, err = store.Allow(ctx, "key", 20, 10)
	if err != nil {
		t.Fatalf("second Allow: %v", err)
	}
	store.Stop()
}
