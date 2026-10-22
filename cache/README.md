# Cache (`gokit/cache`)

`cache` provides high-performance caching abstractions built on top of `redis.UniversalClient`. It seamlessly supports standalone Redis, Redis Sentinel, and Redis Cluster deployments through uniform configuration without altering application code.

---

## Features

- **Universal Topology Routing**:
  - Automatically configures connection pools for single node, Sentinel failover pairs, or multi-shard Redis Clusters.
- **Cluster-Safe Batch Deletions**:
  - Automatically splits multi-key delete operations across hash slots using pipelining, avoiding cross-slot Redis Cluster errors.
- **Direct Client Access**:
  - Provides escape hatches via `.Client()` to access the underlying `redis.UniversalClient` for advanced operations (e.g., Lua scripts, distributed locks, bloom filters).

---

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/dhawalhost/gokit/cache"
)

func main() {
    ctx := context.Background()

    // 1. Initialize Redis Cache (Standalone, Sentinel, or Cluster)
    c, err := cache.NewRedis(cache.RedisConfig{
        Addr:     "localhost:6379", // Or Addrs: []string{"node1:6379", "node2:6379"} for cluster
        Password: "",
        DB:       0,
        Cluster:  false, // Set true to enable Redis Cluster mode
    })
    if err != nil {
        panic(err)
    }
    defer c.Close()

    // 2. Set, Get, Delete
    _ = c.Set(ctx, "session:usr-101", "active", 30*time.Minute)

    val, err := c.Get(ctx, "session:usr-101")
    if err == nil {
        fmt.Println("Session value:", val)
    }

    // Pipelined cluster-safe multi-key delete
    _ = c.Delete(ctx, "k1", "k2", "k3")
}
```

---

## 🛡️ Senior System Design Patterns

### 1. Cache Stampede Protection (`GetOrSet` with SingleFlight)
When a high-traffic key expires, thousands of concurrent requests can simultaneously miss cache and swamp the primary database (thundering herd). `GetOrSet` coalesces duplicate concurrent queries so only **one** request hits the source of truth:

```go
// Even if 10,000 goroutines call this at the same instant,
// the fetcher callback executes exactly ONCE.
val, err := c.GetOrSet(ctx, "catalog:product:42", 5*time.Minute, func(c context.Context) (string, error) {
    // Heavy DB query or microservice RPC
    productJSON := db.FetchProductFromDatabase(42)
    return productJSON, nil
})
```

---

### 2. Two-Tier Caching (L1 Local Memory + L2 Redis)
Absorbs hot key spikes (e.g. 5% of traffic hitting a single key) by maintaining a short-lived, size-bounded in-memory L1 cache in front of Redis L2. Requests for hot keys return in ~50 nanoseconds without network hops:

```go
twoTier := cache.NewTwoTier(c, cache.TwoTierConfig{
    L1TTL:        5 * time.Second, // Bounded short TTL prevents stale drift
    L1MaxEntries: 10000,          // Prevents memory exhaustion
})

// Fast L1 check -> fallback to Redis L2 -> coalesced DB fetch
val, err := twoTier.GetOrSet(ctx, "homepage:trending", time.Minute, func(c context.Context) (string, error) {
    return queryTrendingFromDB(), nil
})
```

---

### 3. Sharded Counter (Lock Contention Elimination)
A single counter row or key (`UPDATE counters SET count = count + 1`) suffers severe lock contention above ~2,000 writes/sec. `ShardedCounter` stripes increments across $N$ sub-keys and aggregates on read:

```go
counter := cache.NewShardedCounter(c.Client(), 16) // 16 stripes

// High-throughput writes pick random stripes: ~50k+ writes/sec
_, _ = counter.Incr(ctx, "video:9823:views", 1)

// Reads aggregate all stripes
totalViews, _ := counter.Get(ctx, "video:9823:views")
fmt.Println("Total views:", totalViews)
```

---

### 4. Stale-While-Revalidate (Zero-Latency Background Refresh)
Eliminates user-facing latency spikes when cache keys expire. If a key is past its soft TTL but within hard TTL, `DoStaleWhileRevalidate` returns the stale value immediately (0ms wait) and refreshes the cache asynchronously in the background via singleflight:

```go
val, err := coalescer.DoStaleWhileRevalidate(ctx, c, "sku:100", 1*time.Minute, 10*time.Minute, func(c context.Context) (string, error) {
    return fetchProductDetails(100), nil
})
```

---

### 5. Negative Caching (Tombstone Protection Against Scraping)
When a requested resource doesn't exist (`cache.ErrNotFound`), the coalescer writes a temporary sentinel tombstone with a short TTL (30s) so scraping attacks or missing IDs cannot punch through the cache to DDOS the database:

```go
val, err := c.GetOrSet(ctx, "user:missing_id", 5*time.Minute, func(c context.Context) (string, error) {
    user, found := db.Find(id)
    if !found {
        return "", cache.ErrNotFound // Automatically negative-cached!
    }
    return user.JSON(), nil
})
```

---

### 6. Atomic Flash Sale Inventory Decrement (`AtomicDecrementWithFloor`)
Executes an atomic Redis Lua script in 1 round-trip to decrement inventory or account balances without dropping below a floor (e.g. 0). Eliminates distributed locking overhead and prevents overselling under 50k writes/sec:

```go
// Decrements stock by 2, guaranteeing it never drops below 0
remaining, err := cache.AtomicDecrementWithFloor(ctx, c.Client(), "inventory:sku:101", 2, 0)
if errors.Is(err, cache.ErrInsufficientQuantity) {
    // Flash sale sold out!
    return ErrSoldOut
}
```

