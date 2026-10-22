# loadshed

`loadshed` provides adaptive latency-based concurrency limiting and load shedding for Go HTTP and gRPC services, protecting microservices from cascading failures, queue accumulation, and latency collapse under sudden traffic surges.

---

## The Problem: Why Static Rate Limiting Fails

Static rate limiters (e.g. 100 requests/sec) work only when response latency is flat:
- **Fast Downstream (50ms RTT)**: $100\text{ RPS} \times 0.05\text{s} = 5$ concurrent in-flight requests. The server is completely healthy.
- **Slow Downstream (2,000ms RTT)**: $100\text{ RPS} \times 2.0\text{s} = 200$ concurrent in-flight requests. Goroutines multiply, memory explodes, database connection pools exhaust, and the service collapses.

By **Little's Law** ($L = \lambda W$), concurrency equals throughput $\times$ latency. `loadshed` dynamically computes the real-time capacity of the service using round-trip latency gradients inspired by **TCP Vegas** and Netflix's **concurrency-limits**.

---

## How It Works

```
           Inbound Request
                  │
                  ▼
     ┌────────────────────────┐
     │  Acquire(ctx, Priority) │
     └───────────┬────────────┘
                 │
       InFlight < Limit?
        /              \
     YES                NO
      │                  │
   Admit            Drop (Shed)
      │                  │
   Execute               ├── HTTP: 503 / 429 + Retry-After: 1
      │                  └── gRPC: codes.ResourceExhausted
      ▼
   done(err)
      │
      ▼
 Measure RTT & Update Limit:
   Gradient g = minRTT / sampleRTT
   - g >= 1.0 (Fast):  Limit += 1 (Additive increase)
   - g < 1.0  (Queue): Limit = Limit * g + 2 (Multiplicative decrease)
```

---

## Priority Levels

| Priority | Intended Traffic | Behavior During Load |
| :--- | :--- | :--- |
| `PriorityCritical` | Kubernetes liveness/readiness probes, auth, payment capture, webhooks | Reserved extra headroom (+20%) even when standard traffic is shedding |
| `PriorityStandard` | Normal interactive user traffic | Admitted up to the dynamic concurrency limit |
| `PriorityLow` | Analytics, background jobs, reporting, batch scraping | Early-shed as soon as latency gradient drops |

---

## Installation

```bash
go get github.com/dhawalhost/gokit/loadshed
```

---

## Quickstart

### 1. HTTP Middleware

```go
package main

import (
	"net/http"
	"time"

	"github.com/dhawalhost/gokit/loadshed"
	"github.com/go-chi/chi/v5"
)

func main() {
	// Initialize limiter with adaptive defaults
	lim := loadshed.NewLimiter(
		loadshed.WithInitialLimit(50.0),
		loadshed.WithMinLimit(10.0),
		loadshed.WithMaxLimit(500.0),
		loadshed.WithMinRTTWindow(5 * time.Minute),
	)

	r := chi.NewRouter()

	// Apply load shedding middleware globally
	r.Use(loadshed.Middleware(lim,
		loadshed.WithStatusCode(http.StatusServiceUnavailable),
		loadshed.WithRetryAfter("1"),
	))

	r.Get("/api/users", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id": 1}]`))
	})

	http.ListenAndServe(":8080", r)
}
```

---

### 2. gRPC Interceptors

```go
import (
	"github.com/dhawalhost/gokit/loadshed"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

lim := loadshed.NewLimiter(
	loadshed.WithInitialLimit(100.0),
	loadshed.WithMinLimit(20.0),
)

server := grpc.NewServer(
	grpc.UnaryInterceptor(loadshed.UnaryServerInterceptor(lim,
		loadshed.WithGRPCShedCode(codes.ResourceExhausted),
	)),
	grpc.StreamInterceptor(loadshed.StreamServerInterceptor(lim)),
)
```

---

## Configuration Options

| Option | Default | Description |
| :--- | :--- | :--- |
| `WithInitialLimit(f)` | `20.0` | Initial concurrency limit before samples accumulate |
| `WithMinLimit(f)` | `5.0` | Absolute concurrency floor |
| `WithMaxLimit(f)` | `1000.0` | Maximum concurrency ceiling |
| `WithSmoothing(f)` | `0.2` | EWMA weight for smoothing sample RTT |
| `WithMinRTTWindow(d)` | `5m` | Window after which baseline minRTT expires and resets |
| `WithCriticalHeadroom(f)`| `0.20` | Fraction of extra capacity reserved for critical traffic (+20%) |
| `WithStatusCode(code)` | `503` | HTTP status code when shed (`503` or `429`) |
| `WithRetryAfter(sec)` | `"1"` | Value of `Retry-After` response header |
