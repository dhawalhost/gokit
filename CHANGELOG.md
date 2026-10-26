# Changelog

All notable changes to `gokit` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [v0.9.0] - 2026-10-26

### Added

- **`eventstore`**: Append-only event sourcing engine with optimistic concurrency control, stream snapshots, and CQRS projection catch-up subscriptions.
- **`session`**: Enterprise HTTP session manager with anti-hijacking client fingerprinting (`/24` IPv4 and `/48` IPv6 subnet masking), sliding TTLs, flash message data, and AES-256-GCM encrypted cookie stores.
- Dedicated release notes: [releases/v0.9.0.md](releases/v0.9.0.md).

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
