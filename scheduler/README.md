# scheduler

`scheduler` provides a production-grade, distributed cron and periodic job execution engine for Go microservices. It features standard 5-field cron parsing, interval macros, timezone localization, overlap prevention, panic recovery, execution telemetry, and distributed leader election via `distlock` to guarantee single-replica execution across multi-pod Kubernetes clusters.

---

## Architecture Overview

```
                            Kubernetes Cluster
       ┌────────────────────────┐        ┌────────────────────────┐
       │     Pod 1 (Worker)     │        │     Pod 2 (Worker)     │
       │  ┌──────────────────┐  │        │  ┌──────────────────┐  │
       │  │    Scheduler     │  │        │  │    Scheduler     │  │
       │  └────────┬─────────┘  │        │  └────────┬─────────┘  │
       └───────────┼────────────┘        └───────────┼────────────┘
                   │                                 │
                   │   Try Acquire Lock              │   Try Acquire Lock
                   │   ("job:billing-sync")          │   ("job:billing-sync")
                   ▼                                 ▼
       ┌──────────────────────────────────────────────────────────┐
       │             Distributed Locker (Redis / DB)              │
       │                      via distlock                        │
       └────────────────────────────┬─────────────────────────────┘
                                    │
               ┌────────────────────┴────────────────────┐
               ▼                                         ▼
      Lock Granted (Pod 1)                      Lock Denied (Pod 2)
       - State: Running                          - State: Skipped
       - Execute Job                             - Log & Record Metric
       - Retries & Timeout                       - Await Next Tick
       - Release Lock on Finish
```

---

## Features

- **Standard 5-Field Cron Syntax**: Full support for minute, hour, day-of-month, month, and day-of-week (`*`, `,`, `-`, `/`, and named days `MON-FRI`).
- **Predefined Macros**: `@yearly`, `@annually`, `@monthly`, `@weekly`, `@daily`, `@midnight`, and `@hourly`.
- **Interval Specifications**: Flexible duration expressions like `@every 5m`, `@every 1h30m`, or programmatic `scheduler.Every(10 * time.Minute)`.
- **Timezone Support**: Prefix expressions with `CRON_TZ=America/New_York` or `TZ=UTC`, or set the scheduler-wide default via `WithLocation()`.
- **Distributed Leader Coordination**: Native integration with `distlock.Locker` (Redis, DB, etc.) ensures that in multi-replica deployments, exactly one pod executes each scheduled job run while others skip safely without error.
- **Overlap Prevention**: Choose between `OverlapSkip` (skip tick if previous execution is still running) or `OverlapAllow`.
- **Resilience & Fault Tolerance**:
  - Panic recovery ensures a crashing job never brings down the scheduler daemon.
  - Per-job execution timeouts (`WithJobTimeout`).
  - Automatic retries with exponential/linear backoff (`WithJobRetries`).
- **Telemetry & Inspection**: Thread-safe point-in-time snapshots (`Jobs()`, `Job(id)`), execution counters (`RunCount`, `SuccessCount`, `FailureCount`, `SkippedCount`), and lifecycle hooks (`OnJobStart`, `OnJobSuccess`, `OnJobFailure`, `OnJobSkipped`).

---

## Installation

```bash
go get github.com/dhawalhost/gokit/scheduler
```

---

## Quickstart

### 1. In-Process Periodic Scheduler

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dhawalhost/gokit/scheduler"
)

func main() {
	s := scheduler.New(
		scheduler.WithLocation(time.UTC),
	)

	// Run every weekday at 09:00 AM UTC
	id, err := s.RegisterFunc("0 9 * * 1-5", func(ctx context.Context) error {
		fmt.Println("Running daily weekday sync...")
		return nil
	}, scheduler.WithJobName("weekday-sync"))
	if err != nil {
		log.Fatalf("failed to register job: %v", err)
	}

	// Run every 10 minutes
	_, _ = s.RegisterFunc("@every 10m", func(ctx context.Context) error {
		fmt.Println("Heartbeat check...")
		return nil
	}, scheduler.WithJobName("heartbeat"))

	// Start in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartAsync(ctx)

	// Trigger manually on-demand if needed
	_ = s.RunJob(ctx, id)

	time.Sleep(30 * time.Second)
	_ = s.Stop(context.Background())
}
```

---

### 2. Distributed Locking Across Kubernetes Pods

When running multiple replicas of a microservice, configure a shared `distlock.Locker` (e.g. backed by Redis):

```go
package main

import (
	"context"
	"time"

	"github.com/dhawalhost/gokit/distlock"
	"github.com/dhawalhost/gokit/scheduler"
	"github.com/redis/go-redis/v9"
)

func main() {
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	locker := distlock.NewRedisLocker(rdb)

	s := scheduler.New(
		scheduler.WithLocker(locker),
	)

	// All pods register this exact same job with the same lock key.
	// Only ONE pod will win the lock and execute the billing run.
	_, _ = s.RegisterFunc("@daily", func(ctx context.Context) error {
		// Run heavy billing job
		return nil
	},
		scheduler.WithJobName("daily-billing"),
		scheduler.WithJobLock("scheduler:jobs:billing", 10*time.Minute),
		scheduler.WithJobTimeout(5*time.Minute),
		scheduler.WithJobRetries(3, 10*time.Second),
	)

	s.StartAsync(context.Background())
}
```

---

## Cron Specification Reference

A standard cron expression consists of 5 whitespace-separated fields:

```
┌───────────── Minute (0 - 59)
│ ┌───────────── Hour (0 - 23)
│ │ ┌───────────── Day of Month (1 - 31)
│ │ │ ┌───────────── Month (1 - 12)
│ │ │ │ ┌───────────── Day of Week (0 - 6 or SUN - SAT)
│ │ │ │ │
* * * * *
```

| Syntax | Example | Meaning |
| :--- | :--- | :--- |
| `*` | `* * * * *` | Every minute |
| `,` | `0 0 1,15 * *` | 1st and 15th of every month at midnight |
| `-` | `0 9-17 * * 1-5` | Hourly from 9 AM to 5 PM, Monday through Friday |
| `/` | `*/15 * * * *` | Every 15 minutes |
| Macro | `@daily` | Equivalent to `0 0 * * *` |
| Interval | `@every 1h30m` | Every 1 hour and 30 minutes |
| Timezone | `CRON_TZ=Asia/Tokyo 0 9 * * *` | Every day at 09:00 AM Tokyo time |

---

## Lifecycle Observation Hooks

```go
s := scheduler.New(
	scheduler.WithHook(scheduler.Hook{
		OnJobStart: func(snap scheduler.JobSnapshot) {
			log.Printf("[START] Job %s (ID: %s)", snap.Name, snap.ID)
		},
		OnJobSuccess: func(snap scheduler.JobSnapshot, duration time.Duration) {
			log.Printf("[SUCCESS] Job %s took %v", snap.Name, duration)
		},
		OnJobFailure: func(snap scheduler.JobSnapshot, duration time.Duration, err error) {
			log.Printf("[FAILURE] Job %s failed after %v: %v", snap.Name, duration, err)
		},
		OnJobSkipped: func(snap scheduler.JobSnapshot, reason string) {
			log.Printf("[SKIPPED] Job %s skipped (reason: %s)", snap.Name, reason)
		},
	}),
)
```
