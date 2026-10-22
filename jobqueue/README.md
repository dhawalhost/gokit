# JobQueue (`gokit/jobqueue`)

`jobqueue` provides a production-grade, asynchronous background task execution engine with priority scheduling, worker pools, delayed/scheduled job execution, and automated exponential retry backoff. It supports both high-performance in-memory queues and distributed Redis-backed queues.

---

## Features

- **Priority Scheduling**: Jobs can be scheduled with discrete priority tiers (`PriorityLow`, `PriorityNormal`, `PriorityHigh`, `PriorityCritical`). High-priority tasks execute ahead of lower-priority tasks regardless of submission order.
- **Delayed & Scheduled Execution**: Delay jobs by duration (`EnqueueIn(ctx, job, 5*time.Minute)`) or schedule them for a specific timestamp (`EnqueueAt(ctx, job, nextBillingDate)`).
- **Automated Retry Policy**: Exponential backoff with jitter on task errors until `MaxRetries` is reached, after which jobs transition to `StateFailed`.
- **Worker Concurrency**: Configurable concurrent worker pools per queue instance with graceful drain on service shutdown.
- **Pluggable Backends**:
  - `MemoryQueue`: In-memory heap-based priority queue for monolithic architectures, testing, and local development.
  - `RedisQueue`: Distributed job queue utilizing Redis Sorted Sets (ZSET) and Lists (`LPUSH`/`BRPOP`) with atomic Lua promotion scripts.

---

## Quick Start

### 1. In-Memory JobQueue

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/dhawalhost/gokit/jobqueue"
)

type SendEmailPayload struct {
    To      string `json:"to"`
    Subject string `json:"subject"`
}

func main() {
    ctx := context.Background()

    // 1. Initialize queue with 4 concurrent workers
    q := jobqueue.NewMemoryQueue(jobqueue.QueueConfig{
        Workers: 4,
    })
    defer q.Stop()

    // 2. Register job handler
    q.RegisterHandler("email:send", func(ctx context.Context, job *jobqueue.Job) error {
        var payload SendEmailPayload
        if err := job.Unmarshal(&payload); err != nil {
            return err
        }
        fmt.Printf("Sending email to %s: %s\n", payload.To, payload.Subject)
        return nil
    })

    // 3. Start background processing
    _ = q.Start(ctx)

    // 4. Enqueue immediate high-priority job
    job, _ := jobqueue.NewJSONJob("email:send", SendEmailPayload{
        To:      "user@example.com",
        Subject: "Welcome to our platform!",
    })
    _ = q.Enqueue(ctx, job.WithPriority(jobqueue.PriorityHigh))

    // 5. Enqueue delayed job (runs after 10 minutes)
    reminderJob, _ := jobqueue.NewJSONJob("email:send", SendEmailPayload{
        To:      "user@example.com",
        Subject: "Don't forget to complete your profile",
    })
    _ = q.EnqueueIn(ctx, reminderJob, 10*time.Minute)
}
```

---

### 2. Distributed Redis JobQueue

```go
import (
    "github.com/dhawalhost/gokit/jobqueue"
    "github.com/redis/go-redis/v9"
)

redisClient := redis.NewUniversalClient(&redis.UniversalOptions{
    Addrs: []string{"localhost:6379"},
})

q, err := jobqueue.NewRedisQueue(jobqueue.RedisQueueConfig{
    Client:      redisClient,
    QueuePrefix: "jobs:billing:",
    Workers:     8,
})
if err != nil {
    panic(err)
}
defer q.Stop()

q.RegisterHandler("invoices:generate", invoiceWorkerHandler)
_ = q.Start(ctx)
```

---

## Configuration Reference

| Option | Type | Default | Description |
|---|---|---|---|
| `Workers` | `int` | `4` | Number of concurrent worker goroutines |
| `PollInterval` | `time.Duration` | `1s` | Scheduler interval to promote mature delayed jobs |
| `BaseBackoff` | `time.Duration` | `1s` | Initial retry backoff delay for failed jobs |
| `MaxBackoff` | `time.Duration` | `1m` | Cap on exponential retry delay |

---

## ⚡ High-Throughput Batcher (`jobqueue.Batcher[T]`)

For high-volume ingest pipelines (10k-200k events/sec), individual I/O calls destroy throughput and saturate disk IOPS. `Batcher[T]` buffers items and flushes them either when a batch size threshold is met or when linger timeout expires.

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/dhawalhost/gokit/jobqueue"
)

type AuditLog struct {
    TenantID string
    Event    string
}

func main() {
    ctx := context.Background()

    // Amortize I/O: flush every 100 items or 50ms linger
    batcher := jobqueue.NewBatcher(jobqueue.BatcherConfig[AuditLog]{
        BatchSize:   100,
        Linger:      50 * time.Millisecond,
        BufferSize:  5000,
        BlockOnFull: true,
        FlushFunc: func(ctx context.Context, batch []AuditLog) error {
            // Bulk insert batch into database or Kafka in one round-trip
            fmt.Printf("Flushed batch of %d audit logs\n", len(batch))
            return nil
        },
    })
    defer batcher.Close()

    _ = batcher.Push(ctx, AuditLog{TenantID: "tenant-1", Event: "login"})
}
```

