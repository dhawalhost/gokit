# `diagnostics`: Runtime Memory Watchdog & Autonomous Profiler

`diagnostics` provides production-grade runtime memory pressure monitoring, automated heap dump capture, goroutine leak detection, on-demand profiling helpers, and authenticated diagnostic HTTP routers for Go microservices.

---

## Key Features

- **Automated Memory Watchdog**: Continuously tracks `runtime.MemStats` (`HeapAlloc`, `Sys`, `NumGC`) against configured high-watermark thresholds, triggering automated `.pprof` heap snapshots before an unrecoverable container OOM (Out-Of-Memory) kill.
- **Disk IOPS Cooldown**: Enforces cooldown intervals between consecutive dumps (e.g. 3 minutes) preventing disk exhaustion during sustained memory spikes.
- **Goroutine Leak Detector**: Captures baseline goroutine snapshots and evaluates stack traces after task/request execution, with settle timeouts and configurable tolerance/ignore patterns.
- **On-Demand Profiling Utilities**: Convenient wrappers for `DumpHeap`, `DumpGoroutines` (concise or verbose), `DumpCPU`, `DumpBlock`, and `DumpMutex`.
- **Zero-Trust Diagnostic HTTP Handler**: Mounts standard pprof endpoints (`/debug/pprof/*`) and a `/debug/diagnostics/summary` JSON route, guarded by **Bearer token authentication** and **CIDR subnet allowlists** to protect memory maps and credentials from public exposure.

---

## Architecture Overview

```
                      Runtime Memory Watchdog
                     ─────────────────────────
                      Periodic Poller (Ticker)
                                │
                                ▼
                       runtime.ReadMemStats()
                                │
              ┌─────────────────┴─────────────────┐
              ▼                                   ▼
      HeapAlloc < Threshold              HeapAlloc >= Threshold
         (No Action)                              │
                                                  ▼
                                       Cooldown Window Active?
                                      ┌───────────┴───────────┐
                                      ▼                       ▼
                                     Yes                      No
                                 (Suppressed)                 │
                                                              ▼
                                                   Write Heap .pprof Dump
                                                   & Invoke Watermark Hook
```

---

## Quick Start

### 1. Enable Memory Watchdog in Service Bootstrap

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/dhawalhost/gokit/diagnostics"
)

func main() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Alert and dump heap when HeapAlloc exceeds 500 MB
    watchdog := diagnostics.NewMemoryWatchdog(
        diagnostics.WithMaxHeapAlloc(500*1024*1024),
        diagnostics.WithCooldown(5*time.Minute),
        diagnostics.WithDumpDir("/var/log/app/dumps"),
        diagnostics.WithWatermarkHook(func(summary diagnostics.MemorySummary, dumpPath string) {
            log.Printf("WARNING: Memory watermark breached! HeapAlloc: %d bytes. Dump: %s\n",
                summary.HeapAlloc, dumpPath)
        }),
    )

    if err := watchdog.Start(ctx); err != nil {
        log.Fatalf("failed to start watchdog: %v", err)
    }
    defer watchdog.Stop()

    // Run service...
}
```

### 2. Detect Goroutine Leaks in Unit / Integration Tests

```go
func TestCriticalWorker_NoLeak(t *testing.T) {
    // 1. Capture initial baseline goroutines
    baseline := diagnostics.CaptureGoroutines()

    // 2. Execute concurrent workload
    RunConcurrentJob(context.Background())

    // 3. Verify zero lingering goroutines with 100ms grace settlement period
    err := diagnostics.VerifyNoGoroutineLeak(
        baseline,
        diagnostics.WithTolerance(0),
        diagnostics.WithSettleTimeout(100*time.Millisecond),
    )
    if err != nil {
        t.Fatalf("goroutine leak detected: %v", err)
    }
}
```

### 3. Mount Authenticated Diagnostic HTTP Router

```go
package main

import (
    "net/http"

    "github.com/dhawalhost/gokit/diagnostics"
)

func main() {
    // Mount pprof and diagnostic summary endpoints protected by Bearer token & IP allowlist
    diagHandler := diagnostics.NewHandler(
        diagnostics.WithBearerToken("sec-diag-token-xyz"),
        diagnostics.WithAllowedCIDRs("10.0.0.0/8", "127.0.0.1/32"),
        diagnostics.WithPathPrefix("/debug"),
    )

    // Mount on internal management port (e.g. :9090) or primary router
    http.ListenAndServe(":9090", diagHandler)
}
```
