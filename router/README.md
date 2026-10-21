# Router (`gokit/router`)

`router` provides constructor helpers for initializing `chi.Mux` routers pre-loaded with middleware chains for consistent API routing.

---

## Quick Start

```go
package main

import (
    "net/http"

    "github.com/dhawalhost/gokit/middleware"
    "github.com/dhawalhost/gokit/router"
    "github.com/go-chi/chi/v5"
)

func main() {
    // 1. Create a router with pre-installed global middlewares
    r := router.NewWithMiddleware(
        middleware.RequestID(),
        middleware.SecureHeaders(),
    )

    // 2. Define route handlers
    r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("ok"))
    })

    // 3. Sub-routes and nested groups
    r.Route("/api/v1/users", func(r chi.Router) {
        r.Get("/", listUsers)
        r.Get("/{id}", getUserByID)
    })
}
```
