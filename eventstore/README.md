# `eventstore`: Event Sourcing & CQRS Engine

`eventstore` provides an enterprise-grade Event Sourcing and CQRS (Command Query Responsibility Segregation) engine for Go microservices. It features append-only event streams, optimistic concurrency control, generic aggregate root rehydration, snapshotting strategies, and projection subscription managers.

---

## Key Features

- **Append-Only Event Streams**: Pure immutable logging of domain events with per-stream sequence versions and monotonic global positions.
- **Optimistic Concurrency Control**: Strict version enforcement (`ExpectedVersionAny`, `ExpectedVersionNoStream`, `ExpectedVersionExact(v)`) preventing conflicting concurrent writes.
- **Generic Aggregate Roots**: Type-safe `Repository[T AggregateRoot]` for rehydrating domain aggregates by replaying historical events.
- **Automated Snapshotting**: Pluggable `SnapshotStore` and `SnapshotStrategy` (e.g. `EveryNEvents(100)`, `AlwaysSnapshot`) for sub-millisecond aggregate rehydration on long-lived event streams.
- **CQRS Projections**: `SubscriptionManager` with checkpoint tracking (`CheckpointStore`) for historical catch-up and live event streaming into read models.
- **Contextual Metadata Enrichment**: Inject correlation IDs, causation IDs, tenant IDs, and user identities across all committed events.

---

## Architecture Overview

```
                      Write Side (Commands)
                     ───────────────────────
                      Command Handler / API
                                │
                                ▼
                       AggregateRoot.DoWork()
                     (Emits UncommittedEvents)
                                │
                                ▼
                   Repository[T].Save(ctx, agg)
                     (Optimistic Lock Check)
                                │
                                ▼
                   Event Store (Append-Only Log)
                     (Stream & Global Pos)
                                │
        ┌───────────────────────┴───────────────────────┐
        ▼                                               ▼
Snapshot Store                                  SubscriptionManager
(Periodic State Save)                         (Catch-up & Live Stream)
                                                        │
                                                        ▼
                                              Projections (Read Side)
                                              (SQL / Elastic / Redis)
```

---

## Quick Start

### 1. Define an Aggregate Root

```go
package main

import (
	"errors"
	"github.com/dhawalhost/gokit/eventstore"
)

type BankAccount struct {
	eventstore.BaseAggregate
	Owner   string
	Balance float64
}

func NewBankAccount() *BankAccount {
	return &BankAccount{}
}

func (a *BankAccount) AggregateType() string {
	return "BankAccount"
}

func (a *BankAccount) Open(id, owner string, initialBalance float64) {
	a.SetID(id)
	evt, _ := eventstore.NewEvent(id, "AccountOpened", map[string]any{
		"owner":   owner,
		"balance": initialBalance,
	})
	a.TrackChange(a, evt)
}

func (a *BankAccount) Deposit(amount float64) error {
	if amount <= 0 {
		return errors.New("deposit must be positive")
	}
	evt, _ := eventstore.NewEvent(a.AggregateID(), "MoneyDeposited", map[string]any{
		"amount": amount,
	})
	a.TrackChange(a, evt)
	return nil
}

func (a *BankAccount) ApplyChange(evt eventstore.Event) {
	switch evt.Type {
	case "AccountOpened":
		var payload struct {
			Owner   string  `json:"owner"`
			Balance float64 `json:"balance"`
		}
		_ = evt.UnmarshalData(&payload)
		a.SetID(evt.StreamID)
		a.Owner = payload.Owner
		a.Balance = payload.Balance
	case "MoneyDeposited":
		var payload struct {
			Amount float64 `json:"amount"`
		}
		_ = evt.UnmarshalData(&payload)
		a.Balance += payload.Amount
	}
}
```

### 2. Save & Load with Repository

```go
ctx := context.Background()
store := eventstore.NewMemoryStore()
snapStore := eventstore.NewMemorySnapshotStore()

repo := eventstore.NewRepository[*BankAccount](
	store,
	NewBankAccount,
	eventstore.WithRepositorySnapshotStore(snapStore, eventstore.EveryNEvents(100)),
)

// Create & Persist
acc := NewBankAccount()
acc.Open("acc-101", "Alice", 500)
_ = acc.Deposit(150)
_ = repo.Save(ctx, acc)

// Rehydrate from stream
loaded, err := repo.Load(ctx, "acc-101")
if err != nil {
	log.Fatalf("failed to load: %v", err)
}
fmt.Printf("Balance: %.2f (Version: %d)\n", loaded.Balance, loaded.Version())
```

### 3. Build a CQRS Read Projection

```go
type BalanceProjection struct {
	Total float64
}

func (p *BalanceProjection) Name() string { return "BalanceProjection" }

func (p *BalanceProjection) Handle(ctx context.Context, evt eventstore.Event) error {
	if evt.Type == "MoneyDeposited" {
		var d struct{ Amount float64 }
		_ = evt.UnmarshalData(&d)
		p.Total += d.Amount
	}
	return nil
}

// Run Projection Catch-up & Live Subscription
proj := &BalanceProjection{}
subMgr := eventstore.NewSubscriptionManager(store, eventstore.NewMemoryCheckpointStore())
subMgr.Register(proj)

_ = subMgr.Start(ctx)
defer subMgr.Stop()
```
