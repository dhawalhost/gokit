# Config (`gokit/config`)

`config` provides configuration loading powered by Viper with support for YAML files, `.env` files, environment variable overrides (`APP_*`), validation, hot-reloading, and automatic secret redaction (`config.Secret`).

---

## Features

- **Multi-Source Hierarchy**: File configuration (`config.yaml`) seamlessly overridden by environment variables (e.g. `APP_SERVER_ADDR`).
- **Secret Redaction (`config.Secret`)**: Dedicated string wrapper that renders as `[REDACTED]` when printed or serialized to JSON, preventing credential leakage in logs or telemetry.
- **Fail-Fast Validation**: Ensures critical configuration parameters (database DSN, secret keys) are present before service startup.

---

## Quick Start

```go
package main

import (
    "fmt"

    "github.com/dhawalhost/gokit/config"
)

func main() {
    // 1. Load config from file or environment variables
    cfg, err := config.Load("config.yaml")
    if err != nil {
        panic(err)
    }

    // Or use MustLoad (panics on error, loads env defaults if file path is empty)
    // cfg := config.MustLoad("")

    fmt.Println("Server Address:", cfg.Server.Addr)
    fmt.Println("Database DSN:", cfg.Database.DSN) // Encapsulated in config.Secret -> [REDACTED]
}
```
