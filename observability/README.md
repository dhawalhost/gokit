# Observability (`gokit/observability`)

`observability` provides an enterprise-grade telemetry foundation for Go microservices, including isolated Prometheus metric registries, Go runtime & process instrumentation, gRPC and HTTP metric interceptors, route cardinality protection, and OpenTelemetry (OTel) distributed tracing with W3C context propagation.

---

## Features

- **Isolated Prometheus Registry**: Dedicated, thread-safe `Registry` preventing global registry pollution and collision panics across tests or multi-tenant pipelines.
- **Go Runtime & Process Telemetry**: Automatic collection of goroutine counts, memory allocs, garbage collection pauses, CPU utilization, and open file descriptors via `collectors.NewGoCollector()`.
- **Pre-Wired HTTP & gRPC Metrics**: Standardized duration histograms, total requests counters, and in-flight request gauges for both HTTP servers and gRPC services.
- **Route Cardinality Protection**: Normalizes dynamic URL path variables (e.g. `/users/12345` -> `/users/{id}`) to prevent time-series memory explosion.
- **Custom Metric Factories**: Fluent, validated registration helpers (`NewCounter`, `NewGauge`, `NewHistogram`, `MustNewHistogram`).
- **Endpoint Security**: Turnkey `/metrics` HTTP handler with optional HTTP Basic Authentication.
- **Distributed Tracing & Context Propagation**:
  - OpenTelemetry OTLP HTTP trace exporter with configurable samplers (`WithSampler`) and composite W3C `TraceContext` + `Baggage` propagators.
  - HTTP client transport wrapper (`TraceRoundTripper`, `NewInstrumentedHTTPClient`) injecting outbound trace headers.
  - Span helpers (`StartSpan`, `RecordError`, `AddEvent`, `SetAttributes`, `TraceID`, `SpanID`).

---

## Architecture Overview

```
                      Inbound Request / RPC
                               │
               ┌───────────────┴───────────────┐
               ▼                               ▼
       [HTTP Middleware]              [gRPC Interceptor]
               │                               │
       - Route Normalization           - Method Normalization
       - Span Creation (OTel)          - Status Code Mapping
       - In-Flight Gauge (+1)          - Duration Histogram
       - Latency Histogram (Observe)   - Call Counter (+1)
               │                               │
               └───────────────┬───────────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │     App Service     │
                    │ ┌─────────────────┐ │
                    │ │ TraceRoundTripper│ │ ──> Outbound HTTP with
                    │ └─────────────────┘ │     W3C Traceparent Header
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │    /metrics HTTP    │ ──> Prometheus Scraper
                    │ (Basic Auth Capable)│
                    └─────────────────────┘
```

---

## Quickstart

### 1. Isolated Prometheus Metrics Registry

```go
package main

import (
	"net/http"

	"github.com/dhawalhost/gokit/observability"
	"github.com/go-chi/chi/v5"
)

func main() {
	// 1. Create an isolated registry with Go runtime and OS process metrics
	reg := observability.NewRegistry("authsvc",
		observability.WithGoRuntimeMetrics(),
		observability.WithProcessMetrics(),
	)

	// 2. Register custom business metrics
	loginCounter := reg.MustNewCounter("logins_total", "Total login attempts", "status", "provider")
	loginCounter.WithLabelValues("success", "google").Inc()

	// 3. Attach HTTP metrics middleware
	r := chi.NewRouter()
	r.Use(observability.HTTPMetrics(reg))

	// 4. Expose protected /metrics endpoint
	r.Handle("/metrics", reg.HTTPHandler(
		observability.WithBasicAuth("prom-agent", "secret-password"),
	))

	http.ListenAndServe(":8080", r)
}
```

---

### 2. gRPC Interceptors

Pre-wire Prometheus metrics into gRPC servers and clients:

```go
// gRPC Server Interceptors
server := grpc.NewServer(
    grpc.UnaryInterceptor(observability.UnaryServerMetrics(reg)),
    grpc.StreamInterceptor(observability.StreamServerMetrics(reg)),
)

// gRPC Client Interceptors
conn, err := grpc.Dial(addr,
    grpc.WithUnaryInterceptor(observability.UnaryClientMetrics(reg)),
    grpc.WithStreamInterceptor(observability.StreamClientMetrics(reg)),
)
```

---

### 3. OpenTelemetry Distributed Tracing & Span Helpers

Initialize OTLP export and propagate trace context to outbound services:

```go
// 1. Initialize OTLP tracer provider
shutdown, err := observability.InitTracer(ctx, config.TelemetryConfig{
    Enabled:      true,
    ServiceName:  "ordersvc",
    OTLPEndpoint: "localhost:4318",
})
defer shutdown(context.Background())

// 2. Instrument outbound HTTP client (automatically injects traceparent header)
httpClient := observability.NewInstrumentedHTTPClient(10 * time.Second)
resp, err := httpClient.Get("http://inventory-svc/items/456")

// 3. Span helper utilities
ctx, span := observability.StartSpan(ctx, "ordersvc", "ProcessOrder")
defer span.End()

observability.SetAttributes(ctx, attribute.String("order.id", "ord-1001"))
observability.AddEvent(ctx, "payment_authorized")

if err != nil {
    observability.RecordError(ctx, err)
}

log.Printf("TraceID: %s, SpanID: %s", observability.TraceID(ctx), observability.SpanID(ctx))
```
