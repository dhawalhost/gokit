# Middleware (`gokit/middleware`)

`middleware` provides an enterprise-ready collection of HTTP middlewares adhering to standard `func(http.Handler) http.Handler` signatures for security, traffic shaping, authentication, and context enrichment.

---

## Available Middlewares

| Middleware | Description |
|---|---|
| `RequestID()` | Injects or propagates a sanitized `X-Request-ID` into request context and response headers |
| `Logger(log)` | Logs HTTP method, path, status, latency, and client IP using structured Zap logging |
| `Recovery(log)` | Recovers from unexpected panics, prevents server crash, and returns structured 500 error |
| `SecureHeaders()` | Injects defense-in-depth headers (`HSTS`, `CSP`, `X-Frame-Options`, `X-Content-Type-Options`, `Permissions-Policy`) |
| `CORS(origins)` | Configures Cross-Origin Resource Sharing with pre-flight caching |
| `Timeout(d)` | Enforces per-request execution deadlines via `context.WithTimeout` |
| `RateLimit(cfg)` | Shapes request traffic using sliding window or token bucket backends |
| `JWT(cfg)` | Validates incoming Bearer JWT tokens and sets claims in request context |
| `APIKey(cfg)` | Validates static or dynamic API keys passed via headers or query strings |
| `TenantID()` | Extracts tenant ID and injects into request context |
| `Idempotency(cfg)` | Protects mutating routes against duplicate executions using Redis / memory stores |

---

## Quick Start

```go
package main

import (
    "time"

    "github.com/dhawalhost/gokit/middleware"
    "github.com/go-chi/chi/v5"
    "go.uber.org/zap"
)

func SetupRouter(log *zap.Logger) *chi.Mux {
    r := chi.NewRouter()

    // Global middleware stack
    r.Use(middleware.RequestID())
    r.Use(middleware.Logger(log))
    r.Use(middleware.Recovery(log))
    r.Use(middleware.SecureHeaders())
    r.Use(middleware.Timeout(30 * time.Second))
    r.Use(middleware.CORS([]string{"https://app.example.com"}))

    return r
}
```
