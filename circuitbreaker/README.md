# Circuit Breaker (`gokit/circuitbreaker`)

`circuitbreaker` implements the Circuit Breaker pattern to prevent cascading failures across distributed systems. When a downstream dependency experiences an outage or degradation, the breaker trips to fail fast, preserving system threads, connections, and latency.

---

## The State Machine

```
      ┌─────────────────────────┐
      │         CLOSED          │ ◄─── Normal Operation (Requests allowed)
      └────────────┬────────────┘
                   │ Failures exceed MaxFailures
                   ▼
      ┌─────────────────────────┐
      │          OPEN           │ ◄─── Fails Fast (Returns ErrCircuitOpen immediately)
      └────────────┬────────────┘
                   │ ResetTimeout expires
                   ▼
      ┌─────────────────────────┐
      │        HALF-OPEN        │ ◄─── Probe Phase (Tests limited requests)
      └─────────────────────────┘
        │                     │
        ▼ Success             ▼ Failure
    [ CLOSED ]            [ OPEN ]
```

---

## Quick Start

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/dhawalhost/gokit/circuitbreaker"
)

func main() {
    cb := circuitbreaker.New(circuitbreaker.Config{
        Name:                "payment-api",
        MaxFailures:         5,                // Opens after 5 consecutive errors
        ResetTimeout:        30 * time.Second, // Wait 30s before testing recovery in Half-Open
        MaxHalfOpenRequests: 2,                // Bounds concurrent probe calls in Half-Open
        FailureRatio:        0.5,              // Optional rolling-window error ratio threshold (50%)
        MinRequests:         20,               // Minimum calls in RollingWindow before evaluating FailureRatio
        RollingWindow:       60 * time.Second, // Rolling-window duration
        ExecutionTimeout:    2 * time.Second,  // Optional per-call deadline passed into ExecuteCtx
        OnStateChange: func(name string, from, to circuitbreaker.State) {
            // Invoked outside internal locks so calling cb.State() is deadlock-free
            fmt.Printf("Circuit %s state changed: %s -> %s\n", name, from, to)
        },
    })

    // Execute downstream call guarded by the breaker (context-aware; cancels on ExecutionTimeout)
    err := cb.ExecuteCtx(context.Background(), func(ctx context.Context) error {
        return callThirdPartyAPI(ctx)
    })

    if errors.Is(err, circuitbreaker.ErrOpen) {
        // Handle fallback or return 503 Service Unavailable immediately
        fmt.Println("Downstream dependency unavailable, returning cached fallback")
    }
}
```
