# Database (`gokit/database`)

`database` provides production-tuned PostgreSQL connection pooling combining **GORM v2** for type-safe ORM operations and **pgxpool** for high-throughput raw SQL and bulk operations. It also includes migration execution utilities and health check integrations.

---

## Features

- **Dual Access Interface**:
  - `db.GORM`: GORM v2 handle for relational models, hooks, associations, and queries.
  - `db.Pool`: Raw `pgxpool.Pool` for batch processing, copy protocols, and high-performance querying.
- **Tuned Connection Pooling**:
  - Configurable max open connections, idle connections, lifetime caps, and idle timeouts.
- **Automated Schema Migrations**:
  - Built-in migration runner powered by `golang-migrate/migrate/v4`.

---

## Quick Start

```go
package main

import (
    "context"
    "time"

    "github.com/dhawalhost/gokit/database"
)

func main() {
    ctx := context.Background()

    // 1. Establish database connection pool
    db, err := database.New(ctx, database.Config{
        DSN:             "postgres://user:password@localhost:5432/dbname?sslmode=disable",
        MaxOpenConns:    25,
        MaxIdleConns:    5,
        ConnMaxLifetime: 5 * time.Minute,
        ConnMaxIdleTime: 2 * time.Minute,
    })
    if err != nil {
        panic(err)
    }
    defer db.Close()

    // 2. Query via GORM
    var users []User
    db.GORM.Where("active = ?", true).Find(&users)

    // 3. Query via pgxpool
    rows, _ := db.Pool.Query(ctx, "SELECT id, name FROM users WHERE active = true")
    defer rows.Close()

    // 4. Run database migrations
    _ = database.RunMigrations(ctx, db.Config.DSN, "file://migrations")
}
```
