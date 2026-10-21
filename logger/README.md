# Logger (`gokit/logger`)

`logger` wraps Uber's `zap.Logger` to provide high-performance structured JSON logging with automatic redaction of sensitive credentials, global singleton access, and context propagation helpers.

---

## Features

- **Sensitive Key Masking**: Automatically redacts sensitive fields (e.g. `password`, `token`, `secret`, `authorization`, `api_key`) as `********`.
- **Context Correlation**: Easily attach loggers to `context.Context` to carry request IDs and tenant IDs across function calls.
- **Production Defaults**: Structured JSON formatting, ISO8601 timestamps, and caller source attribution.

---

## Quick Start

```go
package main

import (
    "context"

    "github.com/dhawalhost/gokit/logger"
    "go.uber.org/zap"
)

func main() {
    // 1. Initialize logger (level: debug, info, warn, error)
    log, err := logger.New("info", false) // false = production JSON mode
    if err != nil {
        panic(err)
    }
    logger.SetGlobal(log)

    // 2. Structured key-value logging
    log.Info("processing order",
        zap.String("order_id", "ord-101"),
        zap.Float64("amount", 49.99),
        zap.String("password", "supersecret"), // Automatically masked as ********
    )

    // 3. Inject and retrieve from Context
    ctx := logger.WithContext(context.Background(), log)
    l := logger.FromContext(ctx)
    l.Info("operation completed")
}
```
