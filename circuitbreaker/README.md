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
    "fmt"
    "time"

    "github.com/dhawalhost/gokit/circuitbreaker"
)

func main() {
    cb := circuitbreaker.New(circuitbreaker.Config{
        Name:         "payment-api",
        MaxFailures:  5,               // Opens after 5 consecutive errors
        ResetTimeout: 30 * time.Second, // Wait 30s before testing recovery in Half-Open
        OnStateChange: func(name string, from, to circuitbreaker.State) {
            fmt.Printf("Circuit %s state changed: %s -> %s\n", name, from, to)
        },
    })

    // Execute downstream call guarded by the breaker
    err := cb.Execute(func() error {
        return callThirdPartyAPI()
    })

    if err == circuitbreaker.ErrCircuitOpen {
        // Handle fallback or return 503 Service Unavailable immediately
        fmt.Println("Downstream dependency unavailable, returning cached fallback")
    }
}
```
