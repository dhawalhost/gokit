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

func TestInMemoryLocker_BasicLockUnlock(t *testing.T) {
	locker := distlock.NewInMemoryLocker()
	ctx := context.Background()

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

	// Releasing lock
	if err := lock.Unlock(ctx); err != nil {
		t.Fatalf("unexpected error unlocking: %v", err)
	}

	// Releasing again should return ErrLockReleased
	if err := lock.Unlock(ctx); !errors.Is(err, distlock.ErrLockReleased) {
		t.Errorf("expected ErrLockReleased, got %v", err)
	}
}

func TestInMemoryLocker_MutualExclusion(t *testing.T) {
	locker := distlock.NewInMemoryLocker()
	ctx := context.Background()

	lock1, err := locker.Obtain(ctx, "job:order-process", 1*time.Second)
	if err != nil {
		t.Fatalf("lock1 failed: %v", err)
	}
	defer lock1.Unlock(ctx)

	// Second acquisition for the same key must fail
	_, err = locker.Obtain(ctx, "job:order-process", 1*time.Second)
	if !errors.Is(err, distlock.ErrLockHeld) {
		t.Errorf("expected ErrLockHeld, got %v", err)
	}
}

func TestInMemoryLocker_Expiration(t *testing.T) {
	locker := distlock.NewInMemoryLocker()
	ctx := context.Background()

	// Obtain with very short TTL
	lock1, err := locker.Obtain(ctx, "short-lived", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to obtain lock: %v", err)
	}
	_ = lock1

	// Wait for TTL to pass
	time.Sleep(70 * time.Millisecond)

	// Second attempt should now succeed
	lock2, err := locker.Obtain(ctx, "short-lived", 1*time.Second)
	if err != nil {
		t.Fatalf("expected to acquire after TTL expiration, got: %v", err)
	}
	defer lock2.Unlock(ctx)
}

func TestInMemoryLocker_Extend(t *testing.T) {
	locker := distlock.NewInMemoryLocker()
	ctx := context.Background()

	lock, err := locker.Obtain(ctx, "extendable", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to obtain lock: %v", err)
	}
	defer lock.Unlock(ctx)

	// Extend before expiry
	if err := lock.Extend(ctx, 300*time.Millisecond); err != nil {
		t.Fatalf("extend failed: %v", err)
	}

	// Wait past original 100ms
	time.Sleep(120 * time.Millisecond)

	// Lock should still be held
	_, err = locker.Obtain(ctx, "extendable", 100*time.Millisecond)
	if !errors.Is(err, distlock.ErrLockHeld) {
		t.Errorf("expected lock to still be held after extension, got %v", err)
	}
}

func TestInMemoryLocker_AutoRenew(t *testing.T) {
	locker := distlock.NewInMemoryLocker()
	ctx := context.Background()

	// Lock with 60ms TTL, auto-renew every 20ms
	lock, err := locker.Obtain(ctx, "auto-renew-key", 60*time.Millisecond,
		distlock.WithAutoRenew(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("failed to obtain lock: %v", err)
	}

	// Sleep 150ms (> 2x TTL)
	time.Sleep(150 * time.Millisecond)

	// Lock should still be held thanks to auto-renew
	_, err = locker.Obtain(ctx, "auto-renew-key", 50*time.Millisecond)
	if !errors.Is(err, distlock.ErrLockHeld) {
		t.Errorf("expected lock to still be held via auto-renew, got %v", err)
	}

	// Unlock stops auto-renew cleanly
	if err := lock.Unlock(ctx); err != nil {
		t.Fatalf("failed to unlock: %v", err)
	}

	// Can now acquire immediately
	lock2, err := locker.Obtain(ctx, "auto-renew-key", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to re-obtain lock: %v", err)
	}
	defer lock2.Unlock(ctx)
}

func TestInMemoryLocker_WithLock(t *testing.T) {
	locker := distlock.NewInMemoryLocker()
	ctx := context.Background()
	var executed bool

	err := locker.WithLock(ctx, "task:sync", 500*time.Millisecond, func(innerCtx context.Context) error {
		executed = true
		// Verify lock is held during execution
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

	// Lock should have been automatically released
	lock2, err := locker.Obtain(ctx, "task:sync", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("expected lock to be released after WithLock: %v", err)
	}
	defer lock2.Unlock(ctx)
}

func TestInMemoryLocker_WithRetry(t *testing.T) {
	locker := distlock.NewInMemoryLocker()
	ctx := context.Background()

	// Process 1 holds lock for 40ms then unlocks
	lock1, err := locker.Obtain(ctx, "retry-resource", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("lock1 failed: %v", err)
	}

	go func() {
		time.Sleep(40 * time.Millisecond)
		_ = lock1.Unlock(ctx)
	}()

	// Process 2 attempts acquisition with retries
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
}

func TestRedisLocker_Integration(t *testing.T) {
	s := miniredis.RunT(t)
	defer s.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr: s.Addr(),
	})
	defer rdb.Close()

	locker := distlock.NewRedisLocker(rdb, distlock.WithKeyPrefix("testlock:"))
	ctx := context.Background()

	// 1. Basic Obtain and Unlock
	lock, err := locker.Obtain(ctx, "order:100", 1*time.Second)
	if err != nil {
		t.Fatalf("failed to obtain redis lock: %v", err)
	}
	if lock.Key() != "order:100" {
		t.Errorf("expected key 'order:100', got %s", lock.Key())
	}

	// 2. Mutual exclusion
	_, err = locker.Obtain(ctx, "order:100", 1*time.Second)
	if !errors.Is(err, distlock.ErrLockHeld) {
		t.Errorf("expected ErrLockHeld from redis, got %v", err)
	}

	// 3. Extend
	if err := lock.Extend(ctx, 2*time.Second); err != nil {
		t.Fatalf("redis extend failed: %v", err)
	}

	// 4. Unlock
	if err := lock.Unlock(ctx); err != nil {
		t.Fatalf("redis unlock failed: %v", err)
	}

	// 5. Unlocking again should fail
	if err := lock.Unlock(ctx); !errors.Is(err, distlock.ErrLockReleased) {
		t.Errorf("expected ErrLockReleased, got %v", err)
	}

	// 6. WithLock
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
}

func TestRedisLocker_AutoRenew(t *testing.T) {
	s := miniredis.RunT(t)
	defer s.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr: s.Addr(),
	})
	defer rdb.Close()

	locker := distlock.NewRedisLocker(rdb)
	ctx := context.Background()

	// Obtain with 500ms TTL, auto-renew every 100ms
	lock, err := locker.Obtain(ctx, "heartbeat-task", 500*time.Millisecond,
		distlock.WithAutoRenew(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("failed to obtain lock: %v", err)
	}

	// Run for 300ms (verifies renewals happen without errors)
	time.Sleep(300 * time.Millisecond)

	// Clean unlock
	if err := lock.Unlock(ctx); err != nil {
		t.Fatalf("failed to unlock: %v", err)
	}
}
