# kafka (`eventbus/kafka`)

`eventbus/kafka` provides a high-throughput, enterprise Apache Kafka & Redpanda streaming adapter for `gokit/eventbus`. It implements [`eventbus.Bus`](../eventbus.go#L75), enabling seamless integration with all `eventbus` middlewares (Retries, Recovery, DLQ, OpenTelemetry tracing) and the Transactional Outbox pattern.

Built on pure-Go [`github.com/segmentio/kafka-go`](https://github.com/segmentio/kafka-go), requiring zero CGO / librdkafka dependencies.

---

## Architecture Overview

```
                      ┌───────────────────────────────┐
                      │   Domain Event Publisher      │
                      │ (e.g. outbox, order service)  │
                      └───────────────┬───────────────┘
                                      │ Publish(ctx, msg)
                                      ▼
                      ┌───────────────────────────────┐
                      │       eventbus/kafka.Bus      │
                      │   (kafka.Writer with Hash     │
                      │   Partitioner & Batch Linger) │
                      └───────────────┬───────────────┘
                                      │
                                      ▼
                      ┌───────────────────────────────┐
                      │    Apache Kafka / Redpanda    │
                      │  Topic: orders.created        │
                      │  Partition 0, 1, 2, ...       │
                      └───────────────┬───────────────┘
                                      │
                                      ▼
                      ┌───────────────────────────────┐
                      │      kafka.Reader Cluster     │
                      │  - Consumer Groups            │
                      │  - Automatic / Manual Acks    │
                      │  - Worker Concurrency Pool    │
                      └───────────────┬───────────────┘
                                      │
                                      ▼
                      ┌───────────────────────────────┐
                      │  eventbus.Middleware Pipeline │
                      │  - Tracing & OpenTelemetry    │
                      │  - Panic Recovery             │
                      │  - Retry with Backoff         │
                      │  - Dead-Letter Queue (DLQ)    │
                      └───────────────┬───────────────┘
                                      │
                                      ▼
                      ┌───────────────────────────────┐
                      │      HandlerFunc Worker       │
                      └───────────────────────────────┘
```

---

## Features

- **Pure-Go Architecture**: Zero CGO dependencies, ultra-fast compilation, cross-platform compatibility.
- **Universal Contract**: Implements [`eventbus.Bus`](../eventbus.go#L75) (`Publish`, `Subscribe`, `Close`), allowing microservices to switch between In-Memory, Redis Streams, RabbitMQ, and Kafka without rewriting domain handlers.
- **Consumer Group Support**: Distributed, load-balanced partition consumption across multiple service replicas.
- **Multi-Worker Concurrency**: Set [`WithConcurrency(N)`](../eventbus.go#L60) to parallelize message processing per subscription.
- **Batching & Linger**: Configurable batch sizes and batch timeouts (`WithBatchSize`, `WithBatchTimeout`) to amortize network I/O.
- **Pluggable Partitioning**: Key-based hash partitioning via [`kafkago.Hash`](https://pkg.go.dev/github.com/segmentio/kafka-go#Hash), least-bytes, or custom balancers.
- **Security & Reliability**: Native TLS (`WithTLS`), custom dialers (`WithDialer`), and configurable required acks (`WithRequiredAcks`).

---

## Installation

```bash
go get github.com/dhawalhost/gokit/eventbus/kafka
```

---

## Quick Start

### 1. Initialize the Kafka Bus

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/dhawalhost/gokit/eventbus"
	"github.com/dhawalhost/gokit/eventbus/kafka"
	kafkago "github.com/segmentio/kafka-go"
)

func main() {
	bus, err := kafka.New(
		kafka.WithBrokers("localhost:9092"),
		kafka.WithClientID("order-service"),
		kafka.WithBatchSize(200),
		kafka.WithBatchTimeout(10*time.Millisecond),
		kafka.WithRequiredAcks(kafkago.RequireAll),
	)
	if err != nil {
		log.Fatalf("failed to connect to kafka: %v", err)
	}
	defer bus.Close()

	ctx := context.Background()

	// 2. Publish an event with partition key
	msg := &eventbus.Message{
		Topic:   "orders.created",
		Key:     "tenant_101", // Partition key ensuring strict in-order processing
		Payload: []byte(`{"order_id":"ord_9901","amount":15000}`),
		Headers: map[string]string{
			"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		},
	}
	if err := bus.Publish(ctx, msg); err != nil {
		log.Fatalf("publish error: %v", err)
	}

	// 3. Subscribe with consumer group & worker pool
	sub, err := bus.Subscribe(ctx, "orders.created", func(c context.Context, m *eventbus.Message) error {
		log.Printf("Received order: %s [Partition Key: %s]", string(m.Payload), m.Key)
		return nil
	},
		eventbus.WithConsumerGroup("order-workers"),
		eventbus.WithConcurrency(4),
		eventbus.WithAutoAck(true),
	)
	if err != nil {
		log.Fatalf("subscribe error: %v", err)
	}
	defer sub.Unsubscribe()

	// Wait for events...
	time.Sleep(5 * time.Second)
}
```

---

## Using with Middleware Pipeline

Apply `eventbus` middlewares to your Kafka consumer:

```go
handler := eventbus.Chain(
    eventbus.RecoveryMiddleware(logger),
    eventbus.TracingMiddleware("orders-consumer"),
    eventbus.RetryMiddleware(3, 100*time.Millisecond),
    eventbus.DeadLetterQueueMiddleware(bus, "orders.dlq"),
)(func(ctx context.Context, msg *eventbus.Message) error {
    return processOrder(ctx, msg)
})

sub, err := bus.Subscribe(ctx, "orders.created", handler,
    eventbus.WithConsumerGroup("order-billing"),
)
```

---

## Configuration Options

| Option | Description | Default |
|---|---|---|
| `WithBrokers(hosts...)` | Seed broker addresses | Required (unless `WithWriter`) |
| `WithClientID(id)` | Client identifier reported to brokers | `""` |
| `WithBalancer(b)` | Partition balancing algorithm | `&kafka.Hash{}` |
| `WithRequiredAcks(acks)` | Acknowledgment guarantee (`RequireAll`, `RequireOne`, `RequireNone`) | `RequireAll` (-1) |
| `WithBatchSize(size)` | Max batch buffer size | `100` |
| `WithBatchTimeout(d)` | Max linger wait duration | `10ms` |
| `WithWriteTimeout(d)` | Timeout for write operations | `5s` |
| `WithReadTimeout(d)` | Timeout for read operations | `10s` |
| `WithCompression(c)` | Compression codec (`Snappy`, `Gzip`, `Lz4`, `Zstd`) | `None` |
| `WithTLS(cfg)` | TLS configuration for secure brokers | `nil` (plaintext) |
| `WithDialer(d)` | Custom dialer | `nil` |
