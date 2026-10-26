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

> [!TIP]
> For micro-batching writes (bulk inserts, audit logs) or request coalescing (DataLoader pattern), use the dedicated [`gokit/batcher`](../batcher/README.md) package.

