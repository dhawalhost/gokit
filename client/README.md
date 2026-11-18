# Resilient HTTP Client (`gokit/client`)

`client` is a production-ready, fault-tolerant outbound HTTP client designed for reliable inter-service microservice communication. It bundles automatic retry backoff with jitter, circuit breaker integration, `Retry-After` header parsing, request body replay, and distributed tracing context propagation.

---

## Features

- **Exponential Backoff with Full Jitter & Method-Aware Retries**:
  - Automatically retries transient network errors and retryable status codes (`408`, `429`, `500`, `502`, `503`, `504`) for idempotent HTTP methods (`GET`, `HEAD`, `OPTIONS`, `PUT`, `DELETE`) by default.
  - Non-idempotent methods (`POST`, `PATCH`) are retried only when an `Idempotency-Key` (or `X-Idempotency-Key`) header is present, `WithRetryNonIdempotent(true)` is configured, or a custom `RetryCondition` is set.
  - Full jitter prevents the "thundering herd" problem on downstream services recovering from an outage.
- **Bounded Per-Attempt Timeouts (`WithAttemptTimeout`)**:
  - Enforces individual attempt deadlines while keeping the attempt context alive until `resp.Body.Close()` so large response bodies can be read safely.
- **`Retry-After` Header Adherence**:
  - Automatically respects standard HTTP `Retry-After` delay directives from rate-limited upstream services.
- **Request Body Buffering & Replay**:
  - Automatically caches request bodies in memory to safely re-stream payloads on retries without stream exhaustion.
- **Circuit Breaker Integration**:
  - Preemptively fails fast when downstream services are degraded, protecting system resources.
- **Telemetry & Distributed Context Propagation**:
  - Automatically propagates `X-Request-ID` and OpenTelemetry W3C `traceparent` headers.

---

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/dhawalhost/gokit/circuitbreaker"
    "github.com/dhawalhost/gokit/client"
)

type ChargeRequest struct {
    Amount   float64 `json:"amount"`
    Currency string  `json:"currency"`
}

type ChargeResponse struct {
    TransactionID string `json:"transaction_id"`
    Status        string `json:"status"`
}

func main() {
    ctx := context.Background()

    // 1. Configure circuit breaker
    cb := circuitbreaker.New(circuitbreaker.Config{
        Name:        "payment-gateway",
        MaxFailures: 5,
    })

    // 2. Configure resilient client
    cli := client.New(
        client.WithName("payment-service"),
        client.WithBaseURL("https://api.payments.internal"),
        client.WithTimeout(10*time.Second),
        client.WithAttemptTimeout(3*time.Second),              // keeps attempt context alive until resp.Body.Close()
        client.WithCircuitBreaker(cb),
        client.WithRetryPolicy(client.DefaultRetryPolicy()),     // retries GET/HEAD/OPTIONS/PUT/DELETE by default
        client.WithDefaultHeader("Idempotency-Key", "req-123"), // or WithRetryNonIdempotent(true) to retry POST/PATCH
        client.WithTracePropagation(true),                      // W3C tracecontext headers
        client.WithRequestIDPropagation(true),                 // X-Request-ID forwarding
    )

    // 3. Make resilient POST request
    reqBody := ChargeRequest{Amount: 199.99, Currency: "USD"}
    var resp ChargeResponse

    httpResp, err := cli.PostJSON(ctx, "/v1/charge", reqBody, &resp)
    if err != nil {
        panic(err)
    }

    fmt.Printf("HTTP Status: %d, Transaction ID: %s\n", httpResp.StatusCode, resp.TransactionID)
}
```

---

## Configuration Reference

| Option | Default | Description |
|---|---|---|
| `WithName(string)` | `"http-client"` | Identifier used in telemetry and log tags |
| `WithBaseURL(string)` | `""` | Target service base URL |
| `WithTimeout(time.Duration)` | `30s` | Maximum HTTP request timeout across all attempts |
| `WithAttemptTimeout(time.Duration)` | `0` | Per-attempt timeout; context stays active until `resp.Body.Close()` |
| `WithCircuitBreaker(*CB)` | `nil` | Integrated circuit breaker instance |
| `WithRetryPolicy(RetryPolicy)` | Default | Custom retry evaluator and backoff strategy (`GET`/`HEAD`/`OPTIONS`/`PUT`/`DELETE` by default) |
| `WithRetryNonIdempotent(bool)` | `false` | Opt in to retrying non-idempotent methods (`POST`/`PATCH`) without `Idempotency-Key` |
| `WithTracePropagation(bool)` | `true` | Automatically injects OpenTelemetry headers |
| `WithRequestIDPropagation(bool)`| `true` | Automatically forwards `X-Request-ID` header |
