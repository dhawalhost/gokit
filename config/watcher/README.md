# config/watcher

`config/watcher` provides resilient, dynamic configuration watching and hot-reloading for cloud-native Go microservices.

---

## Why Dynamic Configuration Watching Matters

In production microservices (Kubernetes, AWS ECS, Serverless):
1. **Zero-Downtime Reconfiguration**: Rate limits, log levels, database connection pool bounds, and feature switches must update instantly without restarting containers or dropping customer traffic.
2. **Kubernetes Symlink Swapping**: When a Kubernetes `ConfigMap` or `Secret` is updated, Kubernetes creates a new timestamped directory and atomically swaps the `..data` symlink. `config/watcher` handles directory-level fsnotify events, symlink resolution, and event debouncing to guarantee clean, race-free reloads.
3. **Resilience & Last-Known-Good (LKG)**: If a newly pushed configuration file contains invalid JSON/YAML or a remote provider encounters a network timeout, `config/watcher` triggers an `OnError` alert and **never** crashes or overwrites the healthy in-memory configuration.

---

## Features

- **Multi-Provider Architecture**:
  - `FileProvider`: Watches local files or Kubernetes ConfigMap mount paths (`.yaml`, `.yml`, `.json`) with automatic symlink resolution and configurable debouncing.
  - `PollingProvider`: Periodically queries remote endpoints (e.g. AWS Secrets Manager, SSM Parameter Store, HashiCorp Vault, remote HTTP APIs) with sha256 checksum comparison to avoid spurious events.
  - `MemoryProvider`: Thread-safe in-memory provider for unit testing and runtime overrides.
- **Provider Precedence**: Layer multiple providers where later providers override keys from earlier ones (e.g. `FileProvider` + `PollingProvider`).
- **Atomic Type Getters**: `GetString()`, `GetInt()`, `GetFloat64()`, `GetBool()`, and `GetDuration()` with default value fallbacks and type coercions.
- **Nested Dot Syntax**: Access deeply nested configuration keys via `server.database.port`.
- **Struct Unmarshaling**: `w.Unmarshal(&myStruct)` atomically decodes the active configuration into typed application structs.
- **Subscription Hooks**:
  - `OnReload(func(snapshot map[string]any))`: Global notification on any configuration change.
  - `OnKeyChange(key, func(oldVal, newVal any))`: Targeted notifications when a specific key changes.
  - `OnError(func(err error))`: Alerts when a provider encounters a fetch or parse error.

---

## Installation

```bash
go get github.com/dhawalhost/gokit/config/watcher
```

---

## Quickstart

### 1. Watching a Local File or Kubernetes ConfigMap

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dhawalhost/gokit/config/watcher"
	"go.uber.org/zap"
)

type AppConfig struct {
	Server struct {
		Port int    `json:"port"`
		Host string `json:"host"`
	} `json:"server"`
	LogLevel string `json:"log_level"`
}

func main() {
	logger, _ := zap.NewProduction()

	// 1. Create file provider with 50ms debounce
	fileProv := watcher.NewFileProvider("/etc/config/app.yaml",
		watcher.WithDebounce(50*time.Millisecond),
	)

	// 2. Initialize watcher
	w := watcher.New(fileProv).WithOptions(
		watcher.WithLogger(logger),
	)

	// 3. Register change callbacks
	w.OnKeyChange("log_level", func(oldVal, newVal any) {
		logger.Info("log level updated dynamically",
			zap.Any("old", oldVal),
			zap.Any("new", newVal),
		)
	})

	w.OnError(func(err error) {
		logger.Error("config reload error (retained LKG)", zap.Error(err))
	})

	// 4. Start watching
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		logger.Fatal("failed to start config watcher", zap.Error(err))
	}

	// 5. Read typed config
	var cfg AppConfig
	if err := w.Unmarshal(&cfg); err != nil {
		logger.Error("unmarshal error", zap.Error(err))
	}
	fmt.Printf("Server listening on %s:%d\n", cfg.Server.Host, cfg.Server.Port)
}
```

---

### 2. Multi-Provider Layering (File + Remote Vault Polling)

```go
fileProv := watcher.NewFileProvider("config.yaml")

vaultProv := watcher.NewPollingProvider("vault", 30*time.Second, func(ctx context.Context) (map[string]any, error) {
	// Query Vault or AWS Secrets Manager
	return map[string]any{
		"database.password": "fetched-from-vault",
	}, nil
})

// Vault provider overrides values from local file provider
w := watcher.New(fileProv, vaultProv)
```
