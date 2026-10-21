# Saga (`gokit/saga`)

`saga` provides a distributed transaction orchestrator implementing the **Saga Pattern** for microservices. It coordinates multi-step distributed workflows, ensuring eventual consistency by automatically executing compensating rollback actions in reverse order when any intermediate step fails.

---

## The Problem: Distributed Transactions without 2PC

Two-Phase Commit (2PC) does not scale across microservices and introduces high latency and lock contention. In distributed operations (such as checkout: *Reserve Inventory -> Charge Payment -> Generate Shipping Label*), any step can fail.

The Saga pattern executes local transactions in sequence. If Step 3 fails, the orchestrator triggers compensating actions for Step 2 and Step 1 in reverse order (*Refund Payment -> Release Inventory*), restoring data consistency.

```
Forward Flow:
[ Step 1: Inventory ] ──► [ Step 2: Payment ] ──► [ Step 3: Shipping ] (FAILS!)
                                                         │
Compensating Flow (Reverse Order):                       ▼
[ Compensate 1: Release Inv ] ◄── [ Compensate 2: Refund Payment ]
```

---

## Features

- **Automated Reverse Compensation**: Automatically tracks completed steps and triggers corresponding rollback actions in strict reverse topological order on failure.
- **Detached Compensation Context**: Uses an isolated context for compensations so that even if the caller's request context timed out, rollbacks complete safely.
- **Granular Diagnostics**: Returns an `ExecutionResult` logging execution durations, errors, and compensation statuses per step.

---

## Quick Start

```go
package main

import (
    "context"
    "fmt"

    "github.com/dhawalhost/gokit/saga"
)

func main() {
    ctx := context.Background()

    // 1. Build distributed Saga
    s := saga.New("customer-checkout")

    // Step 1: Reserve Inventory
    s.AddStep(saga.NewStep(
        "reserve-inventory",
        func(ctx context.Context) error {
            fmt.Println("Reserving inventory in warehouse...")
            return nil
        },
        func(ctx context.Context) error {
            fmt.Println("Compensating: Releasing inventory...")
            return nil
        },
    ))

    // Step 2: Charge Credit Card
    s.AddStep(saga.NewStep(
        "charge-payment",
        func(ctx context.Context) error {
            return fmt.Errorf("insufficient funds on credit card")
        },
        func(ctx context.Context) error {
            fmt.Println("Compensating: Refunding card...")
            return nil
        },
    ))

    // 2. Execute Saga
    res, err := s.Execute(ctx)
    if err != nil {
        fmt.Printf("Saga failed at step %q (State: %s): %v\n", res.FailedStep, res.State, err)
        // Output: Saga failed at step "charge-payment" (State: compensated)
    }
}
```
