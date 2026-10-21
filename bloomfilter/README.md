# Bloom Filter (`gokit/bloomfilter`)

`bloomfilter` provides space-efficient probabilistic data structures for fast set-membership checks with zero false negatives and mathematically bounded false positive rates. It includes both high-performance in-memory implementations and distributed Redis-backed filters.

---

## Use Cases

- **Cache Penetration Defense**: Reject queries for non-existent IDs before hitting the primary database.
- **Deduplication**: Fast check for already processed event IDs, email addresses, or URLs.
- **Recommendation Systems**: Filter out items already viewed by a user.

---

## Quick Start

### 1. In-Memory Filter

```go
package main

import (
    "fmt"

    "github.com/dhawalhost/gokit/bloomfilter"
)

func main() {
    // Expected items: 100,000; Target false-positive rate: 1% (0.01)
    filter, err := bloomfilter.New(100_000, 0.01)
    if err != nil {
        panic(err)
    }

    filter.AddString("user:123")
    filter.AddString("user:456")

    fmt.Println(filter.ContainsString("user:123")) // true
    fmt.Println(filter.ContainsString("user:999")) // false (or <=1% chance of true)

    // Inspect stats
    fmt.Printf("Bit count: %d, Estimated FPR: %.4f\n", filter.Count(), filter.EstimatedFalsePositiveRate())
}
```

---

### 2. Distributed Redis-Backed Filter

```go
import (
    "context"
    "time"

    "github.com/dhawalhost/gokit/bloomfilter"
)

func SetupRedisBloom(ctx context.Context, redisClient redis.UniversalClient) {
    store, _ := bloomfilter.NewRedisStore(redisClient, "bf:registered-emails", 500_000, 0.005)

    // Add string
    _ = store.AddString(ctx, "alice@example.com")

    // Check membership
    exists, _ := store.ContainsString(ctx, "alice@example.com") // true

    // Optional expiration window (e.g. for hourly deduplication)
    _ = store.Expire(ctx, 24*time.Hour)
}
```
