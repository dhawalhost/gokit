# Health (`gokit/health`)

`health` provides Kubernetes-compatible liveness (`/health/live`), readiness (`/health/ready`), and startup probe endpoints with pluggable dependency health checkers.

---

## Features

- **Liveness Probes**: Evaluates process responsiveness.
- **Readiness Probes**: Evaluates external dependencies (e.g. database connectivity, Redis ping, cache status). If any required dependency fails, returns `503 Service Unavailable` with details.

---

## Quick Start

```go
package main

import (
    "net/http"

    "github.com/dhawalhost/gokit/database"
    "github.com/dhawalhost/gokit/health"
    "github.com/go-chi/chi/v5"
)

func RegisterHealthRoutes(r chi.Router, db *database.DB) {
    h := health.NewHandler()

    // Register dependency health checkers (implement health.Checker interface)
    h.Register("database", db)

    // Mount HTTP handlers
    r.Get("/health/live", h.LiveHandler())   // 200 OK if service is up
    r.Get("/health/ready", h.ReadyHandler()) // 200 OK if DB & deps are reachable, else 503
}
```
