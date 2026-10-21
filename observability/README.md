# Observability (`gokit/observability`)

`observability` provides Prometheus metric collectors with route pattern cardinality protection and OpenTelemetry (OTel) distributed tracing exporters for production microservice observability.

---

## Features

- **Route Cardinality Protection**: Normalizes dynamic URL path variables (e.g. `/users/12345` -> `/users/:id`) to prevent Prometheus memory exhaustion.
- **OpenTelemetry Tracing**: Exports traces via OTLP/HTTP to collectors (Jaeger, Tempo, Datadog, Honeycomb).
- **HTTP Middlewares**: Automated metrics tracking (request duration, status code, method) and trace span creation.

---

## Quick Start

```go
package main

import (
    "context"

    "github.com/dhawalhost/gokit/observability"
    "github.com/go-chi/chi/v5"
)

func SetupObservability(ctx context.Context, r *chi.Mux) func(context.Context) error {
    // 1. Initialize Prometheus metrics
    observability.InitMetrics("my-microservice")

    // 2. Attach observability middlewares
    r.Use(observability.Metrics())
    r.Use(observability.Tracing("my-microservice"))

    // 3. Expose /metrics endpoint for Prometheus scraping
    r.Handle("/metrics", observability.MetricsHandler())

    // 4. Initialize OTLP tracer exporter (optional)
    shutdown, err := observability.InitTracer(ctx, observability.Config{
        Enabled:      true,
        Endpoint:     "http://localhost:4318/v1/traces",
        ServiceName:  "my-microservice",
    })
    if err != nil {
        panic(err)
    }

    return shutdown
}
```
