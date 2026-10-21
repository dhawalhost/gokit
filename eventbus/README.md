# EventBus (`gokit/eventbus`)

`eventbus` provides a high-performance, resilient publish/subscribe event-driven messaging abstraction for Go microservices. It features pluggable backends (in-memory with wildcard routing, and Redis Streams with consumer groups), a composable middleware pipeline (panic recovery, retry with backoff, dead-letter queues, and OpenTelemetry tracing), and seamless integration with the transactional outbox pattern.

---

## Features

- **Universal Message Model**: Stamped with UUID v4, UTC timestamps, partition keys, headers, and type-safe JSON serialization/deserialization.
- **In-Memory Bus (`MemoryBus`)**:
  - High-throughput asynchronous delivery with per-subscription worker pools.
  - Flexible topic pattern matching:
    - Exact: `orders.created`
    - Single-segment wildcard (`*`): `orders.*` matches `orders.created` and `orders.cancelled`
    - Multi-segment wildcard (`#` or `>`): `orders.#` matches `orders.v1.us.created`
    - Global wildcard (`#` or `>`): Subscribes to all topics across the system
- **Redis Streams Bus (`RedisBus`)**:
  - Leverages Redis Streams (`XADD`, `XREADGROUP`, `XACK`) over `redis.UniversalClient` (compatible with standalone Redis, Redis Sentinel, and Redis Cluster).
  - Consumer Groups enable load-balanced, competing consumer distribution across multiple service replicas.
  - Automatic stream creation (`MKSTREAM`) and approximate stream trimming (`MAXLEN ~`) to cap memory usage.
  - Configurable `AutoAck` and multi-goroutine worker concurrency per subscription.
- **Resilient Middleware Pipeline**:
  - **Recovery**: Catches panics in event handlers and converts them into structured errors.
  - **Retry**: Retries transient handler failures with configurable attempts and backoff.
  - **Dead Letter Queue (DLQ)**: Automatically routes exhausted poison messages to a DLQ topic with failure diagnostics (`x-death-reason`, `x-death-timestamp`, `x-original-topic`).
  - **Distributed Tracing**: Automatically injects and extracts W3C `traceparent` headers via OpenTelemetry, creating consumer spans linked to publisher traces.
- **Transactional Outbox Bridge**:
  - `NewOutboxPublisher(bus)` seamlessly adapts any `eventbus.Publisher` into an `outbox.Publisher` for zero-loss domain event dispatching from PostgreSQL.

---

## Installation

```go
import "github.com/dhawalhost/gokit/eventbus"
```

---

## Quick Start

### 1. In-Memory EventBus

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/dhawalhost/gokit/eventbus"
)

type OrderCreated struct {
    OrderID string  `json:"order_id"`
    Amount  float64 `json:"amount"`
}

func main() {
    bus := eventbus.NewMemoryBus(eventbus.MemoryBusConfig{BufferSize: 100})
    defer bus.Close()

    ctx := context.Background()

    // Subscribe to topic pattern with multiple concurrent workers
    sub, err := bus.Subscribe(ctx, "orders.*", func(ctx context.Context, msg *eventbus.Message) error {
        var event OrderCreated
        if err := msg.Unmarshal(&event); err != nil {
            return err
        }
        fmt.Printf("Received Order: %s, Amount: $%.2f (Topic: %s)\n", event.OrderID, event.Amount, msg.Topic)
        return nil
    }, eventbus.WithConcurrency(2))
    if err != nil {
        panic(err)
    }
    defer sub.Unsubscribe()

    // Publish JSON message
    msg, _ := eventbus.NewJSONMessage("orders.created", OrderCreated{
        OrderID: "ORD-1234",
        Amount:  99.50,
    })
    msg.WithKey("tenant-alpha").WithHeader("source", "checkout-service")

    _ = bus.Publish(ctx, msg)

    time.Sleep(100 * time.Millisecond)
}
```

---

### 2. Redis Streams EventBus (Distributed Consumer Groups)

Use `RedisBus` for clustered microservice environments where multiple worker pods should share the processing load without duplicate execution:

```go
import (
    "github.com/dhawalhost/gokit/eventbus"
    "github.com/redis/go-redis/v9"
)

redisClient := redis.NewUniversalClient(&redis.UniversalOptions{
    Addrs: []string{"localhost:6379"},
})

bus, err := eventbus.NewRedisBus(eventbus.RedisBusConfig{
    Client:       redisClient,
    StreamPrefix: "events:",
    DefaultGroup: "order-consumers",
    MaxLen:       50000, // Keep last 50k events per stream
})
if err != nil {
    panic(err)
}
defer bus.Close()

// Subscribe with Consumer Group
sub, err := bus.Subscribe(ctx, "payments.processed", func(ctx context.Context, msg *eventbus.Message) error {
    fmt.Printf("Processing payment %s from key %s\n", string(msg.Payload), msg.Key)
    return nil // Returning nil triggers automatic XACK
},
    eventbus.WithConsumerGroup("billing-service"),
    eventbus.WithAutoAck(true),
    eventbus.WithConcurrency(4),
)
```

---

### 3. Middleware Pipeline (Recovery, Retry, DLQ, and Tracing)

Compose enterprise-grade resilience guards around your event handlers:

```go
dlqBus := eventbus.NewMemoryBus()
defer dlqBus.Close()

// Chain middlewares: Recovery -> Tracing -> DLQ -> Retry -> Handler
handler := eventbus.Chain(
    eventbus.RecoveryMiddleware(),
    eventbus.TracingMiddleware(nil), // uses global OTel tracer
    eventbus.DeadLetterMiddleware(dlqBus, "dead-letter.events"),
    eventbus.RetryMiddleware(3, 50*time.Millisecond),
)(func(ctx context.Context, msg *eventbus.Message) error {
    if string(msg.Payload) == "poison" {
        return fmt.Errorf("unable to process payload")
    }
    return nil
})

// Subscribe with resilient handler
_, _ = bus.Subscribe(ctx, "orders.processed", handler)
```

---

### 4. Transactional Outbox Integration

Connect your transactional outbox relay directly to the EventBus:

```go
import (
    "github.com/dhawalhost/gokit/eventbus"
    "github.com/dhawalhost/gokit/outbox"
)

// 1. Wrap the eventbus into an outbox publisher
outboxPublisher := eventbus.NewOutboxPublisher(redisBus)

// 2. Configure the outbox relay
relay := outbox.NewRelay(outboxStore, outboxPublisher, outbox.RelayConfig{
    BatchSize:    50,
    PollInterval: 500 * time.Millisecond,
})

// 3. Start background outbox polling and publishing
relay.Start(ctx)
```

---

## Configuration Reference

### `MemoryBusConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `BufferSize` | `int` | `100` | Internal channel buffer size per subscription |

### `RedisBusConfig`

| Field | Type | Default | Description |
|---|---|---|---|
| `Client` | `redis.UniversalClient` | *required* | Go-Redis UniversalClient (Standalone, Sentinel, Cluster) |
| `StreamPrefix` | `string` | `"events:"` | Prefix prepended to topics to create Redis stream keys |
| `DefaultGroup` | `string` | `"gokit-workers"` | Default consumer group name |
| `MaxLen` | `int64` | `10000` | Approximate maximum length (`MAXLEN ~`) of the stream |
| `BlockTimeout` | `time.Duration` | `2s` | Blocking timeout for `XREADGROUP` |
| `BatchSize` | `int64` | `10` | Number of messages retrieved per poll cycle |

### `SubscribeOptions`

| Option | Description |
|---|---|
| `WithConsumerGroup(group)` | Sets the consumer group name for load-balanced delivery |
| `WithConsumerName(name)` | Sets the unique consumer name inside the consumer group |
| `WithAutoAck(bool)` | Automatically acknowledges (`XACK`) messages on successful handler completion |
| `WithConcurrency(workers)` | Spawns multiple concurrent worker goroutines processing messages |
