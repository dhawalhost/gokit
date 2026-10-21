# Rate Limit (`gokit/ratelimit`)

`ratelimit` provides high-performance rate limiting primitives and HTTP middleware to protect services from abuse, brute-force attacks, and noisy neighbor resource starvation. It supports both in-memory token bucket algorithms and distributed Redis sliding-window counters.

---

## Features

- **Distributed Redis Sliding Window (`RedisStore`)**:
  - Implements sliding window counters using Redis sorted sets (ZSET).
  - Accurately limits request bursts across distributed clusters without boundary reset anomalies.
- **In-Memory Token Bucket (`InMemoryStore`)**:
  - Zero-dependency, thread-safe token bucket limiter powered by `golang.org/x/time/rate`.
- **Flexible Key Extractors**:
  - Rate limit by client IP, authenticated user ID, API key, or tenant ID.
- **Standard HTTP Headers**:
  - Injects `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `Retry-After` on `429 Too Many Requests`.

---

## Quick Start

### 1. Redis Sliding-Window Rate Limiting

```go
package main

import (
    "net/http"
    "time"

    "github.com/dhawalhost/gokit/middleware"
    "github.com/dhawalhost/gokit/ratelimit"
    "github.com/go-chi/chi/v5"
    "github.com/redis/go-redis/v9"
)

func main() {
    r := chi.NewRouter()

    // 1. Setup Redis store (e.g. 100 requests per 1-minute window)
    store := ratelimit.NewRedisStore(redisClient, time.Minute)

    // 2. Attach rate limit middleware
    r.Use(middleware.RateLimit(middleware.RateLimitConfig{
        Store:             store,
        RequestsPerSecond: 20,
        Burst:             50,
        KeyFunc: func(r *http.Request) string {
            return middleware.IPKeyFunc(r) // Rate limit by client IP
        },
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
