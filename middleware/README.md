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
| `Timeout(d)` | Cancels request context at deadline, buffers response writes under a mutex (`503 Service Unavailable` on timeout), and re-panics on the serving goroutine |
| `RateLimit(cfg)` | Shapes traffic via `ratelimit.Store`; supports `TrustedProxies` for right-to-left `X-Forwarded-For` parsing and fails open by default on store errors (`503` when fail-closed) |
| `JWT(cfg)` | Validates Bearer JWTs (HS256, RS256, `KeyFunc`/`NewJWKSKeyFunc`) with `Issuer`, `Audience`, and `Leeway` checks, storing claims in context |
| `APIKey(cfg)` | Validates static or dynamic API keys passed via headers or query strings |
| `TenantID(opts...)` | Resolves tenant ID, validating `X-Tenant-ID` against authenticated JWT claims (`403 Forbidden` on mismatch unless `WithTrustHeader(true)`) |
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

    // Protected routes with JWT validation (HS256, RS256, or cached JWKS + claim verification)
    r.Group(func(pr chi.Router) {
        pr.Use(middleware.JWT(middleware.JWTConfig{
            KeyFunc: middleware.NewJWKSKeyFunc(
                "https://auth.example.com/.well-known/jwks.json",
                middleware.WithJWKSCacheTTL(5*time.Minute),
            ),
            Algorithm: "RS256",
            Issuer:    "https://auth.example.com",
            Audience:  "billing-api",
            Leeway:    30 * time.Second,
        }))
        // Bind tenant context from authenticated JWT claims (returns 403 if X-Tenant-ID conflicts)
        pr.Use(middleware.TenantID())

        // Rate limit behind trusted load-balancer CIDRs (defaults to RemoteAddr otherwise)
        pr.Use(middleware.RateLimit(middleware.RateLimitConfig{
            Store:             rateLimitStore,
            RequestsPerSecond: 20,
            Burst:             40,
            TrustedProxies:    []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
            FailOpen:          middleware.Bool(true), // default true; set FailClosed: true for 503 on store errors
        }))

        pr.Get("/profile", func(w http.ResponseWriter, r *http.Request) {
            // Extract strongly-typed claims directly
            var claims struct {
                UserID string `json:"user_id"`
            }
            if ok := middleware.TypedClaimsFromContext(r.Context(), &claims); ok {
                // Access typed claims
            }
        })
    })

    return r
}
```

