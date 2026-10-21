# Server (`gokit/server`)

`server` provides an HTTP server wrapping `net/http` and `chi.Mux` with sensible production defaults, configurable timeouts, optional TLS, and signal-driven graceful shutdown.

---

## Features

- **Graceful Shutdown**: Catches `SIGINT` and `SIGTERM` signals and gracefully drains in-flight requests within a configurable shutdown timeout.
- **Connection Hardening**: Configurable `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` mitigating slow-loris attacks.
- **Testing Friendly**: Exposes `.Handler()` to bind directly into `httptest.NewServer` or `httptest.ResponseRecorder` without occupying physical ports.

---

## Quick Start

```go
package main

import (
    "context"
    "time"

    "github.com/dhawalhost/gokit/server"
    "github.com/go-chi/chi/v5"
)

func main() {
    srv := server.New(
        server.WithAddr(":8080"),
        server.WithReadTimeout(15 * time.Second),
        server.WithWriteTimeout(15 * time.Second),
        server.WithIdleTimeout(60 * time.Second),
        server.WithShutdownTimeout(10 * time.Second),
        // server.WithTLS("cert.pem", "key.pem"), // optional TLS
    )

    r := chi.NewRouter()
    r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("pong"))
    })

    srv.Mount("/", r)

    // Blocks until shutdown signal is received
    if err := srv.Run(context.Background()); err != nil {
        panic(err)
    }
}
```
