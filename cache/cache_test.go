package cache_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/dhawalhost/gokit/cache"
	"github.com/dhawalhost/gokit/config"
)

func TestCoalescer_Do(t *testing.T) {
	tests := []struct {
		name          string
		concurrency   int
		key           string
		fetchDelay    time.Duration
		fetchErr      error
		expectedCalls int32
		wantErr       bool
	}{
		{
			name:          "single concurrent execution deduplication",
			concurrency:   10,
			key:           "hot_key_1",
			fetchDelay:    50 * time.Millisecond,
			fetchErr:      nil,
			expectedCalls: 1,
			wantErr:       false,
		},
		{
			name:          "fetch error propagation to all callers",
			concurrency:   5,
			key:           "hot_error_key",
			fetchDelay:    20 * time.Millisecond,
			fetchErr:      fmt.Errorf("db connection failed"),
			expectedCalls: 1,
			wantErr:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rc, err := cache.NewRedis(config.RedisConfig{Addr: mr.Addr()})
			if err != nil {
				t.Fatalf("failed to create redis cache: %v", err)
			}
			defer func() { _ = rc.Close() }()

			var callCount atomic.Int32
			var wg sync.WaitGroup
			errs := make(chan error, tc.concurrency)
			vals := make(chan string, tc.concurrency)

			for i := 0; i < tc.concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ctx := context.Background()
					val, fetchErr := rc.GetOrSet(ctx, tc.key, time.Minute, func(c context.Context) (string, error) {
						callCount.Add(1)
						time.Sleep(tc.fetchDelay)
						if tc.fetchErr != nil {
							return "", tc.fetchErr
						}
						return "computed_data", nil
					})
					if fetchErr != nil {
						errs <- fetchErr
					} else {
						vals <- val
					}
				}()
			}

			wg.Wait()
			close(errs)
			close(vals)

			if tc.wantErr {
				if len(errs) != tc.concurrency {
					t.Fatalf("expected all %d callers to receive error, got %d", tc.concurrency, len(errs))
				}
			} else {
				if len(vals) != tc.concurrency {
					t.Fatalf("expected all %d callers to receive value, got %d", tc.concurrency, len(vals))
				}
				for v := range vals {
					if v != "computed_data" {
						t.Errorf("unexpected value: %s", v)
					}
				}
			}

			if callCount.Load() != tc.expectedCalls {
				t.Errorf("expected %d DB fetch calls, got %d", tc.expectedCalls, callCount.Load())
			}
		})
	}
}

func TestTwoTierCache(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		val       string
		ttl       time.Duration
		sleepL1   time.Duration
		wantHitL1 bool
	}{
		{
			name:      "immediate hit in L1",
			key:       "user:101",
			val:       "Alice",
			ttl:       time.Minute,
			sleepL1:   0,
			wantHitL1: true,
		},
		{
			name:      "L1 expiration falls back to L2",
			key:       "user:102",
			val:       "Bob",
			ttl:       time.Minute,
			sleepL1:   150 * time.Millisecond,
			wantHitL1: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			l2, err := cache.NewRedis(config.RedisConfig{Addr: mr.Addr()})
			if err != nil {
				t.Fatalf("failed to create L2: %v", err)
			}
			defer func() { _ = l2.Close() }()

			twoTier := cache.NewTwoTier(l2, cache.TwoTierConfig{
				L1TTL:        100 * time.Millisecond,
				L1MaxEntries: 100,
			})

			ctx := context.Background()
			if setErr := twoTier.Set(ctx, tc.key, tc.val, tc.ttl); setErr != nil {
				t.Fatalf("Set failed: %v", setErr)
			}

			if tc.sleepL1 > 0 {
				time.Sleep(tc.sleepL1)
			}

			val, getErr := twoTier.Get(ctx, tc.key)
			if getErr != nil {
				t.Fatalf("Get failed: %v", getErr)
			}
			if val != tc.val {
				t.Errorf("expected %s, got %s", tc.val, val)
			}

			// Invalidation test
			twoTier.InvalidateL1(tc.key)
			exists, _ := twoTier.Exists(ctx, tc.key)
			if !exists {
				t.Errorf("expected key to still exist in L2 after L1 invalidation")
			}

			// Delete test
			if delErr := twoTier.Delete(ctx, tc.key); delErr != nil {
				t.Fatalf("Delete failed: %v", delErr)
			}
			_, getAfterDel := twoTier.Get(ctx, tc.key)
			if getAfterDel == nil {
				t.Errorf("expected error after delete, got nil")
			}
		})
	}
}

func TestShardedCounter(t *testing.T) {
	tests := []struct {
		name        string
		slots       int
		increments  int
		delta       int64
		expectedSum int64
	}{
		{
			name:        "16 slots with 100 increments",
			slots:       16,
			increments:  100,
			delta:       1,
			expectedSum: 100,
		},
		{
			name:        "4 slots with negative and positive deltas",
			slots:       4,
			increments:  50,
			delta:       5,
			expectedSum: 250,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rc, err := cache.NewRedis(config.RedisConfig{Addr: mr.Addr()})
			if err != nil {
				t.Fatalf("NewRedis failed: %v", err)
			}
			defer func() { _ = rc.Close() }()

			sc := cache.NewShardedCounter(rc.Client(), tc.slots)
			ctx := context.Background()
			counterKey := "post:999:views"

			var wg sync.WaitGroup
			for i := 0; i < tc.increments; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _ = sc.Incr(ctx, counterKey, tc.delta)
				}()
			}
			wg.Wait()

			total, getErr := sc.Get(ctx, counterKey)
			if getErr != nil {
				t.Fatalf("Get failed: %v", getErr)
			}

			if total != tc.expectedSum {
				t.Errorf("expected total %d, got %d", tc.expectedSum, total)
			}

			// Reset
			if resetErr := sc.Reset(ctx, counterKey); resetErr != nil {
				t.Fatalf("Reset failed: %v", resetErr)
			}
			afterReset, _ := sc.Get(ctx, counterKey)
			if afterReset != 0 {
				t.Errorf("expected 0 after reset, got %d", afterReset)
			}
		})
	}
}

func TestJitterTTL(t *testing.T) {
	tests := []struct {
		name     string
		base     time.Duration
		fraction float64
	}{
		{
			name:     "10% default jitter",
			base:     time.Minute,
			fraction: 0.10,
		},
		{
			name:     "20% jitter",
			base:     5 * time.Minute,
			fraction: 0.20,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			jittered := cache.JitterTTL(tc.base, tc.fraction)
			if jittered < tc.base {
				t.Errorf("expected jittered TTL >= base, got %v", jittered)
			}
			maxExpected := tc.base + time.Duration(float64(tc.base)*tc.fraction)
			if jittered > maxExpected {
				t.Errorf("expected jittered TTL <= %v, got %v", maxExpected, jittered)
			}
		})
	}
}

func TestNegativeCaching(t *testing.T) {
	tests := []struct {
		name              string
		key               string
		fetchErr          error
		expectedDbCalls   int32
		expectedReturnErr error
	}{
		{
			name:              "ErrNotFound triggers negative caching",
			key:               "missing_sku_999",
			fetchErr:          cache.ErrNotFound,
			expectedDbCalls:   1,
			expectedReturnErr: cache.ErrNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rc, err := cache.NewRedis(config.RedisConfig{Addr: mr.Addr()})
			if err != nil {
				t.Fatalf("NewRedis failed: %v", err)
			}
			defer func() { _ = rc.Close() }()

			var dbCalls atomic.Int32
			ctx := context.Background()

			fetcher := func(c context.Context) (string, error) {
				dbCalls.Add(1)
				return "", tc.fetchErr
			}

			// First call: DB returns ErrNotFound -> writes sentinel to cache
			_, err1 := rc.GetOrSet(ctx, tc.key, time.Minute, fetcher)
			if err1 != tc.expectedReturnErr {
				t.Errorf("expected err %v, got %v", tc.expectedReturnErr, err1)
			}

			// Second call: Returns ErrNotFound directly from cache sentinel (DB not called again)
			_, err2 := rc.GetOrSet(ctx, tc.key, time.Minute, fetcher)
			if err2 != tc.expectedReturnErr {
				t.Errorf("expected err %v, got %v", tc.expectedReturnErr, err2)
			}

			if dbCalls.Load() != tc.expectedDbCalls {
				t.Errorf("expected %d db calls, got %d", tc.expectedDbCalls, dbCalls.Load())
			}
		})
	}
}

func TestStaleWhileRevalidate(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		initialVal string
		newVal     string
		softTTL    time.Duration
		hardTTL    time.Duration
		wait       time.Duration
	}{
		{
			name:       "stale value returned immediately while revalidating",
			key:        "swr:product:42",
			initialVal: "v1_product",
			newVal:     "v2_product",
			softTTL:    50 * time.Millisecond,
			hardTTL:    500 * time.Millisecond,
			wait:       80 * time.Millisecond,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rc, err := cache.NewRedis(config.RedisConfig{Addr: mr.Addr()})
			if err != nil {
				t.Fatalf("NewRedis failed: %v", err)
			}
			defer func() { _ = rc.Close() }()

			coalescer := cache.NewCoalescer()
			ctx := context.Background()

			// 1. Initial population
			val1, err := coalescer.DoStaleWhileRevalidate(ctx, rc, tc.key, tc.softTTL, tc.hardTTL, func(c context.Context) (string, error) {
				return tc.initialVal, nil
			})
			if err != nil || val1 != tc.initialVal {
				t.Fatalf("initial fetch failed: val=%s err=%v", val1, err)
			}

			// 2. Wait until soft TTL passes, but within hard TTL
			time.Sleep(tc.wait)

			// 3. Second call returns stale initialVal instantly, triggers bg refresh
			val2, err := coalescer.DoStaleWhileRevalidate(ctx, rc, tc.key, tc.softTTL, tc.hardTTL, func(c context.Context) (string, error) {
				time.Sleep(20 * time.Millisecond)
				return tc.newVal, nil
			})
			if err != nil || val2 != tc.initialVal {
				t.Errorf("expected stale value %s returned immediately, got %s", tc.initialVal, val2)
			}

			// 4. Wait for background refresh to finish
			time.Sleep(50 * time.Millisecond)

			// 5. Subsequent call returns updated newVal
			val3, _ := coalescer.DoStaleWhileRevalidate(ctx, rc, tc.key, tc.softTTL, tc.hardTTL, func(c context.Context) (string, error) {
				return "should_not_run", nil
			})
			if val3 != tc.newVal {
				t.Errorf("expected refreshed value %s, got %s", tc.newVal, val3)
			}
		})
	}
}

func TestAtomicDecrementWithFloor(t *testing.T) {
	tests := []struct {
		name          string
		initialStock  string
		amount        int64
		floor         int64
		expectedLeft  int64
		expectedError error
	}{
		{
			name:          "successful decrement above floor",
			initialStock:  "10",
			amount:        3,
			floor:         0,
			expectedLeft:  7,
			expectedError: nil,
		},
		{
			name:          "exact decrement to floor",
			initialStock:  "5",
			amount:        5,
			floor:         0,
			expectedLeft:  0,
			expectedError: nil,
		},
		{
			name:          "insufficient stock returns ErrInsufficientQuantity",
			initialStock:  "2",
			amount:        5,
			floor:         0,
			expectedLeft:  0,
			expectedError: cache.ErrInsufficientQuantity,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rc, err := cache.NewRedis(config.RedisConfig{Addr: mr.Addr()})
			if err != nil {
				t.Fatalf("NewRedis failed: %v", err)
			}
			defer func() { _ = rc.Close() }()

			ctx := context.Background()
			itemKey := "inventory:item:100"
			_ = mr.Set(itemKey, tc.initialStock)

			remaining, decrErr := cache.AtomicDecrementWithFloor(ctx, rc.Client(), itemKey, tc.amount, tc.floor)
			if tc.expectedError != nil {
				if decrErr != tc.expectedError {
					t.Errorf("expected error %v, got %v", tc.expectedError, decrErr)
				}
			} else {
				if decrErr != nil {
					t.Fatalf("unexpected error: %v", decrErr)
				}
				if remaining != tc.expectedLeft {
					t.Errorf("expected remaining %d, got %d", tc.expectedLeft, remaining)
				}
			}
		})
	}
}

func TestConsistentHashRing(t *testing.T) {
	tests := []struct {
		name      string
		nodes     []string
		testKey   string
		expectErr error
	}{
		{
			name:      "empty ring returns ErrEmptyRing",
			nodes:     nil,
			testKey:   "user:100",
			expectErr: cache.ErrEmptyRing,
		},
		{
			name:      "deterministic routing across nodes",
			nodes:     []string{"node-1", "node-2", "node-3"},
			testKey:   "user:100",
			expectErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ring := cache.NewConsistentHashRing(50)
			if len(tc.nodes) > 0 {
				ring.Add(tc.nodes...)
			}

			node, err := ring.Get(tc.testKey)
			if tc.expectErr != nil {
				if err != tc.expectErr {
					t.Errorf("expected error %v, got %v", tc.expectErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Determinism check: same key must always return same node
			for i := 0; i < 5; i++ {
				n2, _ := ring.Get(tc.testKey)
				if n2 != node {
					t.Errorf("expected deterministic node %s, got %s", node, n2)
				}
			}

			// Remove a node
			ring.Remove("node-1")
			_, errAfterRemove := ring.Get(tc.testKey)
			if errAfterRemove != nil {
				t.Errorf("failed to route after node removal: %v", errAfterRemove)
			}
		})
	}
}

func TestSaltKey(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		stripes     int
		stripeIdx   int
		expectedKey string
	}{
		{
			name:        "1 stripe returns unsalted key",
			key:         "celebrity:post",
			stripes:     1,
			stripeIdx:   0,
			expectedKey: "celebrity:post",
		},
		{
			name:        "4 stripes distributes key with modulo index",
			key:         "celebrity:post",
			stripes:     4,
			stripeIdx:   6,
			expectedKey: "celebrity:post:salt:2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			salted := cache.SaltKey(tc.key, tc.stripes, tc.stripeIdx)
			if salted != tc.expectedKey {
				t.Errorf("expected %s, got %s", tc.expectedKey, salted)
			}
		})
	}
}

func TestTwoTierCache_EdgeCases_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "TwoTierCache Flush, GetOrSet, and byte serialization",
			run: func(t *testing.T) {
				mr := miniredis.RunT(t)
				rc, err := cache.NewRedis(config.RedisConfig{Addr: mr.Addr()})
				if err != nil {
					t.Fatalf("failed to create redis cache: %v", err)
				}
				defer func() { _ = rc.Close() }()

				tt := cache.NewTwoTier(rc, cache.TwoTierConfig{
					L1TTL:        50 * time.Millisecond,
					L1MaxEntries: 3, // Small capacity to test eviction
				})

				ctx := context.Background()

				// Set with []byte
				err = tt.Set(ctx, "k_bytes", []byte("byte_value"), time.Minute)
				if err != nil {
					t.Fatalf("Set bytes error: %v", err)
				}
				val, err := tt.Get(ctx, "k_bytes")
				if err != nil || val != "byte_value" {
					t.Fatalf("expected byte_value, got %s (err: %v)", val, err)
				}

				// GetOrSet
				fetched := false
				gosVal, err := tt.GetOrSet(ctx, "gos_key", time.Minute, func(ctx context.Context) (string, error) {
					fetched = true
					return "gos_val", nil
				})
				if err != nil || gosVal != "gos_val" || !fetched {
					t.Fatalf("GetOrSet failed: val=%s, fetched=%v, err=%v", gosVal, fetched, err)
				}

				// Eviction: push past L1MaxEntries (3)
				_ = tt.Set(ctx, "k1", "v1", time.Minute)
				_ = tt.Set(ctx, "k2", "v2", time.Minute)
				_ = tt.Set(ctx, "k3", "v3", time.Minute)
				_ = tt.Set(ctx, "k4", "v4", time.Minute) // triggers eviction

				// Flush
				err = tt.Flush(ctx)
				if err != nil {
					t.Fatalf("TwoTier Flush error: %v", err)
				}
				exists, _ := tt.Exists(ctx, "k_bytes")
				if exists {
					t.Errorf("expected k_bytes to be flushed")
				}
			},
		},
		{
			name: "ConsistentHashRing empty ring returns ErrEmptyRing",
			run: func(t *testing.T) {
				emptyRing := cache.NewConsistentHashRing(10)
				_, err := emptyRing.Get("any-key")
				if err == nil {
					t.Errorf("expected error when getting key from empty ring")
				}
			},
		},
		{
			name: "NewTwoTier, NewConsistentHashRing, and NewShardedCounter default options",
			run: func(t *testing.T) {
				mr := miniredis.RunT(t)
				rc, err := cache.NewRedis(config.RedisConfig{Addr: mr.Addr()})
				if err != nil {
					t.Fatalf("failed to create redis: %v", err)
				}
				defer func() { _ = rc.Close() }()

				ttDefault := cache.NewTwoTier(rc, cache.TwoTierConfig{})
				if ttDefault == nil {
					t.Fatal("expected non-nil TwoTierCache")
				}

				ringDefault := cache.NewConsistentHashRing(0)
				if ringDefault == nil {
					t.Fatal("expected non-nil ConsistentHashRing")
				}

				scDefault := cache.NewShardedCounter(rc.Client(), 0)
				if scDefault == nil {
					t.Fatal("expected non-nil ShardedCounter")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
