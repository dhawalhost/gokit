# batcher

`batcher` provides high-throughput micro-batching and request-coalescing utilities for Go microservices. It includes an asynchronous write batching buffer (`Batcher[T]`) for high-volume database inserts, audit logs, and message queues, as well as an idiomatic DataLoader pattern (`DataLoader[K, V]`) to eliminate the N+1 queries problem across APIs and services.

---

## Architecture Overview

### 1. Asynchronous Write Batcher (`Batcher[T]`)

```
Concurrent Goroutines
  ┌──────────┐  ┌──────────┐  ┌──────────┐
  │ Submit() │  │ Submit() │  │ Submit() │
  └────┬─────┘  └────┬─────┘  └────┬─────┘
       │             │             │
       ▼             ▼             ▼
  ┌──────────────────────────────────────┐
  │          Input Queue Channel         │  (QueueSize & Backpressure: Block/Drop/Reject)
  └──────────────────┬───────────────────┘
                     │
                     ▼
          ┌──────────────────────┐
          │     Accumulator      │  (Flushes when batch size reaches MaxBatchSize
          │      Goroutine       │   or ticker exceeds FlushInterval)
          └──────────┬───────────┘
                     │
                     ▼
  ┌──────────────────────────────────────┐
  │            Worker Pool               │
  │  ┌────────────┐      ┌────────────┐  │  (Executes BatchFunc concurrently
  │  │  Worker 1  │      │  Worker 2  │  │   with retries and panic recovery)
  │  └────────────┘      └────────────┘  │
  └──────────────────────────────────────┘
```

### 2. Request-Response DataLoader (`DataLoader[K, V]`)

```
Concurrent Callers
  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐
  │ Load(ctx, k1) │  │ Load(ctx, k2) │  │ Load(ctx, k1) │
  └───────┬───────┘  └───────┬───────┘  └───────┬───────┘
          │                  │                  │
          ▼                  ▼                  ▼
  ┌─────────────────────────────────────────────────────┐
  │              In-Memory Memoization Cache            │  (Cache Hit: Returns immediately)
  └──────────────────────────┬──────────────────────────┘
                             │ (Cache Miss)
                             ▼
  ┌─────────────────────────────────────────────────────┐
  │             Coalescing Batch Window                 │  (Window: 5ms or 100 keys)
  └──────────────────────────┬──────────────────────────┘
                             │
                             ▼
  ┌─────────────────────────────────────────────────────┐
  │      BatchLoadFunc: 1 Single Roundtrip Call         │
  └──────────────────────────┬──────────────────────────┘
                             │
                             ▼
            Dispatches results to individual callers
```

---

## Features

- **Generic & Type-Safe**: Completely generic implementation (`Batcher[T any]` and `DataLoader[K comparable, V any]`) requiring zero type assertions.
- **Dual Triggers**: Flushes batches automatically when reaching `MaxBatchSize` or when `FlushInterval` expires.
- **Configurable Backpressure**:
  - `BackpressureBlock`: Blocks caller until buffer space becomes available.
  - `BackpressureReject`: Fails fast returning `ErrQueueFull`.
  - `BackpressureDropOldest`: Evicts oldest pending item to admit new traffic during surges.
- **Fault-Tolerant Worker Pool**: Dispatches batches across concurrent workers with exponential retries and panic isolation.
- **Zero-Drop Graceful Shutdown**: `Shutdown(ctx)` and `Stop()` drain all buffered items through the worker pipeline before terminating.
- **N+1 DataLoader with Memoization**: Coalesces concurrent lookup keys into a single batch call with optional per-loader caching, `LoadMany`, and cache invalidation primitives (`Prime`, `Clear`, `ClearAll`).

---

## Installation

```bash
go get github.com/dhawalhost/gokit/batcher
```

---

## Quickstart

### 1. High-Throughput Write Batcher

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dhawalhost/gokit/batcher"
)

type AuditLog struct {
	Action    string
	UserID    string
	Timestamp time.Time
}

func main() {
	// Initialize batcher with max 100 items or 50ms interval
	b := batcher.New(func(ctx context.Context, batch []AuditLog) error {
		fmt.Printf("Persisting bulk batch of %d audit logs to database...\n", len(batch))
		return nil
	},
		batcher.WithMaxBatchSize(100),
		batcher.WithFlushInterval(50*time.Millisecond),
		batcher.WithWorkers(4),
		batcher.WithQueueSize(1000),
		batcher.WithBackpressure(batcher.BackpressureBlock),
		batcher.WithRetries(3, 10*time.Millisecond),
	)
	defer b.Stop()

	// Submit logs concurrently
	ctx := context.Background()
	for i := 0; i < 250; i++ {
		_ = b.Submit(ctx, AuditLog{
			Action:    "user.login",
			UserID:    fmt.Sprintf("user-%d", i),
			Timestamp: time.Now(),
		})
	}

	// Wait for workers to drain or trigger explicit manual flush
	_ = b.Flush(ctx)

	stats := b.Stats()
	fmt.Printf("Stats: Submitted=%d, Flushed=%d, Batches=%d\n",
		stats.Submitted, stats.Flushed, stats.Batches)
}
```

---

### 2. Solving N+1 Queries with `DataLoader`

```go
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dhawalhost/gokit/batcher"
)

type User struct {
	ID   string
	Name string
}

func main() {
	ctx := context.Background()

	// Batch function fetching users by IDs in 1 SQL query: SELECT * FROM users WHERE id IN (?)
	userLoader := batcher.NewDataLoader(func(ctx context.Context, ids []string) (map[string]User, error) {
		fmt.Printf("--> Database query for %d user IDs: %v\n", len(ids), ids)
		results := make(map[string]User)
		for _, id := range ids {
			results[id] = User{ID: id, Name: "User " + id}
		}
		return results, nil
	},
		batcher.WithDataLoaderWait(10*time.Millisecond),
		batcher.WithDataLoaderBatchSize(50),
		batcher.WithDataLoaderCache(true),
	)
	defer userLoader.Close()

	// 10 concurrent requests for users (some duplicates)
	userIDs := []string{"u1", "u2", "u3", "u1", "u2", "u4"}
	var wg sync.WaitGroup
	wg.Add(len(userIDs))

	for _, id := range userIDs {
		go func(uid string) {
			defer wg.Done()
			user, err := userLoader.Load(ctx, uid)
			if err != nil {
				log.Printf("error loading user %s: %v", uid, err)
				return
			}
			fmt.Printf("Loaded: %s (%s)\n", user.Name, user.ID)
		}(id)
	}

	wg.Wait()
	// Output: All 6 requests were coalesced into 1 database query!
}
```
