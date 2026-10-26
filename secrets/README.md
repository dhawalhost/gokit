# secrets

`secrets` provides dynamic secret management, leak-proof memory scrubbing, multi-provider abstraction (Environment variables, Kubernetes secret volume files, in-memory mocks, and fallback chains), alongside in-memory TTL caching with automatic background rotation for Go microservices.

---

## Architecture Overview

```
                          ┌──────────────────────────┐
                          │    secrets.NewManager    │
                          │   (TTL Caching & Auto-   │
                          │   Background Rotation)   │
                          └────────────┬─────────────┘
                                       │
                                       ▼
                          ┌──────────────────────────┐
                          │     secrets.Provider     │
                          │       (Interface)        │
                          └────────────┬─────────────┘
                                       │
         ┌─────────────────────────────┼─────────────────────────────┐
         ▼                             ▼                             ▼
┌──────────────────┐          ┌──────────────────┐          ┌──────────────────┐
│   EnvProvider    │          │   FileProvider   │          │  ChainProvider   │
│ (Reads normalized│          │ (Reads Kubernetes│          │ (Tries providers │
│  APP_* env vars) │          │  secret volumes) │          │  in prioritized  │
└──────────────────┘          └──────────────────┘          │  fallback order) │
                                                            └─────────┬────────┘
                                                                      │
                                                        ┌─────────────┴─────────────┐
                                                        ▼                           ▼
                                               ┌──────────────────┐        ┌──────────────────┐
                                               │   FileProvider   │        │   EnvProvider    │
                                               │ (Primary Secret) │        │(Local Dev Fallbk)│
                                               └──────────────────┘        └──────────────────┘
```

---

## Features

- **Leak-Proof Secret Container**:
  - Automatically overrides `String()` and `GoString()` to print `"[REDACTED]"`, preventing accidental leakage when printing structures, logging with Zap/Slog, or inspecting stack traces.
  - Implements `json.Marshaler` and `encoding.TextMarshaler` rendering `"[REDACTED]"`.
  - Provides [`Zero()`](secret.go#L112) to securely scrub raw bytes from memory upon deallocation or rotation.
- **Provider Abstractions**:
  - `MemoryProvider`: High-speed thread-safe in-memory provider for unit tests and local mocks.
  - `EnvProvider`: Automatically normalizes dot/dash-separated keys to uppercase with prefixing (e.g. `db.password` with `APP_` prefix reads `APP_DB_PASSWORD`).
  - `FileProvider`: Reads individual secret files from directory mounts (standard for Kubernetes Secret volumes).
  - `ChainProvider`: Evaluates multiple providers in sequence, falling back when `ErrSecretNotFound` occurs.
- **Manager with TTL Caching & Rotation**:
  - In-memory memoization cache with configurable TTL to avoid hammering upstream secret stores.
  - Optional background auto-refresh worker checking and re-loading rotated secrets.
  - Graceful teardown via `Close()`.

---

## Installation

```bash
go get github.com/dhawalhost/gokit/secrets
```

---

## Quickstart

### 1. Leak-Proof Secret Container

```go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/dhawalhost/gokit/secrets"
)

type DatabaseConfig struct {
	Host     string          `json:"host"`
	User     string          `json:"user"`
	Password *secrets.Secret `json:"password"`
}

func main() {
	cfg := DatabaseConfig{
		Host:     "db.internal:5432",
		User:     "postgres",
		Password: secrets.NewSecret("p@ssw0rd!"),
	}
	defer cfg.Password.Zero() // Scrubs bytes from memory on exit

	// Printing directly masks the secret:
	fmt.Println("Password:", cfg.Password) // Prints: Password: [REDACTED]

	// Serializing to JSON masks the secret:
	jsonBytes, _ := json.Marshal(cfg)
	fmt.Println(string(jsonBytes))
	// Prints: {"host":"db.internal:5432","user":"postgres","password":"[REDACTED]"}

	// Explicitly retrieve raw value for database driver connection string:
	rawPassword := cfg.Password.Value()
	_ = rawPassword
}
```

---

### 2. Multi-Provider Fallback & Dynamic Rotation

Chain Kubernetes secret volume files with environment variables fallback, cached with a 5-minute TTL and automatic background rotation:

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dhawalhost/gokit/secrets"
)

func main() {
	ctx := context.Background()

	// 1. Setup multi-tier provider chain:
	// Tries Kubernetes mounted secret volume (/etc/secrets) first;
	// Falls back to process environment variables (APP_*) for local dev.
	provider := secrets.NewChainProvider(
		secrets.NewFileProvider("/etc/secrets"),
		secrets.NewEnvProvider("APP_"),
	)

	// 2. Initialize Manager with 5-minute cache TTL and 1-minute auto-refresh:
	mgr := secrets.NewManager(provider,
		secrets.WithTTL(5*time.Minute),
		secrets.WithCache(true),
		secrets.WithAutoRefresh(1*time.Minute),
	)
	defer mgr.Close()

	// 3. Fetch secret:
	apiKey, err := mgr.Get(ctx, "stripe_api_key")
	if err != nil {
		fmt.Printf("failed to load secret: %v\n", err)
		return
	}

	fmt.Printf("Loaded secret version: %s (updated at %v)\n",
		apiKey.Version(), apiKey.UpdatedAt())
}
```
