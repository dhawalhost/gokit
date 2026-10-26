# migration

`migration` provides a standalone, driver-agnostic, embed-friendly database schema migration engine for Go applications. It tracks schema versions in a dedicated tracking table (`schema_migrations`), detects dirty migrations, executes transactional and non-transactional DDL, and natively integrates with [`distlock.Locker`](../distlock/README.md) to eliminate concurrency race conditions during multi-replica container rollouts.

---

## Architecture Overview

```
                        ┌───────────────────────────────┐
                        │      embed.FS / fs.FS         │
                        │ 000001_init.up.sql            │
                        │ 000001_init.down.sql          │
                        │ 000002_add_index.up.sql       │
                        └───────────────┬───────────────┘
                                        │ migration.LoadFromFS
                                        ▼
                        ┌───────────────────────────────┐
                        │     []*migration.Migration    │
                        │   (Sorted, Validated Models)  │
                        └───────────────┬───────────────┘
                                        │
                                        ▼
┌───────────────────────┐       ┌───────────────────────────────┐
│    distlock.Locker    │◄─────►│      migration.Migrator       │
│ (Redis, Consul, Etcd) │       │   Up, Down, UpStep, DownStep, │
└───────────────────────┘       │   MigrateTo, Force, Status    │
                                └───────────────┬───────────────┘
                                                │
                                                ▼
                                ┌───────────────────────────────┐
                                │       *sql.DB / DB Tx         │
                                │   - schema_migrations table   │
                                │   - Atomic DDL / DML changes  │
                                └───────────────────────────────┘
```

---

## Features

- **Driver & Storage Agnostic**:
  - Works with standard library [`*sql.DB`](migrator.go#L106) (`PostgreSQL`, `MySQL`, `SQLite`, `CockroachDB`, `MariaDB`).
  - Abstract [`DB`](migrator.go#L35) interface allows pure in-memory testing without spin-up delays or external containers.
- **Embedded & Filesystem Loading**:
  - Load migrations effortlessly using Go's standard [`io/fs.FS`](migration.go#L67) and `//go:embed`.
  - Automatic validation of file naming convention: `{version}_{name}.{up|down}.sql`.
- **Atomic Transactional Execution**:
  - Executes each migration inside its own database transaction by default, rolling back automatically on failure.
  - Option [`WithNoTransaction()`](options.go#L53) for databases or statements that prohibit transactions (such as `CREATE INDEX CONCURRENTLY` in PostgreSQL).
- **Dirty Migration State Tracking**:
  - Flags migrations as `dirty = true` before execution and clears the flag upon success.
  - Halts execution immediately with [`ErrDirtyMigration`](errors.go#L15) if a prior migration crashed midway, preventing schema corruption.
  - Provides [`Force(version)`](migrator.go#L305) to cleanly reset migration state after manual intervention.
- **Distributed Locking Integration**:
  - Integrates with [`distlock.Locker`](../distlock/README.md) to ensure only a single pod/replica runs migrations during rolling Kubernetes deployments.

---

## Installation

```bash
go get github.com/dhawalhost/gokit/migration
```

---

## Quick Start

### 1. Directory Structure & SQL Files

Organize your SQL migration scripts:

```
migrations/
├── 000001_create_users.up.sql
├── 000001_create_users.down.sql
├── 000002_add_email_index.up.sql
└── 000002_add_email_index.down.sql
```

### 2. Run Migrations with `embed.FS`

```go
package main

import (
	"context"
	"database/sql"
	"embed"
	"log"
	"time"

	"github.com/dhawalhost/gokit/migration"
	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Open database connection
	db, err := sql.Open("postgres", "postgres://user:pass@localhost:5432/mydb?sslmode=disable")
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer db.Close()

	// 2. Parse and load embedded migration scripts
	migs, err := migration.LoadFromFS(migrationFS, "migrations")
	if err != nil {
		log.Fatalf("load migrations: %v", err)
	}

	// 3. Construct migrator
	migrator, err := migration.NewSQL(db, migs)
	if err != nil {
		log.Fatalf("create migrator: %v", err)
	}

	// 4. Apply all pending migrations
	if err := migrator.Up(ctx); err != nil {
		if err == migration.ErrNoChange {
			log.Println("Database schema is up to date!")
			return
		}
		log.Fatalf("migration failed: %v", err)
	}

	log.Println("Migrations applied successfully!")
}
```

---

## Distributed Locking (Kubernetes Pod Rollout)

When multiple instances of a service start up concurrently in Kubernetes, use [`distlock.Locker`](../distlock/README.md) to serialize schema execution:

```go
locker := distlock.NewRedisLocker(redisClient)

migrator, err := migration.NewSQL(db, migs,
    migration.WithTableName("custom_schema_migrations"),
    migration.WithLocker(locker, "lock:db:migrations", 30*time.Second),
)
if err != nil {
    log.Fatal(err)
}

if err := migrator.Up(ctx); err != nil && err != migration.ErrNoChange {
    log.Fatalf("schema migration failed: %v", err)
}
```

---

## Migration Operations

### Step-by-Step Execution

```go
// Apply the next single pending migration
err := migrator.UpStep(ctx, 1)

// Roll back the latest single applied migration
err := migrator.DownStep(ctx, 1)
```

### Target Version Migration

```go
// Migrate forward or backward to version 3
err := migrator.MigrateTo(ctx, 3)

// Rollback all migrations to version 0
err := migrator.Down(ctx)
```

### Inspect Status & Current Version

```go
version, dirty, err := migrator.Version(ctx)
log.Printf("Current Schema Version: %d (Dirty: %v)", version, dirty)

statuses, err := migrator.Status(ctx)
for _, s := range statuses {
    log.Printf("Version %d: %s [Applied: %v, At: %v]", s.Version, s.Name, s.Applied, s.AppliedAt)
}
```

### Recover from Dirty State

If a network failure or syntax error marks a migration dirty:

```go
// Force version state back to 2 (removes dirty flag)
err := migrator.Force(ctx, 2)
```

---

## Configuration Options

| Option | Description | Default |
|---|---|---|
| [`WithTableName(name)`](options.go#L31) | Customizes schema version tracking table name | `"schema_migrations"` |
| [`WithLocker(locker, key, ttl)`](options.go#L40) | Integrates distributed mutual exclusion | `nil` (no locking) |
| [`WithNoTransaction()`](options.go#L53) | Runs DDL directly without wrapping in a transaction | `false` (transactional) |
