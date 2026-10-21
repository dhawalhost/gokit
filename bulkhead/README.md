# Bulkhead (`gokit/bulkhead`)

`bulkhead` implements the **Bulkhead Pattern** to isolate execution resources and prevent the "Noisy Neighbor" problem in multi-tenant environments. By enforcing concurrency limits and queue thresholds, it ensures that one tenant or degraded downstream dependency cannot exhaust connection pools, memory, or goroutines for the entire application.

---

## Features

- **Semaphore-Based Concurrency Limiting**:
  - Enforces hard limits on maximum concurrent in-flight executions.
  - Optional waiting queue before immediate rejection.
- **Partitioned Bulkheads**:
  - Dynamically assigns and tracks separate bulkhead limits per tenant, per customer tier, or per downstream API (`PartitionedBulkhead`).
  - Guarantees that Tenant A maxing out their concurrency limit will never affect Tenant B.
- **HTTP Middleware**:
  - Automatically isolates incoming HTTP routes with key extractors (e.g. `X-Tenant-ID`).
  - Returns `429 Too Many Requests` with a `Retry-After: 1` header on saturation.

---

## Quick Start

### 1. Guarding Downstream Dependencies

```go
package main

import (
    "context"
    "fmt"

    "github.com/dhawalhost/gokit/bulkhead"
)

func main() {
    ctx := context.Background()

    // Allow at most 10 concurrent calls to a heavy external API
    b := bulkhead.New(bulkhead.Config{
        MaxConcurrent: 10,
        MaxQueue:      5, // up to 5 requests can wait in queue before rejection
    })

    err := b.Execute(ctx, func() error {
        return callHeavyExternalService()
    })

    if err == bulkhead.ErrBulkheadFull {
        fmt.Println("Concurrency limit exceeded; failing fast without queue bloat")
    }
}
```

---

### 2. Multi-Tenant HTTP Concurrency Middleware

```go
package main

import (
    "net/http"

    "github.com/dhawalhost/gokit/bulkhead"
    "github.com/go-chi/chi/v5"
)

func main() {
    r := chi.NewRouter()

    // 1. Configure partitioned bulkhead (e.g. max 20 concurrent requests per tenant)
    pb := bulkhead.NewPartitionedBulkhead(bulkhead.Config{
        MaxConcurrent: 20,
        MaxQueue:      0, // Fast fail
    })

    // 2. Attach middleware partitioned by X-Tenant-ID header
    r.Use(bulkhead.Middleware(pb, func(r *http.Request) string {
        return r.Header.Get("X-Tenant-ID")
    }))

    r.Get("/api/v1/reports", generateReportHandler)
}
```
