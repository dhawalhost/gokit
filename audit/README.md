# Audit (`gokit/audit`)

`audit` provides a compliance-ready, structured audit logging framework designed for SOC 2, HIPAA, ISO 27001, and GDPR requirements. It records immutable security events with automatic context enrichment, sensitive data sanitization (PII, tokens, credentials), and non-blocking asynchronous sink pipelines.

---

## Features

- **Structured Compliance Event Model**:
  - **Actor**: User ID, email, name, role, client IP, user agent.
  - **Action**: Hierarchical event name (e.g. `billing.invoice.void`, `iam.user.deprovision`).
  - **Resource**: Type, ID, name, and tenant boundary.
  - **Changes**: Pre-mutation vs. post-mutation diff capture (`From` vs. `To`).
  - **Status & Metadata**: Outcome (`success`, `failure`, `denied`), duration, error details, and custom metadata attributes.
- **Automated PII & Secret Redaction**:
  - Automatically identifies and masks sensitive keys (passwords, tokens, API keys, secret credentials, SSNs, credit cards).
- **Asynchronous, Non-Blocking Sinks**:
  - `AsyncSink`: Buffered worker pool preventing audit logging from slowing down HTTP request processing.
  - `LoggerSink`: Serializes events to structured JSON via Zap or slog.
  - `MultiSink`: Multiplexes events across multiple backends (e.g. cloud storage + SIEM + local log stream).
- **HTTP Audit Middleware**: Automatically captures incoming mutating requests (`POST`, `PUT`, `PATCH`, `DELETE`) with route, tenant, actor, and response status.

---

## Quick Start

### 1. Initializing the Recorder

```go
package main

import (
    "context"

    "github.com/dhawalhost/gokit/audit"
    "go.uber.org/zap"
)

func InitAuditor(logger *zap.Logger) (*audit.Recorder, func()) {
    // 1. Create a logger sink
    baseSink := audit.NewLoggerSink(logger)

    // 2. Wrap in an asynchronous buffered worker pool
    asyncSink := audit.NewAsyncSink(baseSink, audit.AsyncSinkConfig{
        BufferSize: 4096, // capacity before backpressure
        BatchSize:  50,   // flush batch size
        Workers:    2,    // background worker count
    })

    recorder := audit.NewRecorder(asyncSink)

    cleanup := func() {
        _ = asyncSink.Close() // Drains pending audit events before exit
    }

    return recorder, cleanup
}
```

---

### 2. Recording Audit Events (Fluent API)

```go
func OnSubscriptionUpgraded(ctx context.Context, recorder *audit.Recorder) error {
    return recorder.NewEvent("billing.subscription.upgrade").
        WithActorUser("usr-99", "admin@acme.corp", "Alice Smith", "admin").
        WithResource("subscription", "sub-101", "Enterprise Plan").
        WithTenant("tenant-acme").
        WithChange("tier", "pro", "enterprise").
        WithChange("seat_count", 10, 50).
        WithMetadata("stripe_customer_id", "cus_N7xK").
        Record(ctx) // Automatically injects X-Request-ID, client IP, and traceparent
}
```

---

### 3. HTTP Audit Middleware

```go
import (
    "github.com/dhawalhost/gokit/audit"
    "github.com/go-chi/chi/v5"
)

r := chi.NewRouter()

// Automatically audit all mutating API calls
r.Use(audit.Middleware(recorder, audit.MiddlewareConfig{
    IncludePaths: []string{"/api/v1/*"},
    ExcludePaths: []string{"/health/*", "/metrics"},
}))
```

---

## Configuration Reference

### `AsyncSinkConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `BufferSize` | `int` | `2048` | Maximum queued events in memory |
| `BatchSize` | `int` | `50` | Number of events flushed per sink call |
| `Workers` | `int` | `2` | Number of parallel background consumer goroutines |
| `FlushInterval` | `time.Duration` | `100ms` | Maximum duration before flushing partial batches |
