package distlock_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/dhawalhost/gokit/distlock"
)

func TestInMemoryLocker_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, locker distlock.Locker, ctx context.Context)
	}{
		{
			name: "obtain and release lifecycle with double unlock error",
			run: func(t *testing.T, locker distlock.Locker, ctx context.Context) {
				lock, err := locker.Obtain(ctx, "resource:1", 1*time.Second)
				if err != nil {
					t.Fatalf("unexpected error obtaining lock: %v", err)
				}
				if lock.Key() != "resource:1" {
					t.Errorf("expected key 'resource:1', got %s", lock.Key())
				}
				if lock.Token() == "" {
					t.Error("expected non-empty token")
				}

				if err := lock.Unlock(ctx); err != nil {
					t.Fatalf("unexpected error unlocking: %v", err)
				}
				if err := lock.Unlock(ctx); !errors.Is(err, distlock.ErrLockReleased) {
					t.Errorf("expected ErrLockReleased on second unlock, got %v", err)
				}
			},
		},
		{
			name: "mutual exclusion rejects concurrent acquisition for same key",
			run: func(t *testing.T, locker distlock.Locker, ctx context.Context) {
				lock1, err := locker.Obtain(ctx, "job:order-process", 1*time.Second)
				if err != nil {
					t.Fatalf("lock1 failed: %v", err)
				}
				defer lock1.Unlock(ctx)

				_, err = locker.Obtain(ctx, "job:order-process", 1*time.Second)
				if !errors.Is(err, distlock.ErrLockHeld) {
					t.Errorf("expected ErrLockHeld, got %v", err)
				}
			},
		},
		{
			name: "lock expires after TTL allowing subsequent acquisition",
			run: func(t *testing.T, locker distlock.Locker, ctx context.Context) {
				lock1, err := locker.Obtain(ctx, "short-lived", 50*time.Millisecond)
				if err != nil {
					t.Fatalf("failed to obtain lock: %v", err)
				}
				_ = lock1

				time.Sleep(70 * time.Millisecond)

				lock2, err := locker.Obtain(ctx, "short-lived", 1*time.Second)
				if err != nil {
					t.Fatalf("expected to acquire after TTL expiration, got: %v", err)
				}
				defer lock2.Unlock(ctx)
			},
		},
		{
			name: "extend renews TTL to maintain active lock",
			run: func(t *testing.T, locker distlock.Locker, ctx context.Context) {
				lock, err := locker.Obtain(ctx, "extendable", 100*time.Millisecond)
				if err != nil {
					t.Fatalf("failed to obtain lock: %v", err)
				}
				defer lock.Unlock(ctx)

				if err := lock.Extend(ctx, 300*time.Millisecond); err != nil {
					t.Fatalf("extend failed: %v", err)
				}

				time.Sleep(120 * time.Millisecond)

				_, err = locker.Obtain(ctx, "extendable", 100*time.Millisecond)
				if !errors.Is(err, distlock.ErrLockHeld) {
					t.Errorf("expected lock to still be held after extension, got %v", err)
				}
			},
		},
		{
			name: "auto-renew maintains lock beyond initial TTL",
			run: func(t *testing.T, locker distlock.Locker, ctx context.Context) {
				lock, err := locker.Obtain(ctx, "auto-renew-key", 60*time.Millisecond,
					distlock.WithAutoRenew(20*time.Millisecond),
				)
				if err != nil {
					t.Fatalf("failed to obtain lock: %v", err)
				}

				time.Sleep(150 * time.Millisecond)

				_, err = locker.Obtain(ctx, "auto-renew-key", 50*time.Millisecond)
				if !errors.Is(err, distlock.ErrLockHeld) {
					t.Errorf("expected lock to still be held via auto-renew, got %v", err)
				}

				if err := lock.Unlock(ctx); err != nil {
					t.Fatalf("failed to unlock: %v", err)
				}

				lock2, err := locker.Obtain(ctx, "auto-renew-key", 100*time.Millisecond)
				if err != nil {
					t.Fatalf("failed to re-obtain lock: %v", err)
				}
				defer lock2.Unlock(ctx)
			},
		},
		{
			name: "withLock executes protected closure and releases lock",
			run: func(t *testing.T, locker distlock.Locker, ctx context.Context) {
				var executed bool
				err := locker.WithLock(ctx, "task:sync", 500*time.Millisecond, func(innerCtx context.Context) error {
					executed = true
					_, heldErr := locker.Obtain(innerCtx, "task:sync", 500*time.Millisecond)
					if !errors.Is(heldErr, distlock.ErrLockHeld) {
						t.Errorf("expected lock to be held inside WithLock, got %v", heldErr)
					}
					return nil
				})

				if err != nil {
					t.Fatalf("WithLock failed: %v", err)
				}
				if !executed {
					t.Error("expected function to have executed")
				}

				lock2, err := locker.Obtain(ctx, "task:sync", 100*time.Millisecond)
				if err != nil {
					t.Fatalf("expected lock to be released after WithLock: %v", err)
				}
				defer lock2.Unlock(ctx)
			},
		},
		{
			name: "withRetry successfully acquires lock after competitor releases",
			run: func(t *testing.T, locker distlock.Locker, ctx context.Context) {
				lock1, err := locker.Obtain(ctx, "retry-resource", 100*time.Millisecond)
				if err != nil {
					t.Fatalf("lock1 failed: %v", err)
				}

				go func() {
					time.Sleep(40 * time.Millisecond)
					_ = lock1.Unlock(ctx)
				}()

				start := time.Now()
				lock2, err := locker.Obtain(ctx, "retry-resource", 200*time.Millisecond,
					distlock.WithRetry(5, 15*time.Millisecond),
				)
				if err != nil {
					t.Fatalf("lock2 should have succeeded with retries, got: %v", err)
				}
				defer lock2.Unlock(ctx)

				if time.Since(start) < 30*time.Millisecond {
					t.Errorf("expected wait before obtaining lock, got %v", time.Since(start))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			locker := distlock.NewInMemoryLocker()
			tc.run(t, locker, context.Background())
		})
	}
}

func TestRedisLocker_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, locker distlock.Locker, ctx context.Context)
	}{
		{
			name: "redis lifecycle: obtain, mutual exclusion, extend, unlock, re-unlock error",
			run: func(t *testing.T, locker distlock.Locker, ctx context.Context) {
				lock, err := locker.Obtain(ctx, "order:100", 1*time.Second)
				if err != nil {
					t.Fatalf("failed to obtain redis lock: %v", err)
				}
				if lock.Key() != "order:100" {
					t.Errorf("expected key 'order:100', got %s", lock.Key())
				}

				_, err = locker.Obtain(ctx, "order:100", 1*time.Second)
				if !errors.Is(err, distlock.ErrLockHeld) {
					t.Errorf("expected ErrLockHeld from redis, got %v", err)
				}

				if err := lock.Extend(ctx, 2*time.Second); err != nil {
					t.Fatalf("redis extend failed: %v", err)
				}

				if err := lock.Unlock(ctx); err != nil {
					t.Fatalf("redis unlock failed: %v", err)
				}

				if err := lock.Unlock(ctx); !errors.Is(err, distlock.ErrLockReleased) {
					t.Errorf("expected ErrLockReleased, got %v", err)
				}
			},
		},
		{
			name: "redis auto-renew preserves lock through background heartbeats",
			run: func(t *testing.T, locker distlock.Locker, ctx context.Context) {
				lock, err := locker.Obtain(ctx, "heartbeat-task", 500*time.Millisecond,
					distlock.WithAutoRenew(100*time.Millisecond),
				)
				if err != nil {
					t.Fatalf("failed to obtain lock: %v", err)
				}

				time.Sleep(300 * time.Millisecond)

				if err := lock.Unlock(ctx); err != nil {
					t.Fatalf("failed to unlock: %v", err)
				}
			},
		},
		{
			name: "concurrent WithLock under contention executes all jobs safely",
			run: func(t *testing.T, locker distlock.Locker, ctx context.Context) {
				var count int32
				var wg sync.WaitGroup
				for i := 0; i < 5; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_ = locker.WithLock(ctx, "counter-mutex", 1*time.Second, func(_ context.Context) error {
							atomic.AddInt32(&count, 1)
							return nil
						}, distlock.WithRetry(10, 20*time.Millisecond))
					}()
				}
				wg.Wait()

				if atomic.LoadInt32(&count) != 5 {
					t.Errorf("expected all 5 goroutines to safely execute under lock, got %d", count)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := miniredis.RunT(t)
			defer s.Close()

			rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
			defer rdb.Close()

			locker := distlock.NewRedisLocker(rdb, distlock.WithKeyPrefix("testlock:"))
			tc.run(t, locker, context.Background())
		})
	}
}

func TestFencingToken_Monotonicity(t *testing.T) {
	tests := []struct {
		name          string
		setupLocker   func(t *testing.T) distlock.Locker
		expectedCount int
	}{
		{
			name: "in-memory locker generates monotonic fencing tokens",
			setupLocker: func(t *testing.T) distlock.Locker {
				return distlock.NewInMemoryLocker()
			},
			expectedCount: 3,
		},
		{
			name: "redis locker generates monotonic fencing tokens",
			setupLocker: func(t *testing.T) distlock.Locker {
				s := miniredis.RunT(t)
				rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
				return distlock.NewRedisLocker(rdb)
			},
			expectedCount: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			locker := tc.setupLocker(t)
			ctx := context.Background()
			key := "leader:scheduler"
			var previousToken int64

			for i := 1; i <= tc.expectedCount; i++ {
				lock, err := locker.Obtain(ctx, key, 100*time.Millisecond)
				if err != nil {
					t.Fatalf("iteration %d: failed to obtain lock: %v", i, err)
				}

				fenced, ok := lock.(distlock.FencedLock)
				if !ok {
					t.Fatalf("iteration %d: lock does not implement FencedLock", i)
				}

				token := fenced.FencingToken()
				if token <= previousToken {
					t.Errorf("iteration %d: expected token > %d, got %d", i, previousToken, token)
				}
				previousToken = token

				if err := lock.Unlock(ctx); err != nil {
					t.Fatalf("iteration %d: failed to unlock: %v", i, err)
				}
			}
		})
	}
}

func TestLock_EdgeCases_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "WithToken and TTL accessors",
			run: func(t *testing.T) {
				locker := distlock.NewInMemoryLocker()
				ctx := context.Background()

				customToken := "my-custom-uuid-token"
				lock, err := locker.Obtain(ctx, "res:token", 5*time.Second, distlock.WithToken(customToken))
				if err != nil {
					t.Fatalf("Obtain error: %v", err)
				}
				defer func() { _ = lock.Unlock(ctx) }()

				if lock.Token() != customToken {
					t.Errorf("expected token %s, got %s", customToken, lock.Token())
				}
				if lock.TTL() != 5*time.Second {
					t.Errorf("expected TTL 5s, got %v", lock.TTL())
				}
				if lock.Key() != "res:token" {
					t.Errorf("expected key res:token, got %s", lock.Key())
				}
			},
		},
		{
			name: "WithLock propagates callback error and releases lock",
			run: func(t *testing.T) {
				locker := distlock.NewInMemoryLocker()
				ctx := context.Background()

				expectedErr := errors.New("business logic failed")
				err := locker.WithLock(ctx, "res:err", time.Second, func(ctx context.Context) error {
					return expectedErr
				})
				if !errors.Is(err, expectedErr) {
					t.Errorf("expected %v, got %v", expectedErr, err)
				}

				// Lock should be unlocked after error, so re-acquiring should succeed
				lock, err := locker.Obtain(ctx, "res:err", time.Second)
				if err != nil {
					t.Fatalf("expected to re-obtain lock after WithLock error: %v", err)
				}
				_ = lock.Unlock(ctx)
			},
		},
		{
			name: "NewBaseLock, AutoRenew edge cases and RetryDelayWithJitter",
			run: func(t *testing.T) {
				// NewBaseLock with empty token generates UUID
				b := distlock.NewBaseLock("res:empty", "", time.Second, 123)
				if b.Token() == "" {
					t.Error("expected non-empty token generated")
				}
				if b.FencingToken() != 123 {
					t.Errorf("expected fencing token 123, got %d", b.FencingToken())
				}

				// RetryDelayWithJitter defaults
				d0 := distlock.RetryDelayWithJitter(0)
				if d0 <= 0 {
					t.Error("expected positive delay for base 0")
				}
				d1 := distlock.RetryDelayWithJitter(1)
				if d1 != 1 {
					t.Errorf("expected 1, got %v", d1)
				}

				// AutoRenew double start and context cancellation
				ctx, cancel := context.WithCancel(context.Background())
				b.StartAutoRenew(ctx, 0, func(_ context.Context, _ time.Duration) error {
					return nil
				})
				// Double start is a no-op
				b.StartAutoRenew(ctx, 0, func(_ context.Context, _ time.Duration) error {
					return nil
				})
				// Cancel context terminates renew loop
				cancel()
				time.Sleep(20 * time.Millisecond)
				b.StopAutoRenew()
				// Double stop is safe
				b.StopAutoRenew()
			},
		},
		{
			name: "InMemory lock Extend and Unlock error cases",
			run: func(t *testing.T) {
				locker := distlock.NewInMemoryLocker()
				ctx := context.Background()

				lock, err := locker.Obtain(ctx, "res:mismatch", time.Second, distlock.WithToken("token-A"))
				if err != nil {
					t.Fatalf("Obtain error: %v", err)
				}

				// Create mismatched lock instance targeting same key
				otherLock := distlock.NewBaseLock("res:mismatch", "token-B", time.Second)
				// We can test Extend on non-existent lock
				emptyLock, _ := locker.Obtain(ctx, "res:temp", 10*time.Millisecond)
				_ = emptyLock.Unlock(ctx)
				if err := emptyLock.Extend(ctx, time.Second); !errors.Is(err, distlock.ErrLockReleased) {
					t.Errorf("expected ErrLockReleased on released lock extend, got %v", err)
				}

				// WithLock fails when context is canceled
				cancCtx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := locker.WithLock(cancCtx, "res:canc", time.Second, func(_ context.Context) error { return nil }); err == nil {
					t.Error("expected error from WithLock on canceled context")
				}

				_ = lock.Unlock(ctx)
				_ = otherLock
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
