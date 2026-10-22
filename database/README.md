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

---

## Read/Write Splitting & Replica Routing

For read-heavy database architectures (e.g. AWS Aurora, Cloud SQL, RDS), `ReplicaRouter` routes mutations to the Primary instance and load-balances read queries across Read Replicas:

```go
// 1. Initialize ReplicaRouter
router, err := database.NewReplicaRouter(primaryDB, replicaDB1, replicaDB2)
if err != nil {
    panic(err)
}
defer router.Close()

// 2. Writes always go to Primary
_ = router.Write().GORM.Create(&newOrder)

// 3. Reads automatically round-robin across replicas
var orders []Order
_ = router.GORM(ctx).Find(&orders)

// 4. Force read from Primary to bypass replication lag (read-after-write consistency)
ctx = database.WithPrimary(ctx)
_ = router.GORM(ctx).Find(&orders) // Guarantees read hits Primary
```

---

## 🛡️ Causal "Read-Your-Writes" Consistency Middleware

When users mutate data (e.g. creating a post or updating profile), asynchronous replication lag often causes subsequent page refreshes to hit a replica that hasn't caught up yet, resulting in "ghost" data loss.

`ReadYourWritesMiddleware` automatically pins any mutating user session to the Primary database for the replication lag duration (e.g. 5 seconds):

```go
tracker := database.NewReadYourWritesTracker()

// Key extractor pulls the authenticated user ID or session token
userExtractor := func(r *http.Request) string {
    return r.Header.Get("X-User-ID")
}

// 5-second primary stickiness window after any POST/PUT/PATCH/DELETE
rywMiddleware := database.ReadYourWritesMiddleware(tracker, userExtractor, 5*time.Second)

r := chi.NewRouter()
r.Use(rywMiddleware)

r.Post("/posts", func(w http.ResponseWriter, r *http.Request) {
    // 1. Mutation: Writes to Primary; user_123 is now pinned for 5 seconds
    _ = router.Write().GORM.Create(&newPost)
})

r.Get("/posts", func(w http.ResponseWriter, r *http.Request) {
    // 2. Query: If called within 5 seconds by user_123, router.GORM(r.Context())
    // automatically routes to Primary! All other users continue to hit Read Replicas.
    var posts []Post
    _ = router.GORM(r.Context()).Find(&posts)
})
```
