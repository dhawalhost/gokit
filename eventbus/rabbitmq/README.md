# rabbitmq (`eventbus/rabbitmq`)

`eventbus/rabbitmq` provides a production-grade RabbitMQ (AMQP 0-9-1) streaming broker adapter for `gokit/eventbus`. It implements [`eventbus.Bus`](../eventbus.go#L75), enabling seamless integration with all `eventbus` middlewares (Retries, Recovery, DLQ, OpenTelemetry tracing) and the Transactional Outbox pattern.

Built on the official [`github.com/rabbitmq/amqp091-go`](https://github.com/rabbitmq/amqp091-go) client.

---

## Architecture Overview

```
                      ┌───────────────────────────────┐
                      │    Domain Event Publisher     │
                      │  (outbox relay, order service)│
                      └───────────────┬───────────────┘
                                      │ Publish(ctx, msg)
                                      ▼
                      ┌───────────────────────────────┐
                      │     eventbus/rabbitmq.Bus     │
                      │  Exchange: "events" (topic)   │
                      │  Routing Key: "orders.created"│
                      └───────────────┬───────────────┘
                                      │
                                      ▼
                      ┌───────────────────────────────┐
                      │       RabbitMQ Exchange       │
                      │       Type: "topic"           │
                      └───────┬───────────────┬───────┘
                              │               │
                 Binding:     │               │ Binding:
                 "orders.*"   │               │ "orders.#"
                              ▼               ▼
                 ┌──────────────────┐   ┌──────────────────┐
                 │  Queue: Worker A │   │  Queue: Worker B │
                 │  (Group shared)  │   │  (Group shared)  │
                 └────────┬─────────┘   └────────┬─────────┘
                          │                      │
                          ▼                      ▼
                 ┌─────────────────────────────────────────┐
                 │       eventbus.Middleware Pipeline      │
                 │       - Panic Recovery                  │
                 │       - Retry with Exponential Backoff  │
                 │       - Dead-Letter Queue Routing (DLQ) │
                 │       - OpenTelemetry Tracing Carrier   │
                 └────────────────────┬────────────────────┘
                                      │
                                      ▼
                         ┌─────────────────────────┐
                         │   HandlerFunc Worker    │
                         └─────────────────────────┘
```

---

## Features

- **Standard AMQP 0-9-1 Contract**: Implements [`eventbus.Bus`](../eventbus.go#L75) (`Publish`, `Subscribe`, `Close`), allowing microservices to switch between In-Memory, Redis Streams, Kafka, and RabbitMQ without rewriting domain handlers.
- **Topic Exchange Wildcard Routing**: Native AMQP topic routing:
  - Exact: `orders.created`
  - Single-segment wildcard (`*`): `orders.*` matches `orders.created` and `orders.cancelled`
  - Multi-segment wildcard (`#`): `orders.#` matches `orders.v1.us.created`
- **Competing Consumers Pattern**: Configure [`WithConsumerGroup("group-name")`](../eventbus.go#L40) to bind a shared durable queue across multiple service replicas for load balancing.
- **Broadcast / Fan-out**: Omit `ConsumerGroup` to create an exclusive, auto-delete queue for broadcast semantics.
- **QoS & Prefetch Control**: [`WithPrefetchCount(n)`](options.go#L83) prevents slow consumers from being overwhelmed by unacknowledged messages.
- **Reliable Acknowledgments**:
  - `WithAutoAck(true)`: Automatically sends `Ack(false)` when the handler succeeds.
  - Automatically sends `Nack(false, requeue)` when the handler fails, ensuring zero message loss.
- **Multi-Worker Concurrency**: [`WithConcurrency(N)`](../eventbus.go#L60) spawns $N$ concurrent worker goroutines draining the delivery channel.
- **Security & TLS**: Native AMQPS encryption support via [`WithTLS(cfg)`](options.go#L99).

---

## Installation

```bash
go get github.com/dhawalhost/gokit/eventbus/rabbitmq
```

---

## Quick Start

### 1. Initialize the RabbitMQ Bus

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/dhawalhost/gokit/eventbus"
	"github.com/dhawalhost/gokit/eventbus/rabbitmq"
)

func main() {
	bus, err := rabbitmq.New(
		rabbitmq.WithURL("amqp://guest:guest@localhost:5672/"),
		rabbitmq.WithExchange("app_events"),
		rabbitmq.WithExchangeType("topic"),
		rabbitmq.WithDurable(true),
		rabbitmq.WithPrefetchCount(50),
	)
	if err != nil {
		log.Fatalf("failed to connect to rabbitmq: %v", err)
	}
	defer bus.Close()

	ctx := context.Background()

	// 2. Publish an event
	msg := &eventbus.Message{
		Topic:   "orders.created",
		Key:     "tenant_101",
		Payload: []byte(`{"order_id":"ord_5521","total":8900}`),
		Headers: map[string]string{
			"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		},
	}
	if err := bus.Publish(ctx, msg); err != nil {
		log.Fatalf("publish error: %v", err)
	}

	// 3. Subscribe with competing consumer group
	sub, err := bus.Subscribe(ctx, "orders.*", func(c context.Context, m *eventbus.Message) error {
		log.Printf("Received: %s [Topic: %s, Key: %s]", string(m.Payload), m.Topic, m.Key)
		return nil
	},
		eventbus.WithConsumerGroup("order-fulfillers"),
		eventbus.WithConcurrency(4),
		eventbus.WithAutoAck(true),
	)
	if err != nil {
		log.Fatalf("subscribe error: %v", err)
	}
	defer sub.Unsubscribe()

	time.Sleep(5 * time.Second)
}
```

---

## Using with Middleware Pipeline

```go
handler := eventbus.Chain(
    eventbus.RecoveryMiddleware(logger),
    eventbus.TracingMiddleware("orders-consumer"),
    eventbus.RetryMiddleware(3, 100*time.Millisecond),
    eventbus.DeadLetterQueueMiddleware(bus, "orders.dlq"),
)(func(ctx context.Context, msg *eventbus.Message) error {
    return processOrder(ctx, msg)
})

sub, err := bus.Subscribe(ctx, "orders.#", handler,
    eventbus.WithConsumerGroup("order-billing"),
)
```

---

## Configuration Options

| Option | Description | Default |
|---|---|---|
| `WithURL(url)` | AMQP connection URL (`amqp://...` or `amqps://...`) | Required (unless `WithChannelFactory`) |
| `WithExchange(name)` | Name of the target AMQP exchange | `"events"` |
| `WithExchangeType(kind)` | Type of the exchange (`"topic"`, `"direct"`, `"fanout"`) | `"topic"` |
| `WithDurable(durable)` | Durability for exchange and consumer queues | `true` |
| `WithPrefetchCount(n)` | Prefetch limit per consumer channel | `50` |
| `WithRequeueOnNack(requeue)` | Whether unhandled failed messages are requeued | `true` |
| `WithTLS(cfg)` | TLS configuration for AMQPS connections | `nil` (plaintext) |
| `WithChannelFactory(fn)` | Custom channel creation factory (for unit testing) | `nil` |
