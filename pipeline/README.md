# Pipeline Package

`gokit/pipeline` provides an enterprise-grade, high-throughput streaming ETL and concurrent data processing pipeline engine in Go. It supports type-safe modular stages, fan-out/fan-in multiplexing, temporal windowing (tumbling and sliding windows), worker pool concurrency, bounded backpressure, automatic retries with backoff, panic recovery, dead-letter queue (DLQ) routing, and live operational telemetry.

---

## Architectural Highlights

```
                          ┌────────────────────────────────────────────────────────┐
                          │                   Execution Graph                      │
                          │                                                        │
   [In Stream] ──────────►│  [Stage 1: Map] ──► [Stage 2: Filter] ──► [Stage 3: Par]  │──────────► [Out Stream]
(<-chan In / Slice)       │       │                    │                     │     │        (<-chan Out / Slice)
                          │       ▼ (on error)         ▼ (on reject)         ▼     │
                          │  ┌──────────────────────────────────────────────────┐  │
                          │  │         Dead-Letter Queue (DLQ Sink)             │  │
                          │  └──────────────────────────────────────────────────┘  │
                          └────────────────────────────────────────────────────────┘
```

1. **Type-Safe Dynamic Stages**: Fluent, generic pipeline construction (`Pipeline[In, Out]`) with typed stage adapters bridging intermediate transformation types (`Map`, `Filter`, `FlatMap`, `Batch`, `Unbatch`, `Parallel`, `Throttle`).
2. **Temporal Windowing**:
   - **Tumbling Windows**: Non-overlapping fixed time windows aggregating streaming elements into time buckets.
   - **Sliding Windows**: Overlapping temporal windows evaluating sliding intervals while pruning expired historical events.
3. **Dead-Letter Routing & Fault Isolation**: Unhandled errors, transformation failures, or unexpected payload shapes can be routed to a configured `DeadLetterSink` (e.g. `MemoryDeadLetterQueue`, `ChanDeadLetterSink`, or custom sinks) without interrupting downstream stream processing.
4. **Panic Isolation & Graceful Drainage**: Unhandled panics in user stage functions are intercepted and converted into wrapped `ErrStagePanicked` errors. When input channels close, all buffered stages drain completely downstream without dropping in-flight elements.
5. **Observability & Metrics**: Live tracking of stage throughput (`ItemsIn`, `ItemsOut`), error counts, dead-letter drops, and microsecond-level latency snapshots via `p.Stats()`.

---

## Quick Start

### 1. Multi-Stage Stream ETL

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dhawalhost/gokit/pipeline"
)

type RawLog struct {
	Level   string
	Message string
}

type EnrichedAlert struct {
	AlertID   string
	Severity  string
	Message   string
	Timestamp time.Time
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dlq := pipeline.NewMemoryDeadLetterQueue(1000)

	// Construct pipeline transforming RawLog -> EnrichedAlert
	p := pipeline.New[RawLog, EnrichedAlert]("alert-enrichment",
		pipeline.WithPipelineBufferSize[RawLog, EnrichedAlert](64),
		pipeline.WithPipelineDLQ[RawLog, EnrichedAlert](dlq),
	)

	// Filter ERROR and FATAL logs
	pipeline.AddStage(p, pipeline.Filter("critical-filter", func(ctx context.Context, log RawLog) bool {
		return log.Level == "ERROR" || log.Level == "FATAL"
	}))

	// Parallel enrichment stage across 4 concurrent workers
	pipeline.AddStage(p, pipeline.Parallel("enricher", 4, func(ctx context.Context, log RawLog) (EnrichedAlert, error) {
		return EnrichedAlert{
			AlertID:   fmt.Sprintf("ALERT-%d", time.Now().UnixNano()),
			Severity:  log.Level,
			Message:   log.Message,
			Timestamp: time.Now().UTC(),
		}, nil
	}))

	// Execute synchronously over a slice of logs
	logs := []RawLog{
		{Level: "INFO", Message: "User logged in"},
		{Level: "ERROR", Message: "Payment gateway timeout"},
		{Level: "DEBUG", Message: "Cache miss"},
		{Level: "FATAL", Message: "Database disk corrupted"},
	}

	alerts, err := p.RunSlice(ctx, logs)
	if err != nil {
		panic(err)
	}

	for _, alert := range alerts {
		fmt.Printf("[%s] %s: %s\n", alert.Severity, alert.AlertID, alert.Message)
	}

	// Inspect metrics
	stats := p.Stats()
	fmt.Printf("Processed: %d in, %d out, %d errors\n", stats.TotalIn, stats.TotalOut, stats.TotalErrors)
}
```

---

### 2. Micro-Batching & Sliding Windows

```go
// Batch items up to 100 elements or every 50ms
batchStage := pipeline.Batch[MetricPoint]("batcher", 100, 50*time.Millisecond)

// Tumbling temporal window calculating sum over 1-second buckets
windowStage := pipeline.TumblingWindow("1s-tumbler", time.Second, func(ctx context.Context, win pipeline.Window[float64]) (float64, error) {
	var sum float64
	for _, val := range win.Items {
		sum += val
	}
	return sum, nil
})
```

---

### 3. Fan-In & Fan-Out Multiplexing

```go
// Multiplex multiple input channels into a single unified stream
unifiedStream := pipeline.FanIn(ctx, sensorChannel1, sensorChannel2, sensorChannel3)

// Distribute work round-robin across 4 worker channels
workerChannels := pipeline.FanOut(ctx, unifiedStream, 4, 32)

// Broadcast/tee stream to multiple independent consumers
mirroredChannels := pipeline.Broadcast(ctx, unifiedStream, 2, 32)
```

---

## Stage Reference

| Stage | Constructor | Description |
|---|---|---|
| `Map` | `pipeline.Map(name, fn, opts...)` | 1-to-1 transformation with retry backoff and panic recovery |
| `Filter` | `pipeline.Filter(name, predicate)` | Predicate filter selectively emitting matching items |
| `FlatMap` | `pipeline.FlatMap(name, fn, opts...)` | 1-to-many expansion producing 0 or more downstream elements |
| `Batch` | `pipeline.Batch(name, size, wait)` | Micro-batching by count or maximum duration |
| `Unbatch` | `pipeline.Unbatch(name)` | Flattens batch slices `[]T` back into individual elements `T` |
| `Parallel` | `pipeline.Parallel(name, workers, fn, opts...)` | Concurrent worker pool processing with thread-safe fan-out |
| `Throttle` | `pipeline.Throttle(name, interval)` | Rate-limits throughput with minimum spacing interval between elements |
| `TumblingWindow` | `pipeline.TumblingWindow(name, duration, aggregator, opts...)` | Fixed non-overlapping temporal aggregation |
| `SlidingWindow` | `pipeline.SlidingWindow(name, size, slide, aggregator, opts...)` | Overlapping temporal aggregation with expiration pruning |
