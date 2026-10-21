# Resilient HTTP Client (`gokit/client`)

`client` is a production-ready, fault-tolerant outbound HTTP client designed for reliable inter-service microservice communication. It bundles automatic retry backoff with jitter, circuit breaker integration, `Retry-After` header parsing, request body replay, and distributed tracing context propagation.

---

## Features

- **Exponential Backoff with Full Jitter**:
  - Automatically retries transient network errors and server errors (`429 Too Many Requests`, `500`, `502`, `503`, `504`).
  - Full jitter prevents the "thundering herd" problem on downstream services recovering from an outage.
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
        client.WithCircuitBreaker(cb),
        client.WithRetryPolicy(client.DefaultRetryPolicy()), // 3 retries, backoff, 5xx/429
        client.WithTracePropagation(true),                  // W3C tracecontext headers
        client.WithRequestIDPropagation(true),             // X-Request-ID forwarding
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
| `WithTimeout(time.Duration)` | `30s` | Maximum HTTP request timeout |
| `WithCircuitBreaker(*CB)` | `nil` | Integrated circuit breaker instance |
| `WithRetryPolicy(RetryPolicy)` | Default | Custom retry evaluator and backoff strategy |
| `WithTracePropagation(bool)` | `true` | Automatically injects OpenTelemetry headers |
| `WithRequestIDPropagation(bool)`| `true` | Automatically forwards `X-Request-ID` header |
