# Outbox (`gokit/outbox`)

`outbox` provides an enterprise-grade implementation of the **Transactional Outbox Pattern** for Go services using PostgreSQL and GORM. It eliminates the distributed dual-write problem by guaranteeing that domain state changes and outbound event messages are committed atomically in the same database transaction, followed by reliable background dispatch.

---

## The Problem Solved: Dual-Write Hazard

In microservice architectures, updating a database and publishing an event to a message broker (e.g. Kafka, RabbitMQ, Redis) in the same HTTP request is prone to failure:
- If the database commit succeeds but the network call to the broker fails, the event is lost forever.
- If the event is published first but the database transaction rolls back, downstream services process a phantom event.

`outbox` solves this by persisting event messages to an `outbox_messages` table within the same database transaction. A background relay daemon polls pending messages using **concurrency leasing (`SKIP LOCKED`)** and publishes them to the message broker.

```
┌────────────────────────────────────────────────────────┐
│               Application Service                      │
└───────────────────────────┬────────────────────────────┘
                            │ 1. Atomic Transaction
                            ▼
              ┌───────────────────────────┐
              │     PostgreSQL Database   │
              │ ┌───────────────────────┐ │
              │ │ Domain Entity Table   │ │
              │ ├───────────────────────┤ │
              │ │ outbox_messages Table │ │
              │ └───────────────────────┘ │
              └─────────────┬─────────────┘
                            │ 2. Polling with SKIP LOCKED
                            ▼
              ┌───────────────────────────┐
              │       Outbox Relay        │
              └─────────────┬─────────────┘
                            │ 3. At-least-once Dispatch
                            ▼
              ┌───────────────────────────┐
              │ Message Broker / EventBus │
              └───────────────────────────┘
```

---

## Features

- **Atomic Enqueueing**: Helper functions (`outbox.Enqueue`, `outbox.EnqueueJSON`) write messages directly into active GORM database transactions.
- **Concurrent Message Leasing (`FOR UPDATE SKIP LOCKED`)**:
  - Multiple relay instances across different pods can poll the same outbox table concurrently without lock contention or deadlock.
  - Automatically leases messages by advancing `scheduled_at = NOW() + leaseDuration` atomically during batch retrieval, preventing duplicate dispatching without holding long open transactions.
- **Resilient Retry Policy**: Exponential backoff with jitter for transient broker outages (`BaseBackoff`, `MaxBackoff`, `MaxRetries`).
- **Dead-Letter Tracking**: Messages exceeding `MaxRetries` transition to `failed` status with detailed `last_error` diagnostic records.
- **Automated Retention Cleanup**: Periodically purges old `published` messages after a configurable retention window (e.g. 7 days).
- **Direct EventBus Adapter**: Seamlessly bridges with `gokit/eventbus` via `eventbus.NewOutboxPublisher(bus)`.

---

## Database Schema

```sql
CREATE TABLE outbox_messages (
    id           VARCHAR(64) PRIMARY KEY,
    topic        VARCHAR(255) NOT NULL,
    key          VARCHAR(255),
    payload      BYTEA NOT NULL,
    headers      JSONB,
    status       VARCHAR(32) NOT NULL DEFAULT 'pending',
    retry_count  INT NOT NULL DEFAULT 0,
    max_retries  INT NOT NULL DEFAULT 5,
    last_error   TEXT,
    created_at   TIMESTAMPTZ NOT NULL,
    scheduled_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);

CREATE INDEX idx_outbox_scheduled ON outbox_messages (status, scheduled_at);
```

---

## Quick Start

### 1. Enqueueing Events in a Transaction

```go
package main

import (
    "context"

    "github.com/dhawalhost/gokit/outbox"
    "gorm.io/gorm"
)

type Order struct {
    ID     string
    Amount float64
}

func CreateOrder(ctx context.Context, db *gorm.DB, order Order) error {
    return db.Transaction(func(tx *gorm.DB) error {
        // 1. Save business data
        if err := tx.Create(&order).Error; err != nil {
            return err
        }

        // 2. Enqueue outbox event within the same database transaction
        _, err := outbox.EnqueueJSON(ctx, tx, "orders.created", order.ID, order, map[string]string{
            "tenant_id": "tenant-alpha",
            "source":    "checkout-service",
        })
        return err
    })
}
```

---

### 2. Starting the Background Relay

```go
package main

import (
    "context"
    "time"

    "github.com/dhawalhost/gokit/outbox"
    "gorm.io/gorm"
)

func StartRelay(ctx context.Context, db *gorm.DB, publisher outbox.Publisher) *outbox.Relay {
    store := outbox.NewGORMStore(db)

    relay := outbox.NewRelay(store, publisher, outbox.RelayConfig{
        BatchSize:     100,
        PollInterval:  500 * time.Millisecond,
        MaxRetries:    5,
        BaseBackoff:   1 * time.Second,
        MaxBackoff:    1 * time.Minute,
        Retention:     7 * 24 * time.Hour, // Keep processed messages for 7 days
        CleanInterval: 1 * time.Hour,
    })

    relay.Start(ctx)
    return relay
}
```

---

### 3. Integrating with `gokit/eventbus`

```go
import (
    "github.com/dhawalhost/gokit/eventbus"
    "github.com/dhawalhost/gokit/outbox"
)

// Adapt an EventBus instance into an Outbox Publisher
outboxPublisher := eventbus.NewOutboxPublisher(redisEventBus)

relay := outbox.NewRelay(store, outboxPublisher, outbox.RelayConfig{
    BatchSize: 50,
})
relay.Start(ctx)
```

---

## Configuration Reference

### `RelayConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `BatchSize` | `int` | `50` | Maximum messages claimed per poll batch |
| `PollInterval` | `time.Duration` | `500ms` | Interval between polling cycles |
| `MaxRetries` | `int` | `5` | Retry attempts before marking a message as `failed` |
| `BaseBackoff` | `time.Duration` | `1s` | Initial retry backoff delay |
| `MaxBackoff` | `time.Duration` | `1m` | Cap on exponential retry backoff duration |
| `Retention` | `time.Duration` | `7 * 24h` | How long published messages are retained before automated purging |
| `CleanInterval` | `time.Duration` | `1h` | Frequency of running the retention cleanup job |
