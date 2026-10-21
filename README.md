# gokit

[![Go Reference](https://pkg.go.dev/badge/github.com/dhawalhost/gokit.svg)](https://pkg.go.dev/github.com/dhawalhost/gokit)
[![Go Report Card](https://goreportcard.com/badge/github.com/dhawalhost/gokit)](https://goreportcard.com/report/github.com/dhawalhost/gokit)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://go.dev/)

**gokit** is the single-source-of-truth shared infrastructure library for all microservices in the `dhawalhost` ecosystem (leapmailr, wardseal, talentcurate, leverflag, and future services).


---

## Design Goals

- **Zero lock-in** — every abstraction is thin; escape hatches are always available.
- **Composable** — all middleware is `func(http.Handler) http.Handler`; all config is explicit function calls.
- **Production-ready** — graceful shutdown, circuit-breaking, structured logging, distributed tracing, Prometheus metrics, security hardened.
- **Type-safe** — Go generics used in `response` and `pagination` to eliminate type assertions.
- **Secure by default** — AES-256-GCM, JWT validation, SQL injection prevention, rate limiting, secure headers.

---

## Package Overview

| Package | Description |
|---|---|
| `server` | `HTTPServer` wrapping `net/http` with chi, graceful shutdown, optional TLS |
| `router` | Chi router constructors and `ServiceRouter` mount interface |
| `middleware` | 11 composable middlewares: RequestID, Logger, Recovery, CORS, JWT, APIKey, TenantID, RateLimit, Timeout, SecureHeaders, Idempotency |
| `idempotency` | IETF-compliant idempotency control with SHA-256 payload anti-collision, in-flight locking, and Redis/in-memory backends |
| `ratelimit` | `Store` interface with in-memory token-bucket and Redis sliding-window backends |
| `circuitbreaker` | Thread-safe closed/open/half-open circuit breaker |
| `logger` | Zap wrapper with automatic sensitive key masking (`********`), global singleton, and context helpers |
| `config` | Viper-backed config loader with hot-reloading (`NewManager`), secret auto-masking, `APP_*` env prefix, and YAML/.env support |
| `database` | GORM v2 + pgxpool combined `DB` struct with pool management and migration helpers |
| `cache` | `Cache` interface with Redis backend |
| `crypto` | AES-256-GCM, bcrypt, constant-time HMAC, JWT HS256/RS256, timing-safe PKCE, secure random |
| `errors` | Structured `AppError` with HTTP status codes, safe `Wrap` error encapsulation, and JSON writing |
| `response` | Generic `Response[T]` and `PaginatedResponse[T]` helpers |
| `validator` | go-playground/validator wrapper with JSON bind |
| `health` | Liveness and readiness HTTP handlers with named checkers |
| `observability` | Prometheus metrics with route pattern cardinality protection, OTLP tracing, and chi-compatible middlewares |
| `pagination` | Offset and cursor pagination parsed from HTTP requests |
| `idgen` | UUID v4/v7, ULID, NanoID generation |
| `bloomfilter` | In-memory and Redis-backed Bloom filter for probabilistic set membership |
| `client` | Resilient outbound HTTP client with jittered exponential backoff, circuit breaker integration, tracing, and body replay |
| `distlock` | Distributed locking primitives (Redis & In-Memory) with atomic Lua unlock/extend, auto-renewal keep-alive, and spin-retries |
| `audit` | SOC2/HIPAA-ready audit trail recording with Actor/Action/Resource models, PII masking, and async sinks |
| `outbox` | Transactional Outbox pattern with GORM, row-level locking (`SKIP LOCKED`), and background retry relay |
| `multitenancy` | Enterprise multi-tenant data isolation: PostgreSQL RLS session variables, GORM query scopes, and scoped cache keys |
| `authz` | Fine-grained RBAC/ABAC authorization enforcer, wildcard matching, and Chi middlewares |
| `crypto/kms` | Envelope encryption with per-record DEKs, AES-256-GCM, and master key provider interface |
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
r.Use(middleware.Idempotency(idempotency.Config{TTL: 24 * time.Hour}))
```

### `idempotency`

```go
// Memory-backed idempotency
r.Use(idempotency.Middleware(idempotency.Config{
    TTL: 24 * time.Hour,
}))

// Distributed Redis-backed idempotency
r.Use(idempotency.Middleware(idempotency.Config{
    Store: idempotency.NewCacheStore(redisCache),
    TTL:   24 * time.Hour,
}))
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

### `client`

Resilient outbound HTTP client with retry backoff, circuit-breaking, and distributed context propagation.

```go
httpClient := client.New(
    client.WithName("payment-service"),
    client.WithBaseURL("https://api.payments.internal"),
    client.WithTimeout(10 * time.Second),
    client.WithCircuitBreaker(cb),
    client.WithRetryPolicy(client.DefaultRetryPolicy()), // 3 retries, exponential backoff, full jitter, 5xx/429
    client.WithTracePropagation(true),                  // injects W3C tracecontext headers
    client.WithRequestIDPropagation(true),             // forwards X-Request-ID from context
)

var user User
resp, err := httpClient.PostJSON(ctx, "/v1/charge", chargeReq, &user)
```

### `distlock`

Production distributed locks with atomic Lua release/extend, heartbeat auto-renewal, and spin-retries.

```go
// Redis-backed distributed locker
locker := distlock.NewRedisLocker(redisClient)

// Execute critical section under coordinated distributed lock
err := locker.WithLock(ctx, "cron:nightly-billing", 30*time.Second, func(lockCtx context.Context) error {
    return runNightlyBilling(lockCtx)
}, 
    distlock.WithAutoRenew(10*time.Second),       // auto-renews lock until function finishes
    distlock.WithRetry(5, 50*time.Millisecond),  // retries with jitter if currently held
)
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

Supports standalone Redis, Redis Cluster, and Redis Sentinel out-of-the-box via `redis.UniversalClient`:

```go
// Standalone, Cluster, or Sentinel configuration via RedisConfig (Addr or Addrs + Cluster: true)
cache, err := cache.NewRedis(cfg.Redis)
defer cache.Close()

// Cluster-safe operations (automatic slot routing and cross-slot safe pipelined deletes)
cache.Set(ctx, "key", "value", time.Hour)
val, err := cache.Get(ctx, "key")
cache.Delete(ctx, "k1", "k2", "k3") // pipelined multi-key delete

// Access underlying redis.UniversalClient (usable in distlock, ratelimit, bloomfilter)
client := cache.Client()
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

### `bloomfilter`

```go
import "github.com/dhawalhost/gokit/bloomfilter"

// In-memory filter — single process, zero external deps.
f, err := bloomfilter.New(
    100_000, // expected distinct items
    0.01,    // 1 % false-positive rate
)
f.AddString("user:42")
f.ContainsString("user:42") // true
f.ContainsString("user:99") // false (or very rarely true)

// Count, inspect fill, and reset.
fmt.Println(f.Count(), f.OnesCount(), f.EstimatedFalsePositiveRate())
f.Reset()

// Persist / restore via binary encoding.
data, _ := f.MarshalBinary()
var f2 bloomfilter.Filter
f2.UnmarshalBinary(data)

// Redis-backed filter — shared across services / replicas.
store, err := bloomfilter.NewRedisStore(redisClient, "bf:emails", 100_000, 0.01)
store.AddString(ctx, "alice@example.com")
found, _ := store.ContainsString(ctx, "alice@example.com")

// Optional TTL (e.g. for deduplication windows).
store.Expire(ctx, 24*time.Hour)
store.Delete(ctx) // reset
```

**Tuning guide:**
- Lower `falsePositiveRate` → more bits / memory, fewer false positives.
- Higher `expectedItems` → larger bit array; accuracy is maintained.
- Use `EstimatedFalsePositiveRate()` at runtime to decide when to rebuild the filter.

### `audit`

Compliance-ready audit logging for SOC2, HIPAA, and ISO 27001 requirements with automatic context enrichment and PII masking:

```go
import "github.com/dhawalhost/gokit/audit"

// Setup recorder with async non-blocking sink or logger sink
sink := audit.NewAsyncSink(audit.NewLoggerSink(zapLogger), audit.AsyncSinkConfig{
    BufferSize: 2048,
    BatchSize:  50,
})
defer sink.Close()

recorder := audit.NewRecorder(sink)

// Fluent audit event recording
err := recorder.NewEvent("billing.subscription.upgrade").
    WithActorUser("usr-123", "alice@corp.com", "Alice Smith", "admin").
    WithResource("subscription", "sub-456", "Enterprise Tier").
    WithChange("plan", "starter", "enterprise").
    WithMetadata("payment_method", "pm_card_visa").
    Record(ctx) // automatically enriches tenant_id, request_id, client_ip
```

### `outbox`

Transactional Outbox pattern preventing the dual-write problem between database transactions and message brokers:

```go
import "github.com/dhawalhost/gokit/outbox"

// 1. Transactionally store business data and outbox event in the same DB commit
tx := db.GORM.Begin()
tx.Create(&order)
msg, err := outbox.Enqueue(ctx, tx, "orders.created", order.ID, payload, map[string]string{
    "traceparent": currentTraceHeader,
})
tx.Commit()

// 2. Background relay dispatches events to Kafka / RabbitMQ / SQS with retry backoffs
relay := outbox.NewRelay(outbox.NewGORMStore(db.GORM), publisher, outbox.RelayConfig{
    BatchSize:    100,
    PollInterval: 500 * time.Millisecond,
    MaxRetries:   5,
})
relay.Start(ctx)
defer relay.Stop()
```

### `multitenancy`

Tenant data isolation across databases (Postgres RLS session variables & GORM scopes) and Redis caches:

```go
import "github.com/dhawalhost/gokit/multitenancy"

// 1. Strict HTTP tenant enforcement middleware
r.Use(multitenancy.RequireTenant())

// 2. Scoped database queries (GORM scope or PostgreSQL RLS session injection)
db.GORM.Scopes(multitenancy.ScopeFromContext(ctx)).Find(&invoices)

err := multitenancy.WithTenantTx(ctx, db.GORM, tenantID, func(tx *gorm.DB) error {
    // Executes SET LOCAL app.current_tenant = tenantID
    return tx.Find(&invoices).Error
})

// 3. Tenant-isolated Redis cache (auto-prefixes "tenant:<tenant_id>:<key>")
scopedCache := multitenancy.NewContextScopedCache(redisCache)
scopedCache.Set(ctx, "session:1", userData, time.Hour) // isolated per tenant
```

### `authz`

Fine-grained Role-Based (RBAC) and Attribute-Based (ABAC) authorization enforcer with wildcard matching:

```go
import "github.com/dhawalhost/gokit/authz"

// 1. Configure role-to-permission mappings
roles := authz.NewRoleMap(map[string][]string{
    "admin":   {"*"},
    "finance": {"invoices:*", "reports:read"},
    "viewer":  {"*:read"},
})
enforcer := authz.NewEnforcer(authz.WithRoleMap(roles))

// 2. Protect routes with Chi middleware
r.With(authz.Require(enforcer, "invoices:create")).Post("/invoices", createInvoiceHandler)
r.With(authz.RequireRole(enforcer, "admin")).Delete("/invoices/{id}", deleteInvoiceHandler)

// 3. Programmatic checks
if err := enforcer.Enforce(ctx, "invoices:refund"); err != nil {
    // ErrForbidden or ErrUnauthorized
}
```

### `crypto/kms`

Envelope encryption pattern protecting sensitive data at rest using unique, ephemeral Data Encryption Keys (DEKs):

```go
import "github.com/dhawalhost/gokit/crypto/kms"

// 1. Configure KeyProvider (Local AES-256 for dev/self-hosted; AWS/GCP KMS/Vault in production)
provider, _ := kms.NewLocalKeyProvider(map[string][]byte{
    "primary-2026": masterKey32Bytes,
}, "primary-2026")
cipher := kms.NewEnvelopeCipher(provider)

// 2. Encrypt sensitive field with Additional Authenticated Data (AAD)
aad := []byte("tenant:acme_corp")
b64Envelope, err := cipher.EncryptString(ctx, "primary-2026", "secret-credit-card-number", aad)

// 3. Decrypt back to plaintext
cardNum, err := cipher.DecryptString(ctx, b64Envelope, aad)
```

---

## 📚 Examples & Documentation

### Comprehensive Examples

The `examples/` directory contains 6 complete, production-ready applications:

1. **[basic-server](examples/README.md#1-basic-server)**: Minimal HTTP server with health checks and metrics
2. **[database-crud](examples/README.md#2-database-crud)**: RESTful API with PostgreSQL, GORM, and pagination
3. **[auth-jwt](examples/README.md#3-jwt-authentication)**: JWT authentication with protected routes
4. **[cache-ratelimit](examples/README.md#4-cache--rate-limiting)**: Redis caching and IP-based rate limiting
5. **[circuit-breaker](examples/README.md#5-circuit-breaker)**: Fault-tolerant external service calls
6. **[enterprise-service](examples/README.md#6-enterprise-microservice-enterprise-service)**: Multi-tenancy, outbox transactions, audit logging, KMS envelope encryption, and RBAC authorization

See [examples/README.md](examples/README.md) for detailed usage instructions and testing commands.

### Additional Documentation

- **[SECURITY.md](SECURITY.md)**: Security policy, vulnerability reporting, and best practices
- **[CHANGELOG.md](CHANGELOG.md)**: Detailed version history and migration guides
- **[INTEGRATION_TESTS.md](INTEGRATION_TESTS.md)**: Integration testing guide with Docker setup
- **[RELEASE_v0.1.0.md](RELEASE_v0.1.0.md)**: Complete v0.1.0 release notes

### API Documentation

Full API documentation is available at [pkg.go.dev/github.com/dhawalhost/gokit](https://pkg.go.dev/github.com/dhawalhost/gokit)

---

## 🧪 Testing

### Unit Tests
```bash
make test          # Run all unit tests
make test-race     # Run with race detector
make cover         # Generate coverage report
```

### Integration Tests
```bash
# Start required services (PostgreSQL, Redis)
docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=testpass postgres:15-alpine
docker run -d -p 6379:6379 redis:7-alpine

# Run integration tests
export TEST_DATABASE_DSN="postgres://postgres:testpass@localhost:5432/postgres?sslmode=disable"
export TEST_REDIS_ADDR="localhost:6379"
make test-integration
```

See [INTEGRATION_TESTS.md](INTEGRATION_TESTS.md) for detailed setup instructions.

### Other Commands
```bash
make lint          # Run golangci-lint
make vet           # Run go vet
make fmt           # Format code
make build         # Build all packages
make vuln          # Check for vulnerabilities
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
