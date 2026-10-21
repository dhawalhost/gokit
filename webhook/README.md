# Webhooks (`gokit/webhook`)

`webhook` provides secure inbound webhook verification and reliable outbound webhook dispatching for inter-system SaaS integrations (e.g. Stripe, GitHub, Shopify, and customer webhooks).

---

## Features

- **Inbound Verification**:
  - HMAC-SHA256 signature verification preventing payload tampering.
  - Replay attack mitigation via timestamp tolerance checking (`X-Webhook-Timestamp`).
  - Constant-time comparison (`crypto/hmac.Equal`) preventing timing attacks.
  - HTTP middleware for automatic signature validation before handler invocation.
- **Outbound Dispatcher**:
  - Automatically calculates and injects HMAC-SHA256 signatures and timestamps.
  - Resilient dispatching powered by `gokit/client` with exponential backoff, jitter, and circuit breaking.
  - Granular delivery telemetry (`DeliveryResult`).

---

## Quick Start

### 1. Inbound Webhook Verification (Receiver)

```go
package main

import (
    "io"
    "net/http"
    "time"

    "github.com/dhawalhost/gokit/webhook"
    "github.com/go-chi/chi/v5"
)

func main() {
    r := chi.NewRouter()

    secret := []byte("whsec_customer_signing_secret")

    // Automatically verifies HMAC signature and timestamp (5m tolerance)
    r.With(webhook.InboundMiddleware(secret, 5*time.Minute)).
        Post("/api/v1/webhooks/stripe", func(w http.ResponseWriter, r *http.Request) {
            body, _ := io.ReadAll(r.Body)
            // Payload is guaranteed authentic and non-replayed
            w.WriteHeader(http.StatusOK)
        })
}
```

---

### 2. Outbound Webhook Dispatcher (Sender)

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/dhawalhost/gokit/webhook"
)

func main() {
    ctx := context.Background()

    dispatcher := webhook.NewDispatcher(webhook.DispatcherConfig{
        Timeout:    10 * time.Second,
        MaxRetries: 3,
        BaseDelay:  500 * time.Millisecond,
    })

    payload := []byte(`{"event":"invoice.paid","amount":49.99}`)
    secret := []byte("whsec_customer_secret")
    endpoint := "https://client.external.com/webhooks"

    res, err := dispatcher.Dispatch(ctx, endpoint, payload, secret)
    if err != nil {
        fmt.Printf("Delivery failed after retries: %v\n", err)
        return
    }

    fmt.Printf("Delivered successfully! Status: %d in %v\n", res.StatusCode, res.Duration)
}
```
