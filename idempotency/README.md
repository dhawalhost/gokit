# Idempotency (`gokit/idempotency`)

`idempotency` provides HTTP middleware implementing IETF-compliant request deduplication for mutating operations (`POST`, `PUT`, `PATCH`). It guarantees that duplicate API requests resulting from network timeouts or aggressive client retries execute exactly once without producing duplicate side-effects.

---

## Features

- **SHA-256 Request Fingerprinting**:
  - Hashes the request path, method, and body.
  - Rejects attempts to reuse an existing idempotency key with a mismatched payload with `422 Unprocessable Entity`.
- **In-Flight Concurrency Guard**:
  - Acquires locks atomically via `SetNX` with UUID ownership tokens and compare-and-delete release (`CacheStore`).
  - Detects concurrent identical requests presenting the same key and returns `409 Conflict` to prevent race conditions.
- **Tenant & Caller Key Scoping**:
  - Keys default to `<tenant>:<subject>:<Idempotency-Key>` to prevent cross-tenant key collisions (customizable via `KeyFunc`).
- **Safety Limits & Failure Handling**:
  - Bounds request body fingerprinting via `MaxBodyBytes` (default `1 MiB`, returning `413 Request Entity Too Large` when exceeded).
  - Fails closed with `503 Service Unavailable` when the store backend errors instead of bypassing idempotency protection.
  - Skips caching `>= 500` server error responses by default (opt-in via `Cache5xx: true`).
- **Transparent Response Replay**:
  - Caches status codes, response headers, and bodies.
  - Subsequent duplicate requests receive the cached response immediately with the header:
    ```http
    Idempotent-Replayed: true
    ```
- **Pluggable Storage Engines**:
  - `CacheStore` (`NewCacheStore`): Distributed storage backed by any `cache.Cache` (e.g., `RedisCache`), using atomic `cache.AtomicLocker` (`SetNX` / `CompareAndDelete`) when available.
  - `InMemoryStore` (`NewInMemoryStore`): Thread-safe in-memory store for local development and testing.

---

## Quick Start

```go
package main

import (
    "time"

    "github.com/dhawalhost/gokit/cache"
    "github.com/dhawalhost/gokit/idempotency"
    "github.com/go-chi/chi/v5"
)

func main() {
    r := chi.NewRouter()

    // 1. Choose storage engine (e.g. cache.NewRedis(...) or idempotency.NewInMemoryStore())
    redisCache, _ := cache.NewRedis(cache.RedisConfig{Addr: "localhost:6379"})
    store := idempotency.NewCacheStore(redisCache)

    // 2. Register middleware on mutation routes
    r.Route("/api/v1/payments", func(r chi.Router) {
        r.Use(idempotency.Middleware(idempotency.Config{
            Store:        store,
            Header:       "Idempotency-Key", // default: Idempotency-Key
            TTL:          24 * time.Hour,
            MaxBodyBytes: 1 << 20, // default: 1 MiB
        }))

        r.Post("/charge", chargeHandler)
    })
}
```

---

## HTTP Status Behavior

| Scenario | HTTP Status Code | Response Behavior |
|---|---|---|
| **First execution** | `200` / `201` / etc. | Executes handler; caches non-5xx responses in store (or 5xx if `Cache5xx: true`) |
| **Identical retry** | Original Status | Replays cached response with `Idempotent-Replayed: true` |
| **Concurrent in-flight** | `409 Conflict` | Returned immediately while original request is still processing |
| **Payload mismatch** | `422 Unprocessable Entity` | Returned if the same key is sent with different request body |
| **Body exceeds `MaxBodyBytes`** | `413 Request Entity Too Large` | Returned when the request body exceeds the configured size limit |
| **Store unavailable** | `503 Service Unavailable` | Returned when the backing store fails on lookup, lock, or read |
