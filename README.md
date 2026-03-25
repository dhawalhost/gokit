# gokit

[![Go Reference](https://pkg.go.dev/badge/github.com/dhawalhost/gokit.svg)](https://pkg.go.dev/github.com/dhawalhost/gokit)
[![Go Report Card](https://goreportcard.com/badge/github.com/dhawalhost/gokit)](https://goreportcard.com/report/github.com/dhawalhost/gokit)

**gokit** is the single-source-of-truth shared infrastructure library for all microservices in the `dhawalhost` ecosystem (leapmailr, wardseal, talentcurate, leverflag, and future services).

---

## Design Goals

- **Zero lock-in** — every abstraction is thin; escape hatches are always available.
- **Composable** — all middleware is `func(http.Handler) http.Handler`; all config is explicit function calls.
- **Production-ready** — graceful shutdown, circuit-breaking, structured logging, distributed tracing, Prometheus metrics.
- **Type-safe** — Go generics used in `response` and `pagination` to eliminate type assertions.

---

## Package Overview

| Package | Description |
|---|---|
| `server` | `HTTPServer` wrapping `net/http` with chi, graceful shutdown, optional TLS |
| `router` | Chi router constructors and `ServiceRouter` mount interface |
| `middleware` | 10 composable middlewares: RequestID, Logger, Recovery, CORS, JWT, APIKey, TenantID, RateLimit, Timeout, SecureHeaders |
| `ratelimit` | `Store` interface with in-memory token-bucket and Redis sliding-window backends |
| `circuitbreaker` | Thread-safe closed/open/half-open circuit breaker |
| `logger` | Zap wrapper with global singleton and context helpers |
| `config` | Viper-backed config loader with `APP_*` env prefix and YAML file support |
| `database` | GORM v2 + pgxpool combined `DB` struct with migration helpers |
| `cache` | `Cache` interface with Redis backend |
| `crypto` | AES-256-GCM, bcrypt, HMAC, JWT HS256/RS256, PKCE, secure random |
| `errors` | Structured `AppError` with HTTP status codes and JSON writing |
| `response` | Generic `Response[T]` and `PaginatedResponse[T]` helpers |
| `validator` | go-playground/validator wrapper with JSON bind |
| `health` | Liveness and readiness HTTP handlers with named checkers |
| `observability` | Prometheus metrics, OTLP tracing, and chi-compatible middlewares |
| `pagination` | Offset and cursor pagination parsed from HTTP requests |
| `idgen` | UUID v4/v7, ULID, NanoID generation |
| `testutil` | Test helpers: logger, recorder, request builder, response assertions |

---

## Installation

```bash
go get github.com/dhawalhost/gokit
```

---

## Quick Start

```go
package main

import (
    "context"
    "log"

    "github.com/dhawalhost/gokit/config"
    "github.com/dhawalhost/gokit/health"
    "github.com/dhawalhost/gokit/logger"
    "github.com/dhawalhost/gokit/middleware"
    "github.com/dhawalhost/gokit/observability"
    "github.com/dhawalhost/gokit/router"
    "github.com/dhawalhost/gokit/server"
)

func main() {
    cfg := config.MustLoad("")

    log, err := logger.New(cfg.Log.Level, cfg.Log.Development)
    if err != nil {
        log.Fatal("failed to create logger")
    }
    logger.SetGlobal(log)

    observability.InitMetrics("myservice")

    r := router.NewWithMiddleware(
        middleware.RequestID(),
        middleware.Logger(log),
        middleware.Recovery(log),
        middleware.SecureHeaders(),
    )

    h := health.NewHandler()
    r.Get("/health/live", h.LiveHandler())
    r.Get("/health/ready", h.ReadyHandler())
    r.Handle("/metrics", observability.MetricsHandler())

    srv := server.New(
        server.WithAddr(cfg.Server.Addr),
        server.WithReadTimeout(cfg.Server.ReadTimeout),
        server.WithWriteTimeout(cfg.Server.WriteTimeout),
    )
    srv.Mount("/", r)

    if err := srv.Run(context.Background()); err != nil {
        log.Fatal(err.Error())
    }
}
```

---

## Package Usage Examples

### `logger`

```go
log, err := logger.New("info", false)
logger.SetGlobal(log)

// Store/retrieve from context
ctx = logger.WithContext(ctx, log)
l := logger.FromContext(ctx)
```

### `config`

```go
cfg, err := config.Load("config.yaml") // or "" for env-only
cfg := config.MustLoad("")
```

### `errors`

```go
err := errors.NotFound("USER_NOT_FOUND", "user does not exist")
err = errors.WithDetails(err, map[string]string{"id": userID})

// In handler
errors.WriteError(w, r, err)
```

### `response`

```go
response.Ok(w, r, user)
response.Created(w, r, user)
response.NoContent(w)
response.Paginated(w, r, users, pagination)
```

### `middleware`

```go
r.Use(middleware.RequestID())
r.Use(middleware.Logger(log))
r.Use(middleware.Recovery(log))
r.Use(middleware.CORS([]string{"https://example.com"}))
r.Use(middleware.JWT(middleware.JWTConfig{SecretKey: []byte("secret")}))
r.Use(middleware.APIKey(middleware.APIKeyConfig{Validate: myValidator}))
r.Use(middleware.TenantID())
r.Use(middleware.RateLimit(middleware.RateLimitConfig{
    RequestsPerSecond: 10,
    Burst:             20,
    Store:             ratelimit.NewInMemoryStore(),
}))
r.Use(middleware.Timeout(30 * time.Second))
r.Use(middleware.SecureHeaders())
```

### `ratelimit`

```go
// In-memory
store := ratelimit.NewInMemoryStore()

// Redis sliding window
store := ratelimit.NewRedisStore(redisClient, time.Minute)
```

### `circuitbreaker`

```go
cb := circuitbreaker.New(circuitbreaker.Config{
    Name:         "external-api",
    MaxFailures:  5,
    ResetTimeout: 30 * time.Second,
    OnStateChange: func(name string, from, to circuitbreaker.State) {
        log.Printf("circuit %s: %v -> %v", name, from, to)
    },
})

err := cb.Execute(func() error {
    return callExternalAPI()
})
```

### `database`

```go
db, err := database.New(ctx, cfg.Database)
defer db.Close()

// GORM
db.GORM.Find(&users)

// Raw pgx
rows, err := db.Pool.Query(ctx, "SELECT id FROM users")

// Migrations
err = database.RunMigrations(ctx, cfg.Database.DSN, cfg.Database.MigrationsPath)
```

### `cache`

```go
cache, err := cache.NewRedis(cfg.Redis)
cache.Set(ctx, "key", "value", time.Hour)
val, err := cache.Get(ctx, "key")
```

### `crypto`

```go
// AES-256-GCM
ct, err := crypto.EncryptString("secret", key32bytes)
pt, err := crypto.DecryptString(ct, key32bytes)

// Passwords
hash, err := crypto.HashPassword("password")
err = crypto.CheckPassword("password", hash)

// JWT
token, err := crypto.SignHS256(claims, secret)
claims, err := crypto.VerifyHS256(token, secret)

// PKCE
verifier, _ := crypto.GenerateCodeVerifier()
challenge := crypto.GenerateCodeChallenge(verifier)
ok := crypto.VerifyCodeChallenge(verifier, challenge)
```

### `health`

```go
h := health.NewHandler()
h.Register("database", db)
h.Register("redis", redisCache)

r.Get("/health/live", h.LiveHandler())
r.Get("/health/ready", h.ReadyHandler())
```

### `observability`

```go
observability.InitMetrics("myservice")
r.Use(observability.Metrics())
r.Use(observability.Tracing("myservice"))
r.Handle("/metrics", observability.MetricsHandler())

shutdown, err := observability.InitTracer(ctx, cfg.Telemetry)
defer shutdown(ctx)
```

### `pagination`

```go
p := pagination.ParseOffsetParams(r)
var users []User
var total int64
db.GORM.Model(&User{}).Count(&total)
p.Apply(db.GORM).Find(&users)
response.Paginated(w, r, users, p.ToPagination(total))
```

### `idgen`

```go
id := idgen.NewUUID()
id := idgen.NewUUIDv7()
id := idgen.NewULID()
id, err := idgen.NewNanoID()
id := idgen.MustNanoID()
```

### `validator`

```go
v := validator.New()

type CreateUserReq struct {
    Email string `json:"email" validate:"required,email"`
    Name  string `json:"name"  validate:"required,min=2"`
}

var req CreateUserReq
if err := v.Bind(r, &req); err != nil {
    errors.WriteError(w, r, errors.BadRequest("INVALID_INPUT", err.Error()))
    return
}
```

---

## Configuration Reference

All settings can be provided via environment variables with the `APP_` prefix.
Nested keys use `_` as separator (e.g. `APP_SERVER_ADDR`).

| Environment Variable | Type | Default | Description |
|---|---|---|---|
| `APP_SERVER_ADDR` | string | `:8080` | HTTP listen address |
| `APP_SERVER_READ_TIMEOUT` | duration | `30s` | HTTP read timeout |
| `APP_SERVER_WRITE_TIMEOUT` | duration | `30s` | HTTP write timeout |
| `APP_SERVER_IDLE_TIMEOUT` | duration | `120s` | HTTP idle timeout |
| `APP_SERVER_SHUTDOWN_TIMEOUT` | duration | `30s` | Graceful shutdown timeout |
| `APP_DATABASE_DSN` | string | — | PostgreSQL DSN |
| `APP_DATABASE_MAX_OPEN_CONNS` | int | `25` | Max open DB connections |
| `APP_DATABASE_MAX_IDLE_CONNS` | int | `5` | Max idle DB connections |
| `APP_DATABASE_CONN_MAX_LIFETIME` | duration | `5m` | Connection max lifetime |
| `APP_DATABASE_MIGRATIONS_PATH` | string | — | Path to migration files |
| `APP_REDIS_ADDR` | string | — | Redis address (host:port) |
| `APP_REDIS_PASSWORD` | string | — | Redis password |
| `APP_REDIS_DB` | int | `0` | Redis DB index |
| `APP_JWT_SECRET` | string | — | JWT HMAC secret |
| `APP_JWT_EXPIRY` | duration | — | JWT token expiry |
| `APP_JWT_ISSUER` | string | — | JWT issuer claim |
| `APP_LOG_LEVEL` | string | `info` | Log level (debug/info/warn/error) |
| `APP_LOG_DEVELOPMENT` | bool | `false` | Enable development log mode |
| `APP_TELEMETRY_ENABLED` | bool | `false` | Enable OTLP tracing |
| `APP_TELEMETRY_OTLP_ENDPOINT` | string | — | OTLP collector endpoint |
| `APP_TELEMETRY_SERVICE_NAME` | string | — | Service name for traces |

---

## Contributing

1. Fork the repository and create a feature branch.
2. Follow the existing code style — `gofmt`, doc comments on all exports, errors wrapped with `fmt.Errorf("pkg: ...: %w", err)`.
3. Run `make vet fmt test` before opening a PR.
4. Ensure `go build ./...` and `go vet ./...` pass with zero warnings.
5. Add or update tests for any changed behaviour.
