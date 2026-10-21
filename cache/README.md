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
