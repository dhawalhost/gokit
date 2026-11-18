# Changelog

All notable changes to `gokit` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Changed

- **Module Architecture (Phase 4)**: Split `gokit` into a slim core module (`github.com/dhawalhost/gokit`) with zero AWS SDK, Kafka, RabbitMQ, pgx, or GORM dependencies, isolated adapter sub-modules (`database`, `outbox`, `storage/s3`, `eventbus/kafka`, `eventbus/rabbitmq`), an extended module (`github.com/dhawalhost/gokit/x` containing `bloomfilter`, `compression`, `diagnostics`, `eventstore`, `gateway`, `i18n`, `migration`, `notify`, `pipeline`, `saga`, `search`, `session`, and `workflow`), and standalone example/integration modules (`examples/database-crud`, `examples/enterprise-service`, `test/integration`).
- **Breaking (`pagination`)**: Decoupled `pagination` from `gorm.io/gorm`; replaced `(OffsetParams).Apply(*gorm.DB)` with generic `pagination.Apply(p, db)` and `(OffsetParams).Offset()`.
- **Breaking (`multitenancy`)**: Decoupled `multitenancy` from `gorm.io/gorm`; converted `Scope` and `ScopeFromContext` to generic query-builder functions and updated `SetRLS` / `WithTenantTx` to work cleanly with both `database/sql` handles and ORM handles without importing GORM.
- **Breaking (`storage/s3`)**: Moved `S3Storage` and `S3Config` out of `gokit/storage` into `github.com/dhawalhost/gokit/storage/s3` (preserving `NewS3`/`S3Config` and adding `New`/`Config`/`Storage` aliases).
- **Breaking (`eventbus`)**: Decoupled `eventbus.NewOutboxPublisher` from importing `gokit/outbox` directly; it now returns `*OutboxPublisherAdapter` directly, which satisfies `outbox.Publisher`.
- **Documentation & Tooling**: Clarified in-process vs. multi-node persistence semantics in `x/session`, `x/eventstore`, `x/workflow`, and `x/saga`; removed unverified `SOC2`/`HIPAA`/`ISO 27001`/`IETF-compliant` certification claims; updated `README.md`, `Makefile`, `.githooks/pre-commit`, and `.github/workflows/ci.yml` for multi-module verification.

---

## [v0.10.0] - 2026-11-18

### Added

- **`diagnostics`**: Runtime memory watchdog, automated watermark heap dumping, goroutine leak detector, and authenticated `pprof` router.
- **`pipeline`**: Concurrent stream ETL pipeline with bounded backpressure, tumbling/sliding temporal windows, fan-in/fan-out, and dead-letter queues.
- **`database`**: Added `DB.Migrate` integrating `gokit/migration` with `io/fs.FS` / `//go:embed` support.
- **`config` & `secrets`**: Added `config.Secret.ToSecret` and `UnmarshalText` / `UnmarshalJSON` on `secrets.Secret`.
- **`cache`**: Added `cache.AtomicLocker` (`SetNX`, `CompareAndDelete`) and `cache.PrefixClearer` (`ClearPrefix` via `SCAN` + `UNLINK`) interfaces implemented on `RedisCache`, `TwoTierCache`, `multitenancy.ScopedCache` (plus `ClearTenant`), and `multitenancy.ContextScopedCache` (`GK-01`, `GK-14`).
- **`middleware`**:
  - Added `NewTrustedIPKeyFunc(trustedProxies)` and `RateLimitConfig.TrustedProxies` for right-to-left `X-Forwarded-For` parsing behind trusted proxies (`GK-02`).
  - Added `RateLimitConfig.FailOpen`, `RateLimitConfig.FailClosed`, `RateLimitConfig.Logger`, `RateLimitConfig.OnStoreError`, `RateLimitStoreErrors()`, and `Bool()` (`GK-10`).
  - Added `JWTConfig.PublicKey` (`RS256`), `JWTConfig.KeyFunc`, `JWTConfig.Issuer`, `JWTConfig.Audience`, `JWTConfig.Leeway`, and `NewJWKSKeyFunc` with TTL caching and rate-limited refresh (`GK-17`).
- **`multitenancy`**: Added `TenantResolver`, `WithTenantResolver`, `WithTenantClaim`, `WithTrustHeader`, `WithClaimsMap`, `ClaimsMapFromContext`, and `ResolveRequestTenant` (`GK-03`).
- **`client`**: Added `RetryPolicy.RetryableMethods`, `RetryPolicy.RetryNonIdempotent`, and `WithRetryNonIdempotent` (`GK-06`).
- **`distlock`**: Added `Lock.Lost() <-chan struct{}` and `BaseLock.RenewErrors()` (`GK-07`).
- **`circuitbreaker`**: Added `Config.MaxHalfOpenRequests`, `Config.FailureRatio`, `Config.MinRequests`, `Config.RollingWindow`, and `ExecuteCtx` (`GK-13`).
- **`observability`**: Added `WithInsecure()`, `WithServiceVersion()`, and `WithDeploymentEnvironment()` tracer options plus `TelemetryConfig.ServiceVersion`, `Environment`, and `Insecure` (`GK-16`).
- **`outbox`**: Added `RelayConfig.Logger` and `Relay.Errors()` atomic error counter for background poll/cleanup loops.

### Changed

- **Toolchain, Dependencies & CI**: Upgraded Go toolchain to `1.26.11` and `github.com/klauspost/compress` to `v1.20.4`; updated `.github/workflows/ci.yml` to run `go test -race ./...` across all packages and enforce `go mod tidy -diff`.
- **`idempotency`**: Replaced non-atomic `Exists` + `Set` lock acquisition in `CacheStore` with atomic `SetNX` and compare-and-delete tokens (`GK-01`); enforced 1 MiB default body limit (`413`), tenant+subject key scoping, `503` on store errors, and skipped caching `>= 500` responses unless `Cache5xx` is enabled (`GK-12`).
- **`middleware` & `multitenancy`**:
  - Unified `middleware.TenantID` and `multitenancy` onto a single canonical tenant context key.
  - `IPKeyFunc` now defaults strictly to `r.RemoteAddr` without trusting client-supplied `X-Forwarded-For`/`X-Real-IP` headers (`GK-02`).
  - `TenantID` and `multitenancy.RequireTenant` validate tenant identity against authenticated JWT claims and return `403 Forbidden` on mismatch (`GK-03`).
  - `Timeout` now buffers headers/writes under a mutex, drops late writes (`http.ErrHandlerTimeout`), returns `503 Service Unavailable`, and re-panics on the serving goroutine (`GK-04`).
  - `RateLimit` defaults to fail-open on store errors and returns `503 Service Unavailable` when fail-closed (`GK-10`).
- **`client` & `webhook`**:
  - Kept `Timeout` and `AttemptTimeout` contexts alive until `resp.Body.Close()` (`GK-05`), and restricted automatic retries to idempotent methods (`GET`, `HEAD`, `OPTIONS`, `PUT`, `DELETE`) or requests carrying an `Idempotency-Key` header (`GK-06`).
  - Updated `webhook.Dispatcher` to attach a unique `Idempotency-Key` header per outbound webhook delivery so retries remain safe.
- **`distlock`**: `WithLock` enables auto-renewal and cancels the handler context on lock loss (`GK-07`); `RedisLocker.Obtain` acquires the lock and increments the fencing token atomically via Lua with `{key}` Redis Cluster hash tags and surfaces errors (`GK-08`).
- **`scheduler`**: Keyed distributed job locks per scheduled tick with auto-renewal, held per-tick locks until TTL expiry, and surfaced lock backend outages via `OnJobFailure` instead of skipping (`GK-09`).
- **`outbox`**: Ordered composite index `idx_outbox_scheduled` as `(status, scheduled_at)` with a separate `idx_outbox_topic` (`GK-11`).
- **`circuitbreaker`**: Capped half-open trial concurrency and invoked `OnStateChange` outside internal mutex locks (`GK-13`).
- **`cache`**: Removed `Flush(ctx)` from the `cache.Cache` interface in favor of prefix-scoped `ClearPrefix` (`GK-14`).
- **`ratelimit`**: Fixed `InMemoryStore` cleanup start/stop races with `sync.Once`, and switched `RedisStore` to constant-space GCRA using Redis server `TIME` (`GK-15`).
- **`observability`**: Defaulted `InitTracer` to TLS and `ParentBased(TraceIDRatioBased(0.1))` sampling while honoring `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_RESOURCE_ATTRIBUTES` (`GK-16`).
- **`pagination` & `response`**: Defined `response.Pagination` as a type alias (`= pagination.Pagination`) to eliminate circular package dependencies.

### Deprecated

- **`cache` & `multitenancy`**: `RedisCache.Flush`, `TwoTierCache.Flush`, `ScopedCache.Flush`, and `ContextScopedCache.Flush` are deprecated in favor of `ClearPrefix` / `ClearTenant` (`GK-14`).

### Removed

- **`database`**: Removed `database.RunMigrations` (replaced by `DB.Migrate` backed by `gokit/migration`).
- **`jobqueue`**: Removed duplicate `jobqueue` batcher implementation in favor of `gokit/batcher`.

---

## [v0.9.0] - 2026-10-26

### Added

- **`eventstore`**: Append-only event sourcing engine with optimistic concurrency control, stream snapshots, and CQRS projection catch-up subscriptions.
- **`session`**: Enterprise HTTP session manager with anti-hijacking client fingerprinting (`/24` IPv4 and `/48` IPv6 subnet masking), sliding TTLs, flash message data, and AES-256-GCM encrypted cookie stores.

---

## [v0.8.0] - 2026-10-26

### Added

- **`compression`**: Zero-allocation multi-codec HTTP middleware and streaming writers supporting Zstandard (`zstd`), Brotli (`br`), Gzip, and Deflate with recycled `sync.Pool` buffers.

---

## [v0.7.0] - 2026-10-26

### Added

- **`search`**: In-process full-text search engine with BM25 relevance ranking, configurable tokenizers, n-grams, fuzzy edit distance matching, and boolean query execution.

---

## [v0.6.0] - 2026-10-25

### Added

- **`eventbus/kafka`**: Native Apache Kafka & Redpanda streaming transport adapter for `eventbus`.
- **`eventbus/rabbitmq`**: Enterprise RabbitMQ AMQP 0-9-1 broker transport adapter with consumer groups and prefetch controls.

---

## [v0.5.0] - 2026-10-24

### Added

- **`secrets`**: Dynamic secret management with memory scrubbing (`Zero()`), fallback provider chains, and TTL caching.
- **`migration`**: Standalone schema migration engine with transactional DDL and `distlock` synchronization across replicas.

---

## [v0.4.0] - 2026-10-23

### Added

- **`gateway`**: Reverse proxy with active connection pooling, scatter-gather aggregator, and multi-algorithm load balancing.
- **`batcher`**: Write-buffering accumulator and DataLoader batching with TTL memoization.

---

## [v0.3.0] - 2026-10-22

### Added

- **`workflow`**: State machine (FSM) engine with transition guards, lifecycle callbacks, and optimistic versioning.
- **`i18n`**: Internationalization engine with BCP 47 locale negotiation and CLDR pluralization.

---

## [v0.2.0] - 2026-10-21

### Added

- **`multitenancy`**: PostgreSQL Row-Level Security (RLS) session integration and GORM query isolation scopes.
- **`crypto/kms`**: Envelope encryption with per-record DEKs and master key provider interface.
- **`scheduler`**: Distributed cron scheduler with leader election via `distlock`.

---

## [v0.1.0] - 2026-10-20

### Added

- Initial release of `gokit`:
  - Core server, router, middlewares, and logger.
  - Resilience primitives: `circuitbreaker`, `ratelimit`, `bulkhead`, `loadshed`.
  - Data and persistence: `database`, `cache`, `distlock`, `outbox`, `eventbus`, `saga`.
  - Security and utility: `crypto`, `authz`, `audit`, `idempotency`, `featureflag`, `webhook`, `notify`.
