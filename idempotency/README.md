# Idempotency (`gokit/idempotency`)

`idempotency` provides HTTP middleware implementing IETF-compliant request deduplication for mutating operations (`POST`, `PUT`, `PATCH`). It guarantees that duplicate API requests resulting from network timeouts or aggressive client retries execute exactly once without producing duplicate side-effects.

---

## Features

- **SHA-256 Request Fingerprinting**:
  - Hashes the request path, method, and body.
  - Rejects attempts to reuse an existing idempotency key with a mismatched payload with `422 Unprocessable Entity`.
- **In-Flight Concurrency Guard**:
  - Detects concurrent identical requests presenting the same key and returns `409 Conflict` to prevent race conditions.
- **Transparent Response Replay**:
  - Caches status codes, response headers, and bodies.
  - Subsequent duplicate requests receive the cached response immediately with the header:
    ```http
    Idempotent-Replayed: true
    ```
- **Pluggable Storage Engines**:
  - `RedisStore`: High-performance distributed storage with atomic Redis scripts and key TTLs.
  - `MemoryStore`: Thread-safe in-memory cache for local development and testing.

---

## Quick Start

```go
package main

import (
    "time"

    "github.com/dhawalhost/gokit/idempotency"
    "github.com/go-chi/chi/v5"
    "github.com/redis/go-redis/v9"
)

func main() {
    r := chi.NewRouter()

    // 1. Choose storage engine
    store := idempotency.NewRedisStore(redisClient, 24*time.Hour)

    // 2. Register middleware on mutation routes
    r.Route("/api/v1/payments", func(r chi.Router) {
        r.Use(idempotency.Middleware(idempotency.Config{
            Store:      store,
            HeaderName: "Idempotency-Key", // default: Idempotency-Key
            TTL:        24 * time.Hour,
        }))

        r.Post("/charge", chargeHandler)
    })
}
```

---

## HTTP Status Behavior

| Scenario | HTTP Status Code | Response Behavior |
|---|---|---|
| **First execution** | `200` / `201` / etc. | Executes handler, caches response in store |
| **Identical retry** | Original Status | Replays cached response with `Idempotent-Replayed: true` |
| **Concurrent in-flight** | `409 Conflict` | Returned immediately while original request is still processing |
| **Payload mismatch** | `422 Unprocessable Entity` | Returned if the same key is sent with different request body |
