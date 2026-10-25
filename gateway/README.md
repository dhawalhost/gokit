# gateway

`gateway` provides foundational building blocks for building resilient API Gateways and Backend-For-Frontend (BFF) layers in Go microservices. It features parallel scatter-gather task orchestration with per-task deadlines and fallbacks, alongside a dynamic reverse proxy router supporting path-prefix matching, header injection, and multiple load-balancing strategies (Round-Robin, Least-Connections, Random).

---

## Architecture Overview

```
                          ┌──────────────────────────┐
                          │   Inbound HTTP Request   │
                          └────────────┬─────────────┘
                                       │
                    ┌──────────────────┴──────────────────┐
                    ▼                                     ▼
        ┌───────────────────────┐             ┌───────────────────────┐
        │     gateway.Proxy     │             │   gateway.Aggregator  │
        │    (Reverse Proxy     │             │   (Parallel Fanout    │
        │    & Load Balancer)   │             │   & BFF Composition)  │
        └───────────┬───────────┘             └───────────┬───────────┘
                    │                                     │
         ┌──────────┴──────────┐               ┌──────────┴──────────┐
         ▼                     ▼               ▼          ▼          ▼
  ┌─────────────┐       ┌─────────────┐   ┌─────────┐┌─────────┐┌─────────┐
  │ Microservice│       │ Microservice│   │  User   ││ Orders  ││ Recs    │
  │  Instance 1 │       │  Instance 2 │   │ Service ││ Service ││ Service │
  │ (RoundRobin,│       │ (LeastConn, │   │(Critical││(Standard││(Fallback│
  │  Health)    │       │  Random)    │   │ Timeout)││ Timeout)││ Default)│
  └─────────────┘       └─────────────┘   └─────────┘└─────────┘└─────────┘
```

---

## Features

- **Scatter-Gather Parallel Fanout**: Executes independent downstream fetch operations concurrently, cutting composite endpoint latencies to the slowest single downstream call.
- **Graceful Partial Failure & Fallbacks**: Non-critical task failures populate preconfigured fallback defaults without failing the parent request, while critical task failures immediately abort via `ErrCriticalTaskFailed`.
- **Per-Task Timeout Budgets**: Prevents hanging composite endpoints by assigning strict deadlines to individual downstream calls.
- **Reverse Proxy Routing**: Longest-prefix matching router with optional prefix stripping (`StripPrefix`), forwarding client IPs via `X-Forwarded-For` and `X-Forwarded-Proto`.
- **Pluggable Load Balancers**:
  - `RoundRobinBalancer`: Alternates requests across healthy instances in circular sequence.
  - `LeastConnBalancer`: Routes to the backend instance with the fewest active connections.
  - `RandomBalancer`: Uniform random selection across healthy endpoints.
- **Target Health Tracking**: Seamlessly filters out unhealthy backend instances with atomic health markers (`SetAlive(false)`).

---

## Installation

```bash
go get github.com/dhawalhost/gokit/gateway
```

---

## Quickstart

### 1. Scatter-Gather BFF Aggregator

Compose multiple microservice calls in parallel with individual timeouts and graceful degradation:

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dhawalhost/gokit/gateway"
)

type UserProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func main() {
	ctx := context.Background()

	// Build the parallel aggregation plan
	agg := gateway.NewAggregator().
		// Critical task: If user profile fails, abort entire aggregation
		Add("user", func(ctx context.Context) (any, error) {
			return fetchUserProfile(ctx)
		}, gateway.WithCritical(true), gateway.WithTaskTimeout(500*time.Millisecond)).
		// Non-critical task: Orders list
		Add("orders", func(ctx context.Context) (any, error) {
			return fetchRecentOrders(ctx)
		}, gateway.WithTaskTimeout(1*time.Second)).
		// Non-critical task: If recommendation service fails, fallback to defaults
		Add("recommendations", func(ctx context.Context) (any, error) {
			return fetchPersonalizedRecs(ctx)
		}, gateway.WithFallback([]string{"trending-sku-1", "trending-sku-2"}), gateway.WithTaskTimeout(200*time.Millisecond))

	// Execute fanout
	result, err := agg.Execute(ctx)
	if err != nil {
		fmt.Printf("Critical failure during aggregation: %v\n", err)
		return
	}

	// Extract typed results
	user, _ := gateway.GetTyped[UserProfile](result, "user")
	fmt.Printf("User: %s (%s)\n", user.Name, user.ID)

	recs, _ := gateway.GetTyped[[]string](result, "recommendations")
	fmt.Printf("Recommendations: %v\n", recs)
}

func fetchUserProfile(ctx context.Context) (UserProfile, error) {
	return UserProfile{ID: "usr-42", Name: "Alice"}, nil
}

func fetchRecentOrders(ctx context.Context) ([]string, error) {
	return []string{"ord-1001", "ord-1002"}, nil
}

func fetchPersonalizedRecs(ctx context.Context) ([]string, error) {
	return nil, fmt.Errorf("recs service unavailable") // Will trigger fallback!
}
```

---

### 2. Reverse Proxy with Load Balancing

Route inbound traffic to clusters of downstream microservices with round-robin or least-connections balancing:

```go
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/dhawalhost/gokit/gateway"
)

func main() {
	// Configure reverse proxy routes
	proxy, err := gateway.NewProxy(
		// Route 1: Users Service (Round-Robin across 2 instances)
		gateway.RouteConfig{
			PathPrefix:  "/api/v1/users",
			StripPrefix: true, // Rewrites /api/v1/users/profile -> /profile
			Targets: []string{
				"http://user-service-1:8080",
				"http://user-service-2:8080",
			},
			Balancer: gateway.NewRoundRobinBalancer(),
			Headers: map[string]string{
				"X-Gateway-Route": "users",
			},
			Timeout: 5 * time.Second,
		},
		// Route 2: Orders Service (Least-Connections load balancing)
		gateway.RouteConfig{
			PathPrefix:  "/api/v1/orders",
			StripPrefix: false,
			Targets: []string{
				"http://order-service-1:9090",
				"http://order-service-2:9090",
			},
			Balancer: gateway.NewLeastConnBalancer(),
		},
	)
	if err != nil {
		log.Fatalf("failed to create gateway proxy: %v", err)
	}

	log.Println("API Gateway listening on :8000...")
	if err := http.ListenAndServe(":8000", proxy); err != nil {
		log.Fatal(err)
	}
}
```
