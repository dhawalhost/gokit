# workflow

`workflow` provides an extensible, production-ready Finite-State Machine (FSM) and workflow orchestration engine for Go microservices. It features type-safe state transitions, conditional pre-condition guards, lifecycle hooks (`OnEnter`, `OnExit`, transition `Action`), audit trail logging, and version-based optimistic concurrency control.

---

## Architecture Overview

```
                      ┌────────────────────────┐
                      │    workflow.Engine     │
                      │  (Orchestration Core)  │
                      └───────────┬────────────┘
                                  │
         ┌────────────────────────┼────────────────────────┐
         ▼                        ▼                        ▼
┌──────────────────┐    ┌──────────────────┐    ┌──────────────────┐
│    Definition    │    │      Store       │    │   EventContext   │
│  (FSM Topology,  │    │  (Persistence &  │    │ (Payload, State, │
│  Guards & Hooks) │    │Optimistic Locks) │    │ Version, Meta)   │
└──────────────────┘    └─────────┬────────┘    └──────────────────┘
                                  │
                        ┌─────────┴─────────┐
                        ▼                   ▼
               ┌─────────────────┐ ┌─────────────────┐
               │   MemoryStore   │ │SQL / Mongo Store│
               │  (Thread-Safe)  │ │   (Custom)      │
               └─────────────────┘ └─────────────────┘
```

---

## Transition Lifecycle Pipeline

When `engine.Fire(ctx, instanceID, event, payload)` is executed, the transition proceeds through the following deterministic pipeline:

```
[Start]
   │
   ▼
1. Fetch Instance & Definition
   │
   ▼
2. Find Transition (from CurrentState on Event)
   │
   ▼
3. Evaluate Guard Condition (Aborts with ErrGuardFailed if false or panics)
   │
   ▼
4. Run Exit Hooks on CurrentState (OnExit)
   │
   ▼
5. Run Transition Action (WithAction)
   │
   ▼
6. Run Enter Hooks on DestinationState (OnEnter)
   │
   ▼
7. Append Audit History & Increment Version (Optimistic Lock)
   │
   ▼
8. Persist to Store (Returns ErrOptimisticLockConflict if concurrent write detected)
   │
   ▼
[Success]
```

---

## Features

- **Fluent FSM Builder**: Declarative API for specifying states, allowed transitions, conditional guards, and execution actions.
- **Panic Isolation**: All user-provided guards, actions, and lifecycle hooks are shielded with panic recovery, recording errors without crashing worker goroutines.
- **Audit Trail History**: Captures immutable audit entries for each transition with timestamps, execution durations, source/target states, and events.
- **Optimistic Concurrency Control**: Uses monotonic `Version int64` increments to reject concurrent conflicting state mutations with `ErrOptimisticLockConflict`.
- **Pluggable Persistence**: Decoupled `Store` interface with a built-in, thread-safe `MemoryStore` driver suitable for testing and single-node orchestration.
- **Inspection & Query APIs**: Inspect valid upcoming transitions via `CanFire()` and list allowed triggers with `PermittedEvents()`.

---

## Installation

```bash
go get github.com/dhawalhost/gokit/workflow
```

---

## Quickstart

### 1. Define a Workflow

Define states, events, and transitions using `workflow.NewBuilder`:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/dhawalhost/gokit/workflow"
)

const (
	StateDraft          workflow.State = "DRAFT"
	StatePendingPayment workflow.State = "PENDING_PAYMENT"
	StatePaid           workflow.State = "PAID"
	StateShipped        workflow.State = "SHIPPED"
	StateCancelled      workflow.State = "CANCELLED"

	EventSubmit workflow.Event = "SUBMIT"
	EventPay    workflow.Event = "PAY"
	EventShip   workflow.Event = "SHIP"
	EventCancel workflow.Event = "CANCEL"
)

func buildOrderWorkflow() (*workflow.Definition, error) {
	return workflow.NewBuilder("order-fulfillment", StateDraft).
		// Allow submission from DRAFT to PENDING_PAYMENT
		AddTransition(EventSubmit, []workflow.State{StateDraft}, StatePendingPayment).
		// Allow payment with a guard requiring a positive amount
		AddTransition(EventPay, []workflow.State{StatePendingPayment}, StatePaid,
			workflow.WithGuard(func(ctx context.Context, e *workflow.EventContext) bool {
				amount, ok := e.Payload.(int)
				return ok && amount > 0
			}),
			workflow.WithAction(func(ctx context.Context, e *workflow.EventContext) error {
				fmt.Printf("Processing payment of $%d for order %s\n", e.Payload.(int), e.InstanceID)
				return nil
			}),
		).
		// Allow shipping from PAID
		AddTransition(EventShip, []workflow.State{StatePaid}, StateShipped).
		// Allow cancellation from DRAFT or PENDING_PAYMENT
		AddTransition(EventCancel, []workflow.State{StateDraft, StatePendingPayment}, StateCancelled).
		// Lifecycle hooks
		OnEnter(StatePaid, func(ctx context.Context, e *workflow.EventContext) error {
			fmt.Printf("Order %s marked as PAID; trigger invoice generation\n", e.InstanceID)
			return nil
		}).
		Build()
}
```

---

### 2. Instantiate and Drive the Machine

```go
func main() {
	ctx := context.Background()

	// 1. Initialize Engine
	engine := workflow.NewEngine(workflow.NewMemoryStore())

	// 2. Register Definition
	orderDef, err := buildOrderWorkflow()
	if err != nil {
		log.Fatalf("failed to build workflow: %v", err)
	}
	_ = engine.RegisterDefinition(orderDef)

	// 3. Create a new instance
	inst, err := engine.CreateInstance(ctx, "order-fulfillment",
		workflow.WithMetadata(map[string]any{"customer_id": "cust-1001"}),
	)
	if err != nil {
		log.Fatalf("failed to create instance: %v", err)
	}
	fmt.Printf("Created order %s in state %s (version: %d)\n", inst.ID, inst.CurrentState, inst.Version)

	// 4. Submit order
	inst, err = engine.Fire(ctx, inst.ID, EventSubmit, nil)
	if err != nil {
		log.Fatalf("submit failed: %v", err)
	}
	fmt.Printf("Order is now in state %s (version: %d)\n", inst.CurrentState, inst.Version)

	// 5. Check if PAY can be fired
	canPay, _ := engine.CanFire(ctx, inst.ID, EventPay)
	fmt.Printf("Can fire PAY? %v\n", canPay)

	// 6. Pay with payload (triggering Guard and Action)
	inst, err = engine.Fire(ctx, inst.ID, EventPay, 250)
	if err != nil {
		log.Fatalf("payment failed: %v", err)
	}
	fmt.Printf("Order paid! Current state: %s (version: %d)\n", inst.CurrentState, inst.Version)

	// 7. Inspect Audit Trail
	for _, entry := range inst.History {
		fmt.Printf("-> %s -> %s via %s (took %v)\n", entry.FromState, entry.ToState, entry.Event, entry.Duration)
	}
}
```

---

### 3. Concurrency & Optimistic Locking

When scaling state machine processing across multiple concurrent workers, `workflow.Engine` detects race conditions using instance versions. If worker B fires a transition after worker A has already updated the instance, worker B receives `ErrOptimisticLockConflict`:

```go
inst, err := engine.Fire(ctx, orderID, EventSubmit, nil)
if errors.Is(err, workflow.ErrOptimisticLockConflict) {
	// Re-fetch latest instance and retry or reject operation
	log.Println("conflict detected: order was modified concurrently")
}
```
