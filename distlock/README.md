# Distributed Lock (`gokit/distlock`)

`distlock` provides distributed mutual exclusion primitives for coordinating scheduled tasks, worker leader elections, and critical section synchronization across microservice replicas. It supports Redis (via atomic Lua scripts) and thread-safe in-memory backends.

---

## Features

- **Safe Atomic Semantics**:
  - Employs atomic Lua scripts for lock acquisition, release, and lease extension.
  - Generates unique ownership tokens to guarantee that a worker never accidentally releases a lock acquired by another worker after expiration.
- **Heartbeat Lease Auto-Renewal (`WithAutoRenew`)**:
  - Automatically spins up a background goroutine to refresh the lease TTL while long-running jobs are executing, preventing premature lock forfeiture.
- **Spin-Retries with Backoff (`WithRetry`)**:
  - Retries lock acquisition with configurable attempts, backoff intervals, and jitter when a lock is currently contended.
- **Scoped Execution Guard (`WithLock`)**:
  - Automatically executes a closure under the lock and guarantees release upon function return or panic.

---

## Quick Start

### 1. Redis-Backed Distributed Lock

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/dhawalhost/gokit/distlock"
    "github.com/redis/go-redis/v9"
)

func RunDailyBilling(ctx context.Context, redisClient redis.UniversalClient) error {
    locker := distlock.NewRedisLocker(redisClient)

    // Run task exclusively across all service replicas
    return locker.WithLock(ctx, "job:nightly-billing", 30*time.Second, func(lockCtx context.Context) error {
        fmt.Println("Running billing processing exclusively...")
        time.Sleep(5 * time.Second)
        return nil
    },
        // Auto-renews lock lease every 10 seconds until the function returns
        distlock.WithAutoRenew(10*time.Second),
        // Retries 5 times with backoff if currently held by another replica
        distlock.WithRetry(5, 100*time.Millisecond),
    )
}
```

---

### 2. Manual Lock Acquisition and Release

```go
lock, err := locker.Acquire(ctx, "resource:invoice-101", 10*time.Second)
if err != nil {
    // Lock is held by another process
    return err
}
defer lock.Release(ctx)

// Manually extend lock if needed
_ = lock.Extend(ctx, 15*time.Second)
```

---

## Configuration Reference

| Option | Description |
|---|---|
| `WithAutoRenew(interval)` | Starts a background keep-alive heartbeater to extend the lock TTL until release |
| `WithRetry(attempts, delay)` | Retries acquiring the lock with jittered backoff if currently occupied |
