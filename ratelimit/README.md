# Rate Limit (`gokit/ratelimit`)

`ratelimit` provides high-performance rate limiting primitives and HTTP middleware to protect services from abuse, brute-force attacks, and noisy neighbor resource starvation. It supports both in-memory token bucket algorithms and distributed Redis GCRA (Generic Cell Rate Algorithm) limiters.

---

## Features

- **Distributed Redis GCRA (`RedisStore`)**:
  - Implements constant-space `O(1)` GCRA rate limiting in a Lua script using Redis server `TIME` so client clock skew never bypasses limits.
  - Supports standalone Redis, Sentinel, and Redis Cluster without unbounded key growth.
- **In-Memory Token Bucket (`InMemoryStore`)**:
  - Zero-dependency, thread-safe token bucket limiter powered by `golang.org/x/time/rate` with idempotent cleanup lifecycle (`sync.Once`).
- **Trusted Proxy & Fail-Open Middleware**:
  - Defaults `IPKeyFunc` to `r.RemoteAddr`, supports `TrustedProxies` for right-to-left `X-Forwarded-For` parsing, and fails open by default on store errors (or returns `503 Service Unavailable` when fail-closed).
- **Standard HTTP Headers**:
  - Injects `X-RateLimit-Limit` and `Retry-After` on `429 Too Many Requests`.

---

## Quick Start

### 1. Redis GCRA Rate Limiting

```go
package main

import (
    "net/netip"
    "time"

    "github.com/dhawalhost/gokit/middleware"
    "github.com/dhawalhost/gokit/ratelimit"
    "github.com/go-chi/chi/v5"
    "github.com/redis/go-redis/v9"
)

func main() {
    r := chi.NewRouter()

    // 1. Setup Redis GCRA store (windowDuration acts as fallback period when RequestsPerSecond == 0)
    store := ratelimit.NewRedisStore(redisClient, time.Minute)

    // 2. Attach rate limit middleware
    r.Use(middleware.RateLimit(middleware.RateLimitConfig{
        Store:             store,
        RequestsPerSecond: 20,
        Burst:             50,
        TrustedProxies:    []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
        FailOpen:          middleware.Bool(true), // default true; logs & counts store errors
    }))
}
```

---

### 2. In-Memory Token Bucket Limiting

```go
store := ratelimit.NewInMemoryStore()

r.Use(middleware.RateLimit(middleware.RateLimitConfig{
    Store:             store,
    RequestsPerSecond: 10,
    Burst:             20,
}))
```
