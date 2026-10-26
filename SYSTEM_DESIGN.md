# 🏛️ Senior System Design & Production Architecture Playbook

This playbook translates senior-level system design principles into concrete, battle-tested Go patterns using `gokit`.

---

## 🎯 The Senior Design Loop: 5 Steps to Production

At staff and senior levels, system design evaluates **judgment under ambiguity, naming real bottlenecks, and operating under failure modes**, not reciting architectural buzzwords.

```
┌─────────────────────────────────┐     ┌─────────────────────────────────┐
│ 1. Decision-Changing Needs      │ ──> │ 2. Hardware-Shaped Estimates    │
│ (Consistency, SLO, Hot Keys)    │     │ (Peak QPS, RAM, IOPS, Egress)   │
└─────────────────────────────────┘     └─────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────┐     ┌─────────────────────────────────┐
│ 3. API & Schema Contracts       │ ──> │ 4. High-Level Path              │
│ (Ledgers, Idempotency, States)  │     │ (Clean synchronous vs async)    │
└─────────────────────────────────┘     └─────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ 5. Bottleneck Hunting & Trade-Offs                                      │
│ "What breaks at 10× peak? CPU, Disk IOPS, Network, Lock, or Hot Key?"   │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 💥 Bottleneck & Failure Mode Matrix

| Failure Mode / Bottleneck             | Real-World Symptom                                                         | `gokit` Architectural Primitive                                                                                                                                                                           |
| :------------------------------------ | :------------------------------------------------------------------------- | :-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Hot Key / Partition Imbalance**     | 5% of 100k QPS hits a single key, melting Redis/DB CPU                     | [`cache.NewTwoTier`](cache/README.md) (L1 local RAM absorbs hits in ~50ns)                                                                                  |
| **Cache Stampede (Thundering Herd)**  | Hot key expires; 10,000 requests hit DB in same millisecond                | [`cache.GetOrSet`](cache/README.md) (Singleflight request coalescing)                                                                                       |
| **Row-Level Lock Contention**         | `UPDATE counters SET count=count+1` caps at ~2,000 writes/sec              | [`cache.NewShardedCounter`](cache/README.md) (Stripes increments over $N$ slots)                                                                            |
| **Replica Lag Ghosting**              | User updates profile; refresh hits replica with 500ms lag; data disappears | [`database.ReadYourWritesMiddleware`](database/README.md) (Sticky primary routing)                                                                          |
| **Double-Charge / Payment Race**      | Client retries timed-out payment; user charged twice                       | [`idempotency.New`](idempotency/README.md) + [`distlock.New`](distlock/README.md) (In-flight locking)         |
| **Distributed Multi-Service Drift**   | Payment succeeds, inventory reservation fails; inconsistent state          | [`saga.New`](saga/README.md) (Orchestration with reverse compensations)                                                                                     |
| **Dual-Write Inconsistency**          | DB write commits but Redis/Kafka publish fails; permanent drift            | [`outbox.NewOutbox`](outbox/README.md) + [`eventbus.New`](eventbus/README.md) (`SKIP LOCKED` relay)           |
| **Cascading Downstream Failure**      | Slow payment gateway exhausts connection pool and crashes fleet            | [`circuitbreaker.New`](circuitbreaker/README.md) + [`bulkhead.New`](bulkhead/README.md) (Fast-fail isolation) |
| **Inbound Webhook Replay Attacks**    | Stolen webhook payload re-submitted by malicious actor                     | [`webhook.NewVerifier`](webhook/README.md) (HMAC-SHA256 + tolerance window)                                                                                 |
| **Unsafe Deployments / Bad Rollouts** | Buggy new feature deploys to 100% of traffic, causing outage               | [`featureflag.NewClient`](featureflag/README.md) (Sticky deterministic canary rollout)                                                                      |
| **Scatter-Gather Tail Latency**      | Query fans out to 100 shards; p99 hits 63% tail                            | [`idgen.ExtractShardID`](idgen/README.md) (Time-sorted 64-bit ID with embedded routing)                                                                      |
| **LSM Write Amplification Debt**      | 50 MB/s logical writes trigger 500 MB/s disk I/O; write stalls              | [`batcher.NewBatchAccumulator`](batcher/README.md) + [`jobqueue.NewBatcher`](jobqueue/README.md) (Amortize writes via linger) |
| **Dual-Write Search Desync**          | App writes to DB and ES independently; crashes cause silent drift          | [`outbox.NewOutbox`](outbox/README.md) (CDC log tailing into search posting lists)                                                                           |
| **Consumer Lag Explosion**            | Consumer falls behind; unbounded queue triggers OOM crash                  | [`jobqueue.NewBatcher`](jobqueue/README.md) (Configurable prefetch & backpressure watermarks)                                                                |
| **Broker Redelivery Duplicates**      | Network timeout triggers retry; transaction executes twice                 | [`idempotency.New`](idempotency/README.md) (`INSERT ... ON CONFLICT DO NOTHING` / atomic token check)                                                        |
| **N+1 Database Query Storm**          | GraphQL / REST resolvers fire 50 individual queries for 50 items          | [`batcher.NewDataLoader`](batcher/README.md) (Singleflight batch coalescing with in-memory TTL memoization)                                                  |
| **BFF Aggregation Tail Drag**         | Slowest microservice blocks client screen on mobile fanout                 | [`gateway.ScatterGather`](gateway/README.md) (Parallel fanout with context deadlines & partial degradation)                                                 |
| **Multi-Replica Migration Race**      | 20 Kubernetes pods run DDL concurrently on deploy, corrupting schema       | [`migration.NewSQL`](migration/README.md) + [`distlock`](distlock/README.md) (Distributed lock mutex)         |
| **Credential & Key Log Leakage**      | API keys, secrets, or passwords logged in crash dumps or tracing spans     | [`secrets.NewSecret`](secrets/README.md) (`[REDACTED]` stringer & leak-proof memory zeroing)                                                                 |
| **Invalid State Transition Race**     | Order cancelled after shipment due to concurrent status updates            | [`workflow.NewEngine`](workflow/README.md) (FSM transition guards & optimistic version locking)                                                              |
| **Multi-Tenant Data Bleed**           | Unscoped query or missing `WHERE tenant_id` leaks data across accounts      | [`multitenancy.TenantMiddleware`](multitenancy/README.md) (PostgreSQL RLS session variables & GORM scopes)                                                   |
| **Fleet Cascade Saturation / OOM**   | Influx of peak burst traffic exhausts server CPU and memory                | [`loadshed.Adaptive`](loadshed/README.md) (Priority-aware traffic shedding protecting critical paths)                                                        |
| **External Search Cluster Bloat**     | Multi-node Elasticsearch clusters add 50ms latency & thousands in cloud bills | [`search.NewMemoryIndex`](search/README.md) (In-process sub-millisecond BM25 inverted index with zero external dependencies)                               |
| **Network Egress Bill Shock & Delay** | 100k QPS returning 50 KB JSON responses saturates 40 Gbps NICs & racks up egress | [`compression.Middleware`](compression/README.md) (Zero-allocation multi-codec compression reducing egress by 70–85% with Zstd & Brotli)                    |
| **Audit Loss & Concurrent State Corruption** | Destructive in-place updates overwrite ledger history or cause lost updates | [`eventstore.NewRepository`](eventstore/README.md) (Append-only immutable event streams with optimistic version locking & CQRS projection replay) |
| **Session Hijacking & Fixation Attack** | Stolen cookie or fixed session token allows unauthorized account takeover | [`session.NewManager`](session/README.md) (Subnet & UA fingerprint validation, session regeneration on login, sliding/absolute TTLs) |
| **Silent OOM Kills & Goroutine Runaway** | Unmonitored memory leaks trigger Linux kernel SIGKILL 137 without stack traces | [`diagnostics.NewMemoryWatchdog`](diagnostics/README.md) (Automated watermark heap dumping with cooldown, goroutine leak analyzer & token-secured pprof) |
| **Streaming ETL Deadlocks & Memory Starvation** | Unbounded stream channels exhaust memory, slow downstream consumers halt upstream producers, and unhandled errors crash long-lived ETL pipelines | [`pipeline.New`](pipeline/README.md) (Bounded intermediate backpressure, fan-out worker pools, temporal windowing, panic isolation, and dead-letter routing) |

---

## 📐 Production Code Blueprints

### 1. Absorbing Hot Keys & Stampedes (Catalog / Trending Feed)

```go
package main

import (
    "context"
    "time"

    "github.com/dhawalhost/gokit/cache"
)

func GetTrendingCatalogItem(ctx context.Context, twoTier *cache.TwoTierCache, productID string) (string, error) {
    // 1. Fast L1 RAM check (~50ns)
    // 2. Fall back to Redis L2 (~1ms)
    // 3. If cache miss, SingleFlight coalesces 10,000 concurrent callers to 1 DB execution
    return twoTier.GetOrSet(ctx, "catalog:"+productID, 10*time.Minute, func(c context.Context) (string, error) {
        return queryDatabaseForProduct(productID), nil
    })
}
```

---

### 2. Eliminating Counter Contention at Scale (Views, Likes, Votes)

```go
package main

import (
    "context"

    "github.com/dhawalhost/gokit/cache"
)

// RecordEngagement scales to 50,000+ writes/sec without row-level lock contention
func RecordEngagement(ctx context.Context, counter *cache.ShardedCounter, postID string) error {
    // Increments a pseudo-random stripe (post:123:slot:7)
    _, err := counter.Incr(ctx, "post:"+postID+":views", 1)
    return err
}

func GetEngagement(ctx context.Context, counter *cache.ShardedCounter, postID string) (int64, error) {
    // Aggregates all stripes on read
    return counter.Get(ctx, "post:"+postID+":views")
}
```

---

### 3. Causal "Read-Your-Writes" Consistency Across Read Replicas

```go
package main

import (
    "net/http"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/dhawalhost/gokit/database"
)

func SetupRouter(replicaRouter *database.ReplicaRouter) http.Handler {
    tracker := database.NewReadYourWritesTracker()
    userExtractor := func(r *http.Request) string {
        return r.Header.Get("X-User-ID")
    }

    r := chi.NewRouter()
    // Automatically routes subsequent reads to Primary for 5s after any POST/PUT/PATCH/DELETE
    r.Use(database.ReadYourWritesMiddleware(tracker, userExtractor, 5*time.Second))

    r.Post("/user/profile", func(w http.ResponseWriter, r *http.Request) {
        _ = replicaRouter.Write().GORM.Save(&updatedProfile)
    })

    r.Get("/user/profile", func(w http.ResponseWriter, r *http.Request) {
        // If user mutated recently: reads from Primary (0 replica lag)!
        // If not: reads from round-robin Read Replicas.
        _ = replicaRouter.GORM(r.Context()).Find(&profile)
    })

    return r
}
```

---

### 4. Distributed Multi-Service Checkout via Saga

```go
package main

import (
    "context"
    "fmt"

    "github.com/dhawalhost/gokit/saga"
)

func ProcessOrder(ctx context.Context, orderID string, amount int64) (*saga.ExecutionResult, error) {
    orchestrator := saga.New("order-checkout")

    // Step 1: Charge customer
    orchestrator.AddStep(saga.NewStep(
        "charge-payment",
        func(c context.Context) error { return paymentGateway.Charge(orderID, amount) },
        func(c context.Context) error { return paymentGateway.Refund(orderID, amount) }, // rollback
    ))

    // Step 2: Reserve inventory
    orchestrator.AddStep(saga.NewStep(
        "reserve-inventory",
        func(c context.Context) error { return inventoryService.Reserve(orderID) },
        func(c context.Context) error { return inventoryService.Release(orderID) }, // rollback
    ))

    // Step 3: Dispatch warehouse shipment
    orchestrator.AddStep(saga.NewStep(
        "dispatch-shipment",
        func(c context.Context) error { return warehouseService.Ship(orderID) },
        nil, // Terminal action
    ))

    result, err := orchestrator.Execute(ctx)
    if err != nil {
        fmt.Printf("Order failed at %s. Compensations executed. State: %s\n", result.FailedStep, result.State)
        return result, err
    }

    return result, nil
}
```

---

### 5. Eliminating N+1 Query Storms via DataLoader (GraphQL / REST)

```go
package main

import (
    "context"
    "time"

    "github.com/dhawalhost/gokit/batcher"
)

type User struct {
    ID   string
    Name string
}

func SetupUserDataLoader(db Database) *batcher.DataLoader[string, *User] {
    // Coalesces individual user lookups arriving within 10ms into a single batch SELECT query
    return batcher.NewDataLoader(
        func(ctx context.Context, keys []string) (map[string]*User, error) {
            // SELECT * FROM users WHERE id IN ($1, $2, ...)
            return db.BatchFetchUsers(ctx, keys)
        },
        batcher.WithDataLoaderMaxBatchSize(100),
        batcher.WithDataLoaderLinger(10*time.Millisecond),
        batcher.WithDataLoaderTTL(5*time.Minute), // In-memory per-request/TTL memoization
    )
}

func ResolveOrderAuthor(ctx context.Context, loader *batcher.DataLoader[string, *User], authorID string) (*User, error) {
    // 50 concurrent GraphQL resolvers call Load() simultaneously -> exactly 1 DB query is fired!
    return loader.Load(ctx, authorID)
}
```

---

### 6. High-Throughput Write Amortization via Batch Accumulator (Audit / Metrics)

```go
package main

import (
    "context"
    "time"

    "github.com/dhawalhost/gokit/batcher"
)

type LogEvent struct {
    TenantID  string
    Timestamp time.Time
    Payload   []byte
}

func InitAuditBuffer(ctx context.Context, sink DatabaseSink) (*batcher.BatchAccumulator[LogEvent], error) {
    // Amortizes 100,000 writes/sec into micro-batches of 500 items or 50ms flush windows
    return batcher.NewBatchAccumulator[LogEvent](
        500,
        50*time.Millisecond,
        func(ctx context.Context, batch []LogEvent) error {
            return sink.BulkInsert(ctx, batch)
        },
    )
}
```

---

### 7. Parallel BFF Scatter-Gather with Partial Degradation (Mobile Home Screen)

```go
package main

import (
    "context"
    "time"

    "github.com/dhawalhost/gokit/gateway"
)

type HomeScreenData struct {
    Profile UserProfile
    Feed    []FeedItem
    Promo   *PromotionBanner
}

func FetchHomeScreen(ctx context.Context) (*HomeScreenData, error) {
    // Fans out concurrently; enforces a strict 200ms p99 SLA budget across all 3 downstreams
    taskProfile := gateway.Task[UserProfile]{
        Name: "profile",
        Fn:   func(c context.Context) (UserProfile, error) { return userClient.GetProfile(c) },
    }
    taskFeed := gateway.Task[[]FeedItem]{
        Name: "feed",
        Fn:   func(c context.Context) ([]FeedItem, error) { return feedClient.GetTimeline(c) },
    }
    taskPromo := gateway.Task[*PromotionBanner]{
        Name: "promo",
        Fn:   func(c context.Context) (*PromotionBanner, error) { return promoClient.GetActive(c) },
    }

    res := gateway.ScatterGather(ctx, 200*time.Millisecond, taskProfile, taskFeed, taskPromo)

    // Non-critical promotion banner can fail or time out gracefully without failing home screen
    data := &HomeScreenData{
        Profile: gateway.MustGet(res, "profile").(UserProfile),
        Feed:    gateway.MustGet(res, "feed").([]FeedItem),
    }
    if promoRes, ok := res["promo"]; ok && promoRes.Err == nil {
        data.Promo = promoRes.Value.(*PromotionBanner)
    }

    return data, nil
}
```

---

### 8. Leak-Proof Dynamic Secrets & Rotation

```go
package main

import (
    "context"
    "time"

    "github.com/dhawalhost/gokit/secrets"
)

func InitDatabaseCredentials(ctx context.Context) (*secrets.Secret, error) {
    // Prioritized chain: Kubernetes Secret Volume -> Fallback to App Environment
    chain := secrets.NewChainProvider(
        secrets.NewFileProvider("/var/run/secrets/db"),
        secrets.NewEnvProvider("APP_"),
    )

    // Cached with 10-minute TTL and background automated rotation worker
    mgr, err := secrets.NewManager(chain,
        secrets.WithTTL(10*time.Minute),
        secrets.WithAutoRefresh(1*time.Minute),
    )
    if err != nil {
        return nil, err
    }

    // Secret prints "[REDACTED]" in logs/JSON, and Zero() securely scrubs memory
    sec, err := mgr.Get(ctx, "db_password")
    if err != nil {
        return nil, err
    }

    return sec, nil
}
```

---

### 9. Multi-Replica Safe Database Schema Migrations

```go
package main

import (
    "context"
    "database/sql"
    "embed"
    "time"

    "github.com/dhawalhost/gokit/distlock"
    "github.com/dhawalhost/gokit/migration"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func RunSafeMigrations(ctx context.Context, db *sql.DB, locker distlock.Locker) error {
    migs, err := migration.LoadFromFS(migrationFS, "migrations")
    if err != nil {
        return err
    }

    // Mutual exclusion: Serializes migration across 30 Kubernetes pods spinning up simultaneously
    migrator, err := migration.NewSQL(db, migs,
        migration.WithTableName("schema_migrations"),
        migration.WithLocker(locker, "lock:db:migration", 45*time.Second),
    )
    if err != nil {
        return err
    }

    // Transactional DDL execution with dirty state detection
    if err := migrator.Up(ctx); err != nil && err != migration.ErrNoChange {
        return err
    }

    return nil
}
```

---

### 10. Concurrency-Safe Workflow State Machine (KYC / Order Pipeline)

```go
package main

import (
    "context"

    "github.com/dhawalhost/gokit/workflow"
)

type OrderContext struct {
    OrderID string
    Amount  int64
    Version int64 // Optimistic locking version
}

func SetupOrderWorkflow() (*workflow.Engine[OrderContext], error) {
    engine := workflow.NewEngine[OrderContext](workflow.Config{
        InitialState: "PENDING",
    })

    // Strict state transitions prevent invalid business logic (e.g., cancelling a delivered order)
    err := engine.RegisterTransition(workflow.Transition[OrderContext]{
        From:  "PENDING",
        To:    "PAID",
        Event: "PAYMENT_SUCCESS",
        Guard: func(ctx context.Context, from, to workflow.State, data *OrderContext) bool {
            return data.Amount > 0
        },
    })
    if err != nil {
        return nil, err
    }

    return engine, nil
}
```

---

### 11. Sub-Millisecond In-Memory Search & Typeahead Autocomplete

```go
package main

import (
    "context"

    "github.com/dhawalhost/gokit/search"
)

type Product struct {
    ID          string
    Title       string
    Description string
    Category    string
    Price       float64
}

func SetupCatalogSearch(ctx context.Context, products []*Product) (search.Index, error) {
    // Configures edge n-grams for instant typeahead prefix matching
    cfg := search.IndexConfig{
        DefaultAnalyzer: search.NewStandardAnalyzer(),
        FieldAnalyzers: map[string]*search.Analyzer{
            "suggest": search.NewAutocompleteAnalyzer(2, 6),
        },
        K1: 1.2,
        B:  0.75,
    }
    idx := search.NewMemoryIndex(cfg)

    // Batch index products with text fields, category tags, and numeric prices
    var docs []*search.Document
    for _, p := range products {
        doc := search.NewDocument(p.ID).
            SetField("title", p.Title).
            SetField("content", p.Description).
            SetField("suggest", p.Title).
            SetNumeric("price", p.Price).
            AddTag(p.Category)
        docs = append(docs, doc)
    }
    if err := idx.IndexBatch(ctx, docs); err != nil {
        return nil, err
    }

    return idx, nil
}

func SearchCatalog(ctx context.Context, idx search.Index, queryStr, category string, maxPrice float64) (*search.Result, error) {
    // Combines BM25 multi-field matching with strict category filter and price range
    q := search.NewBoolQuery().
        MustClause(
            search.NewMultiMatchQuery(queryStr, "title", "content").
                SetFieldBoost("title", 2.5),
        ).
        FilterClause(
            search.NewTagQuery(category),
            search.NewRangeQuery("price").LessThanOrEqual(maxPrice),
        )

    return idx.Search(ctx, search.Request{
        Query:           q,
        Limit:           20,
        HighlightFields: []string{"title", "content"},
    })
}
```

---

### 12. Enterprise Streaming Broker Routing (Kafka & RabbitMQ)

```go
package main

import (
    "context"

    "github.com/dhawalhost/gokit/eventbus"
    "github.com/dhawalhost/gokit/eventbus/kafka"
    "github.com/dhawalhost/gokit/eventbus/rabbitmq"
)

func InitEnterpriseBrokers(ctx context.Context) (eventbus.Bus, eventbus.Bus, error) {
    // 1. High-throughput Kafka bus with deterministic partition key routing & compression
    kafkaBus, err := kafka.New(
        kafka.WithBrokers("kafka-broker-1:9092", "kafka-broker-2:9092"),
        kafka.WithClientID("order-fulfillment-service"),
        kafka.WithBatchSize(100),
    )
    if err != nil {
        return nil, nil, err
    }

    // 2. RabbitMQ AMQP bus with prefetch QoS and competing consumer groups
    amqpBus, err := rabbitmq.New(
        rabbitmq.WithURL("amqp://guest:guest@rabbitmq:5672/"),
        rabbitmq.WithExchange("domain.events", "topic"),
        rabbitmq.WithPrefetchCount(50),
    )
    if err != nil {
        return nil, nil, err
    }

    return kafkaBus, amqpBus, nil
}
```

---

### 13. Egress Bandwidth Optimization & Zero-GC Multi-Codec Compression

```go
package main

import (
    "net/http"

    "github.com/dhawalhost/gokit/compression"
    "github.com/dhawalhost/gokit/router"
)

func SetupCompressedGateway() http.Handler {
    r := router.New()

    // Multi-codec response compression + transparent request body decompression
    r.Use(compression.Middleware(
        // Avoid compressing tiny payloads (< 1KB) where header overhead inflates size
        compression.WithMinLength(1024),
        // Balance compression ratio vs CPU latency
        compression.WithLevel(compression.LevelDefault),
        // Server preference hierarchy: Zstd (Meta) > Brotli (Google) > Gzip > Deflate
        compression.WithEncodings(
            compression.EncodingZstandard,
            compression.EncodingBrotli,
            compression.EncodingGzip,
        ),
    ))

    r.Get("/api/v1/catalog/export", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        // Large 500 KB JSON payload automatically compressed down to ~65 KB with zstd
        w.Write(catalogExportJSON)
    })

    return r
}
```

---

### 14. Event-Sourced Banking Ledger & CQRS Projection Replay

```go
package main

import (
    "context"
    "fmt"

    "github.com/dhawalhost/gokit/eventstore"
)

type BankAccount struct {
    eventstore.BaseAggregate
    Balance float64
}

func (a *BankAccount) AggregateType() string { return "BankAccount" }

func (a *BankAccount) ApplyChange(evt eventstore.Event) {
    if evt.Type == "MoneyDeposited" {
        var d struct{ Amount float64 }
        _ = evt.UnmarshalData(&d)
        a.Balance += d.Amount
    }
}

func ExecuteFinancialLedger(ctx context.Context) error {
    store := eventstore.NewMemoryStore()
    snapStore := eventstore.NewMemorySnapshotStore()

    // Aggregate Repository with automatic snapshotting every 50 events
    repo := eventstore.NewRepository[*BankAccount](
        store,
        func() *BankAccount { return &BankAccount{} },
        eventstore.WithRepositorySnapshotStore(snapStore, eventstore.EveryNEvents(50)),
    )

    // Append-only events preserve complete auditability with optimistic concurrency
    acc := &BankAccount{}
    acc.SetID("acc-us-east-901")
    evt, _ := eventstore.NewEvent(acc.AggregateID(), "MoneyDeposited", map[string]float64{"amount": 1000})
    acc.TrackChange(acc, evt)
    if err := repo.Save(ctx, acc); err != nil {
        return err
    }

    // Load rehydrates state by reading snapshot + delta events in forward sequence
    loaded, err := repo.Load(ctx, "acc-us-east-901")
    if err != nil {
        return err
    }
    fmt.Printf("Rehydrated balance: %.2f (Version: %d)\n", loaded.Balance, loaded.Version())
    return nil
}
```

---

### 15. Zero-Trust Session Management & Anti-Hijacking Defense

```go
package main

import (
    "net/http"
    "time"

    "github.com/dhawalhost/gokit/router"
    "github.com/dhawalhost/gokit/session"
)

func SetupSecureSessionGateway() http.Handler {
    store := session.NewMemoryStore(time.Minute)

    mgr, _ := session.NewManager(store,
        session.WithCookieName("__Host-sid"),
        session.WithCookieSecure(true),
        session.WithCookieHTTPOnly(true),
        session.WithCookieSameSite(http.SameSiteStrictMode),
        session.WithCookiePath("/"),
        session.WithIdleTimeout(15*time.Minute),      // 15m sliding inactivity window
        session.WithAbsoluteTimeout(12*time.Hour),    // 12h maximum hard lifetime
        session.WithAntiHijacking(true),              // Binds session to /24 IP subnet & User-Agent hash
    )

    r := router.New()
    r.Use(mgr.Middleware())

    r.Post("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
        s := session.FromContext(r.Context())
        s.Set("authenticated", true)
        s.Set("user_id", "usr_10294")
        s.Set("tenant_id", "tenant_acme")
        
        // Critical: Re-keys session ID on privilege transition to neutralize session fixation attacks
        s.Regenerate()
        w.WriteHeader(http.StatusOK)
    })

    r.Get("/api/v1/user/me", func(w http.ResponseWriter, r *http.Request) {
        s := session.FromContext(r.Context())
        if !s.GetBool("authenticated") {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        w.Write([]byte("User ID: " + s.GetString("user_id")))
    })

    return r
}
```

---

### 16. Autonomous Memory Pressure Watchdog & Authenticated Diagnostic Profiling

```go
package main

import (
    "context"
    "log"
    "net/http"
    "time"

    "github.com/dhawalhost/gokit/diagnostics"
)

func SetupProductionDiagnostics(ctx context.Context) http.Handler {
    // 1. Autonomous Memory Pressure Watchdog
    // Dumps heap profile if HeapAlloc crosses 1.5 GB before Linux cgroup OOM killer strikes
    watchdog := diagnostics.NewMemoryWatchdog(
        diagnostics.WithMaxHeapAlloc(1500*1024*1024),
        diagnostics.WithCooldown(5*time.Minute),      // Rate limits dumps to preserve disk IOPS
        diagnostics.WithDumpDir("/var/log/app/heaps"),
        diagnostics.WithWatermarkHook(func(s diagnostics.MemorySummary, dumpPath string) {
            log.Printf("ALERT: Heap watermark breached: %d MB. Profile written to %s\n",
                s.HeapAlloc/(1024*1024), dumpPath)
        }),
    )
    _ = watchdog.Start(ctx)

    // 2. Zero-Trust Diagnostic Server: Pprof & Runtime summary protected by Bearer token & Subnet allowlist
    diagHandler := diagnostics.NewHandler(
        diagnostics.WithBearerToken("sec-ops-token-884"),
        diagnostics.WithAllowedCIDRs("10.0.0.0/8", "127.0.0.1/32"),
        diagnostics.WithPathPrefix("/debug"),
        diagnostics.WithWatchdog(watchdog),
    )

    return diagHandler
}
```

---

### 17. High-Throughput Stream ETL Pipeline with Backpressure & Dead-Letter Routing

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/dhawalhost/gokit/pipeline"
)

type EventLog struct {
    EventID   string
    UserID    string
    Action    string
    Timestamp time.Time
}

type AggregatedSummary struct {
    WindowStart time.Time
    WindowEnd   time.Time
    EventCount  int
    UniqueUsers int
}

func RunStreamingAnalyticsPipeline(ctx context.Context, in <-chan EventLog) error {
    dlq := pipeline.NewMemoryDeadLetterQueue(10000)

    // Construct stream ETL pipeline with 128-element buffer backpressure & DLQ
    p := pipeline.New[EventLog, AggregatedSummary]("streaming-analytics",
        pipeline.WithPipelineBufferSize[EventLog, AggregatedSummary](128),
        pipeline.WithPipelineDLQ[EventLog, AggregatedSummary](dlq),
    )

    // Stage 1: Filter malformed or empty events
    pipeline.AddStage(p, pipeline.Filter("valid-events", func(c context.Context, item EventLog) bool {
        return item.EventID != "" && item.UserID != ""
    }))

    // Stage 2: Parallel enrichment worker pool (8 workers)
    pipeline.AddStage(p, pipeline.Parallel("enricher", 8, func(c context.Context, item EventLog) (EventLog, error) {
        // Hydrate or normalize record
        return item, nil
    }, pipeline.WithRetry(2, 10*time.Millisecond)))

    // Stage 3: Tumbling temporal window (10-second aggregations)
    pipeline.AddStage(p, pipeline.TumblingWindow("10s-aggregator", 10*time.Second,
        func(c context.Context, win pipeline.Window[EventLog]) (AggregatedSummary, error) {
            userSet := make(map[string]struct{})
            for _, ev := range win.Items {
                userSet[ev.UserID] = struct{}{}
            }
            return AggregatedSummary{
                WindowStart: win.StartTime,
                WindowEnd:   win.EndTime,
                EventCount:  len(win.Items),
                UniqueUsers: len(userSet),
            }, nil
        },
    ))

    // Stream results directly into sink consumer
    return p.Sink(ctx, in, func(c context.Context, summary AggregatedSummary) error {
        fmt.Printf("[%s - %s] Processed %d events across %d users\n",
            summary.WindowStart.Format("15:04:05"),
            summary.WindowEnd.Format("15:04:05"),
            summary.EventCount, summary.UniqueUsers)
        return nil
    })
}
```

---

## 🧠 Back-of-the-Envelope Capacity Rules to Memorize

- **$1 \text{ day} \approx 10^5 \text{ seconds}$** ($86,400\text{s}$).
- $1,000 \text{ events/sec} \approx 10^8 \text{ events/day}$ ($100\text{M/day}$).
- $10^5 \text{ QPS} \ (100\text{k req/s})$ at $1\text{ KB}$ payloads = $\sim 100\text{ MB/s}$ bandwidth.
- **Diurnal Peak Multiplier:** Always dimension capacity for **$5\times - 10\times$ peak over average**.
- **Working Set Sizing:** $80\%$ of reads hit $20\%$ of data (Pareto principle). Size L1/L2 cache RAM to fit $20\%$ of daily working set.

---

## 🎨 Diagram Discipline Under Time

In a 45–60 minute senior architecture review, the diagram is a decision-forcing tool, not an artistic illustration.

### What to Draw (In Order):

1. **Client $\to$ Edge $\to$ Service Boundary**: Separate lines for read vs write paths when they diverge.
2. **Stateful Stores with Consistency Labels**: Label primary DB, cache, or queue with its consistency model (e.g., `Sync Replica`, `Async Lag < 1s`, `TTL = 60s`).
3. **Partition / Shard Key on the Hottest Store**: Explicitly write the key (e.g. `Shard Key: user_id`, `Stripe Key: post_id:slot_N`).
4. **Critical Async Hops**: Queue $\to$ Consumer with semantics (`At-least-once + Idempotency`).
5. **Failure Callouts**: Circle the single points of failure (e.g., _Leader election failover_, _Hot partition_, _Stampede on expiry_).

### What to Skip:

- Every microservice name (use 3 logical tiers: API, Worker, Store).
- Redundant mobile vs web client boxes unless traffic shape diverges.
- Schema fields, UI components, CI/CD pipelines.

---

## ⏱️ 25-Minute Drill: High-QPS Read API

**Scenario:** Public product read endpoint `GET /v1/products/{id}` serving 5M DAU, peak 150k QPS (3× holiday spike), 100M SKUs, 4 KB payload, p99 < 50ms at edge.

```
[ Client ]
    │
    ▼
[ CloudFront / Fastly CDN ] ──── (90% edge hit rate: absorbs 135k QPS, ~540 MB/s egress)
    │ (15k QPS origin miss)
    ▼
[ L4/L7 Load Balancer ]
    │
    ▼
[ GoKit API Fleet ] ────────── [ In-Memory L1 Cache ] (80% hit on remainder: ~50ns)
    │ (3k QPS miss)
    ▼
[ Redis L2 Cluster ] ───────── (SingleFlight coalesces stampedes)
    │ (0.3k QPS cold miss)
    ▼
[ Aurora / PG Replicas ] ───── (Read-Your-Writes sticky routing on updates)
```

- **Working Set:** $5\%$ hot SKUs = $5\text{M} \times 4\text{ KB} \approx 20\text{ GB}$ (comfortably fits in distributed Redis L2 RAM).
- **Origin Protection:** A $99\%$ combined cache hit rate drops 150k QPS down to just $1.5\text{k QPS}$ hitting the primary database.
- **Viral SKU Hot Key:** If one product takes $40\%$ of traffic ($60\text{k QPS}$), hash partitioning does **not** help. The fix is local in-process L1 caching ([`cache.TwoTierCache`](cache/README.md)) to absorb the key in RAM.

---

## ⚖️ Load Balancing & Graceful Connection Draining

1. **L4 vs L7 Trade-offs**:
   - **L4 (TCP/IP)**: Ultra-cheap, connection-aware, high packet throughput.
   - **L7 (HTTP/gRPC)**: Route on path/headers, TLS termination, WAF, canaries. Beware CPU and connection churn at 100k+ RPS.
2. **The Connection Drain Race**:
   - When deploying new pods, mark instances out of the pool, stop accepting new connections, and wait for in-flight requests to complete before killing the process.
   - **The Timeout Mismatch Bug:** If Load Balancer idle timeout is 60s but App keep-alive is 75s, silent connection resets occur under low traffic. `gokit/server` defaults `idleTimeout: 120s` to ensure edge LBs terminate idle connections first.
3. **Avoid the "Sticky Session Tax"**:
   - IP-hash or cookie-based sticky routing creates uneven pod load (e.g. corporate NAT hashing thousands of users to one node) and breaks autoscaling. Use stateless JWTs or external session stores ([`cache.RedisCache`](cache/README.md)).

---

## 🔄 Cache Consistency Policies & Invalidation

| Cache Policy                  | Read Path         | Write Path                      | Inconsistency Window                 | Best Used For                                         |
| :---------------------------- | :---------------- | :------------------------------ | :----------------------------------- | :---------------------------------------------------- |
| **TTL-Only**                  | Fast cache hit    | Update DB; wait for expiry      | Up to TTL duration                   | High read:write ratios (product catalogs, news feeds) |
| **Write-Through**             | Fast cache hit    | Synchronously write Cache + DB  | Zero (if single writer/lease)        | Critical session state, auth tokens                   |
| **Write-Behind (Write-Back)** | Instant cache hit | Ack on cache; async queue to DB | Seconds to minutes (durability risk) | Bulk metrics, click analytics, draft saves            |
| **Lease / Atomic Reserve**    | Check cache count | Atomic Redis Lua script         | Zero oversell                        | Flash sale inventory, balance reservation             |

### Flash Sale Inventory Reservation

To prevent overselling when 50,000 users click "Buy" in 3 seconds, use atomic Lua decrement:

```go
// Atomic in Redis: 1 round-trip, zero distributed lock overhead, never oversells
remaining, err := cache.AtomicDecrementWithFloor(ctx, redisClient, "inventory:sku:101", 1, 0)
if errors.Is(err, cache.ErrInsufficientQuantity) {
    return ErrSoldOut
}
```

---

## 🌐 CDN & Multi-Region Edge

A CDN is not just putting CloudFront in front. You must design cache keys, invalidation rules, and origin shielding to keep hit rates $\ge 95\%$ while preventing personalized PII cache poisoning:

1. **Origin Shielding**: A regional mid-tier cache between CDN edge PoPs and your origin server. When 150 PoPs miss cache simultaneously for a new viral asset, the Origin Shield collapses them into **1 request to your primary database**, eliminating origin stampedes.
2. **PII Cache Poisoning Guard**: If a route contains an `Authorization` or `Cookie` header, it must never be cached publicly. Use [`middleware.CacheControl`](middleware/README.md):
   ```go
   // Forces Cache-Control: private, no-store if auth headers exist; sets public + SWR for anonymous
   r.Use(middleware.CacheControl(middleware.CacheControlConfig{
       Public:               true,
       MaxAge:               5 * time.Minute,
       StaleWhileRevalidate: 30 * time.Second,
       Vary:                 []string{"Accept-Encoding"},
   }))
   ```

---

## 🔀 Partitioning & Consistent Hashing

### Partitioning Strategies

| Strategy      | Advantages                                       | Fatal Failure Mode                              | Mitigation                                         |
| :------------ | :----------------------------------------------- | :---------------------------------------------- | :------------------------------------------------- |
| **Range**     | Efficient range scans (`created_at`, `id`)       | Write hotspotting on latest tablet              | Combine with hash prefix: `(shard_id, created_at)` |
| **Hash**      | Uniform distribution for random IDs              | Hot keys (celebrity accounts, viral SKUs)       | Key salting via `cache.SaltKey` + local L1 cache   |
| **Directory** | Granular rebalancing; can move single hot tenant | Directory service becomes critical latency SPOF | Cache lookup directory heavily in memory           |

### Consistent Hashing with Virtual Nodes (VNodes)

- **Naive Modulo (`key % N`)**: Adding 1 node to a 100-node cluster remaps $\frac{N}{N+1} \approx 99\%$ of all keys, triggering a petabyte-scale rebalance storm and freezing the cluster.
- **Consistent Hash Ring with VNodes**: Adding 1 node moves only $\sim 1\%$ of data. Virtual nodes (e.g. 100 vnodes per physical node) distribute key arcs smoothly across physical hosts.

```go
ring := cache.NewConsistentHashRing(100) // 100 vnodes per host
ring.Add("cache-node-1", "cache-node-2", "cache-node-3")

// Deterministic clockwise node routing
targetNode, err := ring.Get("user:session:10092")
```

---

## 🔁 Replication: Sync vs Async, Quorums & Lag

Replication is not a binary choice; your acknowledgment point sets your RPO and write latency:

- **Same-AZ Round Trip**: $\sim 0.5\text{ms}$.
- **Cross-AZ Round Trip**: $\sim 1-2\text{ms}$ (semi-sync across AZs is cheap and provides $\text{RPO} \approx 0$).
- **Cross-Region Round Trip**: $60-150\text{ms}$ (synchronous cross-region writes will destroy write p99).
- **Quorum Mathematics**: $W + R > N$. With $N=3, W=2, R=2$, the system tolerates 1 node failure for both reads and writes.
- **Monotonic Reads & Ghost Data**: Prevent users from seeing time travel (missing posts upon refresh) using [`database.ReadYourWritesMiddleware`](database/README.md).

---

## 👑 Leader Election & Split-Brain Fencing

Electing a leader is easy; preventing an old paused leader from corrupting state is hard.

### The TOCTOU Race & Why Redlock Fails

If a leader pauses for 12 seconds on a 10-second TTL (e.g. during a Stop-The-World GC pause or VM migration):

1. Lock TTL expires in Redis.
2. A new leader is elected and begins writing.
3. The old leader wakes up, assumes it still owns the lock, and executes its stale write, causing **split-brain silent data corruption**.

### The Solution: Monotonic Fencing Tokens

Every grant of leadership carries a strictly increasing epoch (fencing token). Storage layers reject writes with stale tokens:

```go
// 1. Acquire distributed lock with monotonic fencing token
lock, err := locker.Obtain(ctx, "leader:scheduler", 10*time.Second)
if err != nil {
    return err
}
fencedLock := lock.(distlock.FencedLock)
fencingToken := fencedLock.FencingToken() // strictly monotonic: e.g. 42

// 2. Downstream storage enforces fencing check:
// UPDATE ledgers SET balance = balance - 100, last_fencing_token = :fencingToken
// WHERE id = :id AND last_fencing_token < :fencingToken
//
// If old leader resumes with token 41, the DB rejects the write!
```

---

## 🗄️ Sharded SQL in Production (When it Still Wins)

Sharded SQL means operating many independent MySQL or Postgres primary instances. Rather than jumping to NoSQL, companies like Instagram, Shopify (pods), Slack (Vitess), Notion, Figma, and Stripe scale SQL horizontally.

### The Golden Rule
> **The shard key is your transaction boundary.** Pick a key (`tenant_id`, `user_id`, `workspace_id`) so that **$\ge 95\%$** of reads, writes, and invariants live inside a single shard. You retain full ACID, joins, secondary indexes, and decades of mature tooling.

```
┌─────────────────────────────────────────────────────────────────┐
│               Application Layer / Routing Proxy                 │
│              (ExtractShardID: O(1) bitshift route)              │
└─────────────────────────────────────────────────────────────────┘
         │                                       │
         ▼                                       ▼
┌─────────────────────────────────┐     ┌─────────────────────────────────┐
│       Physical Host 1           │     │       Physical Host 2           │
│ ┌──────────────┬──────────────┐ │     │ ┌──────────────┬──────────────┐ │
│ │ Logical S001 │ Logical S002 │ │     │ │ Logical S003 │ Logical S004 │ │
│ └──────────────┴──────────────┘ │     │ └──────────────┴──────────────┘ │
└─────────────────────────────────┘     └─────────────────────────────────┘
```

### Senior Mechanics
1. **Logical Shards $\ne$ Physical Hosts**: Pre-split into hundreds or thousands of logical shards (e.g., Notion runs 480 logical shards on 32 physical Postgres instances). Resharding moves entire logical databases, never rehashing row-by-row.
2. **Global 64-bit IDs with Embedded Routing**: Instagram-style 64-bit ID layout:
   - **41 bits**: Millisecond timestamp since custom epoch (~69 years)
   - **13 bits**: Logical shard ID ($0 - 8191$)
   - **10 bits**: Monotonic sequence ($1024$ IDs/ms per shard)
   - Implemented via [`idgen.NewShardIDGenerator(shardID)`](idgen/README.md) and zero-cost routing via [`idgen.ExtractShardID(id)`](idgen/README.md).
3. **Scatter-Gather Tail Latency**:
   $$\text{Tail Probability} = 1 - (1 - p)^N$$
   If each shard has a $p_{99} = 10\text{ms}$ and a query fans out to 100 shards:
   $$1 - 0.99^{100} \approx 63.4\%$$
   Roughly **$63\%$ of fan-out queries hit at least one shard's tail latency!** Unrouted fan-out queries belong in secondary analytics/search stores fed by CDC, never on the transactional hot path.
4. **Connection Pool Management**: 500 app pods $\times$ 64 shards $\times$ pool of 10 $= 320,000$ connections. Postgres process-per-connection architecture will collapse. Place a connection pooler or proxy (PgBouncer, Vitess `vtgate`) in front.

### Hard Follow-up: Multi-Tenant SaaS Whale Customer
- **Problem**: 64 Postgres shards keyed by `workspace_id`. Largest customer is 40% of one shard's writes and growing 3x/year. They demand cross-workspace search and globally unique project slugs.
- **This Quarter**:
  1. Isolate the whale tenant to its own dedicated physical shard by migrating its logical shard.
  2. Introduce a global directory lookup table (`slug -> workspace_id`) with a reserve-then-commit pattern.
  3. Pipe workspace CDC into an Elasticsearch/OpenSearch index for cross-workspace search.
- **Next Year**: Sub-shard the whale workspace by `project_id`.
- **Refuse on Transactional Path**: Refuse 2PC cross-shard distributed transactions and synchronous scatter-gather search queries.

### Zero-Downtime Resharding Runbook (16 to 64 Physical Hosts under 20k writes/s)
1. **Provision & Replicate**: Spin up 48 new physical Postgres hosts. Configure logical replication / CDC streams from the 16 source hosts for the logical shards moving to new nodes.
2. **Backfill & Catch-up**: Backfill snapshot data; tail WAL to reach near-zero replication lag ($< 100\text{ms}$).
3. **Data Verification**: Run parallel streaming checksums (Merkle-tree hash comparison or row-count + primary key range checksums) per logical shard.
4. **Cutover Window ($\le 5\text{s}$ Write Freeze)**:
   - Put application pods into temporary read-only mode for the moving logical shards (or reject writes with 503 retryable).
   - Wait for logical replication lag to reach exactly 0.
   - Promote new physical hosts to primaries.
   - Update proxy routing map (e.g. `vtgate` / Consul) to point to the 64 new physical hosts.
   - Re-enable writes.
5. **Point of No Return**: The exact instant the first write commits to the new primaries.
6. **Rollback Plan**: If verification fails or promotion errors occur before the write freeze ends, revert routing to original 16 hosts.

---

## ⚡ NoSQL Under Write Amplification (Wide-Column & KV / LSM Patterns)

In Log-Structured Merge-tree (LSM) systems (Cassandra, RocksDB, ScyllaDB, Bigtable), write throughput comes at the cost of **write amplification**:

$$\text{Write Amplification Multiplier} = \frac{\text{Bytes Written to Disk/Replication}}{\text{Logical Bytes Written by Client}}$$

```
Logical Write ──> MemTable (RAM) + WAL (Disk 1×)
                      │
                      ▼ Flush
                 SSTable Level 0 (Disk 1×)
                      │
                      ▼ Compaction Merges
                 SSTable Level 1..N (Disk 2× - 10×)
Total Multiplier: 5× - 10× Disk IOPS / Bandwidth
```

### Production Math & Hardware Sizing
- **Logical Load**: $100\text{k writes/sec} \times 500\text{ bytes} = 50\text{ MB/sec}$.
- **Physical Reality**: With $5\times - 10\times$ write amplification, disk and replication traffic sustained is **$250\text{ MB/sec} - 500\text{ MB/sec}$**!
- **Failure Mode (Compaction Debt)**: If disk I/O or compaction threads cannot keep up with flush rate, SSTables accumulate. Reads must touch dozens of SSTables, p99 spikes, disk fills with obsolete SSTables, and writes stall to let compaction catch up. Retrying clients compound the catastrophe.

### Tombstone Density & Wide Row Disasters
- Deletes, overwrites, and TTL expirations don't free disk space immediately—they write **tombstones**.
- Range scans scanning through millions of tombstones burn CPU, exhaust JVM heap, and trigger `TombstoneOverwhelmingException`.
- **Compaction Strategy Selection**:
  - **Size-Tiered (STCS)**: Optimal for write-heavy workloads with few reads; requires 50% free disk space for compaction.
  - **Leveled (LCS)**: Optimal for read-heavy workloads; lower read amplification, higher write amplification ($10\times - 30\times$).
  - **Time-Window (TWCS)**: Mandatory for TTL time-series data. Drops entire SSTables after window expires with zero compaction write amplification!

### Hard Follow-up: Cassandra p99 Latency Spikes at 70% Disk
1. **Inspection Order**:
   - `nodetool compactionstats`: Check pending compaction tasks (is compaction debt backlogged?).
   - `nodetool tablestats`: Inspect tombstone counts per read and partition sizes (partitions $> 100\text{MB}$ indicate dangerous wide rows).
   - Garbage Collection logs: Check for Stop-The-World (STW) JVM pauses $> 500\text{ms}$.
   - Disk I/O saturation: Use `iostat -xz 1` to verify if disk utilization is pegged at 100%.
2. **Remediations Before Adding Nodes**:
   - Switch TTL time-series tables to `TimeWindowCompactionStrategy` (TWCS).
   - Lower `tombstone_failure_threshold` and adjust schema to bucket hot partition keys by date/hour (e.g. `(tenant_id, bucket_day)`).
   - Smooth write spikes by fronting writes with an amortizing batcher ([`jobqueue.NewBatcher`](jobqueue/README.md)).

---

## 🔍 Secondary Indexes & Inverted Search at Scale

A secondary index is a second access path partitioned differently from the primary key. Adding an index is **never a free read optimization**—it adds write amplification, cross-shard network hops, and eventual consistency lag.

```
┌─────────────────────────────────┐           ┌─────────────────────────────────┐
│ Primary Shard (Key: user_id)   │           │ GSI Shard (Key: email)          │
│ ┌─────────┬───────────────────┐ │   Async   │ ┌──────────┬──────────────────┐ │
│ │ user_id │ email             │ │ ────────> │ │ email    │ user_id          │ │
│ └─────────┴───────────────────┘ │    CDC    │ └──────────┴──────────────────┘ │
└─────────────────────────────────┘           └─────────────────────────────────┘
```

### Local vs Global Indexes
| Dimension | Local Secondary Index (LSI) | Global Secondary Index (GSI) | Inverted Index (Search Engine) |
|---|---|---|---|
| **Partitioning** | Same partition as primary key | Partitioned by indexed attribute | Distributed posting lists (Lucene) |
| **Write Cost** | Single shard, atomic with row | Asynchronous write / 2-shard cross-write | Asynchronous segment creation / merge |
| **Global Query** | **Scatter-gather across all shards** | Single shard lookup ($O(1)$) | Scatter-gather with score merging |
| **Consistency** | Strong / Immediate | Eventual ($50\text{ms} - 500\text{ms}$ lag) | Eventual ($100\text{ms} - 2\text{s}$ refresh) |

### The Dual-Write Trap
> [!CAUTION]
> **Never dual-write synchronously** from application code (`DB.Save()` then `Elasticsearch.Index()`). If the process crashes between writes or Elasticsearch returns a timeout, your search index is permanently desynchronized.
> Always use **Change-Data-Capture (CDC)** or the Transactional Outbox pattern ([`outbox.NewOutbox`](outbox/README.md)) to guarantee reliable ingestion into search indexes.

### Hard Follow-up: Sharded Users with Low-Latency Search
- **Requirements**: Primary key is `user_id`. Needs fast login by email and prefix search by user name.
- **Architecture**:
  1. **Login by Email**: Store a global directory lookup table (`email_hash -> user_id`) cached in Redis. Writes use a reserve-then-commit pattern to enforce uniqueness.
  2. **Prefix Search by Name**: Ingest user mutation events into OpenSearch/Elasticsearch via Debezium CDC from Postgres WAL.
- **Consistency Contract**: Login by email is strongly consistent. Profile name search is eventually consistent with a 200ms SLO.
- **Failure Mode**: If the email directory shard is down, fallback to login via phone/SSO or return 503 for email login while primary `user_id` CRUD remains 100% operational.

---

## 📬 Queues for Throughput (Batching, Prefetch, Backpressure)

A queue is not just async glue—it is a **throughput and admission controller**. It decouples callers, absorbs peak bursts, and dictates system behavior under overload.

```
Producers ──> [ Batcher (Linger: 50ms) ] ──> Broker Topic ──> [ Consumer (Prefetch: K) ] ──> Sink
                     │                                                   │
               Amortize I/O                                       In-flight Credit
```

### 1. Batching & Linger
Writing individual 1 KB messages burns network packets and disk syncs. With a 50ms linger and 1,000 msgs/s per partition, each batch coalesces ~50 messages, boosting throughput by **$5\times - 20\times$** at the expense of a 50ms latency floor.
- Implemented in `gokit` via [`jobqueue.NewBatcher`](jobqueue/README.md).

### 2. Prefetch (In-Flight Credit) Sizing
Prefetch determines how many unacknowledged messages a consumer holds:
$$\text{Optimal Prefetch} \approx \frac{\text{Target Latency Budget}}{\text{Per-Message Processing Time}}$$
- **Too Low**: Workers starve on network roundtrips.
- **Too High**: One slow consumer holds a massive queue, runs out of memory, and causes a rebalance storm when killed by OOM.
- **Memory Ceiling**: $N_{\text{consumers}} \times \text{Prefetch} \times \text{Msg Size} \le \text{Allocated RAM}$.

### 3. Backpressure Triad
1. **Broker High Watermark**: Reject produces when disk capacity reaches 85%.
2. **Consumer Lag SLO**: Alert when consumer lag exceeds time-to-drain budget.
3. **Edge Admission Control**: Shed traffic or return 429 at the API gateway ([`ratelimit`](ratelimit/README.md) and [`bulkhead`](bulkhead/README.md)) before unbuffered queues crash nodes.

### Hard Follow-up: 40-Minute Growing Queue Lag Triage
- **First 10 Minutes**:
  1. Verify consumer processing duration (did a downstream DB query spike from 5ms to 500ms?).
  2. Check for poison messages causing repeated crashes / unhandled panics.
  3. Verify consumer group health (are rebalances thrashing the consumer pool?).
- **Ship Tonight**:
  1. Roll back the latest deploy if processing time degraded.
  2. Route poison messages to Dead Letter Queue (DLQ) after 3 failures.
  3. Scale consumer instances (if downstream database can absorb the increased load).
- **Ship Tomorrow**:
  1. Add missing DB index or introduce read replicas.
  2. Implement partition key salting to smooth out whale tenant skew.

---

## 🛡️ The Exactly-Once Myth: At-Least-Once + Idempotency in Practice

In distributed systems communicating over unreliable networks, **"exactly-once delivery" is an impossible myth** (Two Generals' Problem). Networks partition, acks get dropped, and servers crash after performing a side effect.

### The Production Truth
$$\text{At-Least-Once Delivery} + \text{Idempotent Consumer Processing} = \text{Effectively-Once Execution}$$

```
Client ──[ POST /orders (Key: ord_123:pay) ]──> Gateway
                                                    │
                  ┌─────────────────────────────────┴─────────────────────────────────┐
                  ▼                                                                   ▼
       First Attempt (200 OK)                                              Retry (Network Dropped Ack)
┌──────────────────────────────────────┐                            ┌──────────────────────────────────────┐
│ INSERT ON CONFLICT DO NOTHING (Lock) │                            │ Lookup Key -> Status: COMPLETED      │
│ Charge Credit Card                   │                            │ Replay Previous Response (No Charge) │
│ Mark Key: COMPLETED                  │                            └──────────────────────────────────────┘
└──────────────────────────────────────┘
```

### Idempotent Consumer Mechanics
1. **Stable Idempotency Key**: Formed by the caller using business identifiers (`order_id:action:v1` or UUIDv7).
2. **Atomic Reserve Step**:
   ```sql
   INSERT INTO idempotency_records (key, status, created_at)
   VALUES ('pay_9941a', 'PROCESSING', NOW())
   ON CONFLICT (key) DO NOTHING;
   ```
   If row count $= 0$, an in-flight request is already processing or completed.
3. **Transactional Outbox**: Store domain entities and outgoing event messages in the **same database transaction** ([`outbox.NewOutbox`](outbox/README.md)). A background relay tails the table via `SELECT ... FOR UPDATE SKIP LOCKED` and publishes to Kafka.

### Hard Follow-up: Payment Webhook Re-Delivery after Successful Charge
- **Scenario**: A payment webhook charged the user's card, but network dropped the broker ack. The broker redelivers the same event 30 seconds later.
- **Consumer Path**:
  1. Check idempotency store with key `pay_event:<event_id>` (TTL 7 days).
  2. If status is `COMPLETED`, immediately ack the message and return stored transaction receipt (no re-execution).
  3. **The Edge Case (Charge Succeeded, but DB crashed before writing idempotency record)**:
     - The consumer queries the upstream payment gateway (Stripe/Adyen) using the client idempotency key.
     - The payment gateway returns the existing charge object instead of charging again.
     - The consumer writes the missing `COMPLETED` record to the database and acks the queue.

---

## 🚪 High-Throughput API Gateway & Parallel BFF Scatter-Gather

Modern microservices rely on an edge or internal **API Gateway** to terminate TLS, enforce authentication, balance traffic across backend replicas, and aggregate heterogeneous downstream microservices into a single consolidated payload (Backend-For-Frontend / BFF pattern).

```
[ Mobile / Web Client ]
          │
          ▼
┌─────────────────────────────────────────────────────────────┐
│                 gokit/gateway Reverse Proxy                 │
│  - X-Forwarded-For / X-Request-ID Header Sanitization       │
│  - Multi-Strategy Load Balancing (LeastConn, Weighted, etc) │
│  - Scatter-Gather Parallel Microservice Fan-out             │
└───────────────┬─────────────────────────────┬───────────────┘
                │                             │
       (BFF Parallel Fanout)         (Reverse Proxy Passthrough)
                │                             │
    ┌───────────┼───────────┐                 ▼
    ▼           ▼           ▼         ┌─────────────────┐
┌───────┐   ┌───────┐   ┌───────┐     │ Order Service   │
│UserSvc│   │FeedSvc│   │AdSvc  │     └─────────────────┘
└───────┘   └───────┘   └───────┘
```

### 1. Reverse Proxy Mechanics & Idle Pool Alignment
- **The Keep-Alive Deadlock:** If the downstream microservice closes an idle TCP connection without the edge proxy realizing it, the next proxied request encounters an abrupt `EOF` or `connection reset by peer`.
- [`gateway.NewProxy`](gateway/README.md) utilizes modern Go `net/http/httputil.ReverseProxy` with explicit `Rewrite` hooks (preventing hop-by-hop header leaks) and sets configurable connection pooling (`MaxIdleConnsPerHost`, `IdleConnTimeout`).

### 2. Multi-Strategy Load Balancing
- **Round-Robin**: Uniform distribution for homogeneous stateless workers.
- **Least-Connection**: Routes traffic to the pod currently handling the fewest concurrent in-flight requests; ideal when request processing times vary by orders of magnitude (e.g. fast metadata queries vs heavy PDF generations).
- **IP-Hash**: Deterministic affinity routing ensuring consecutive requests from the same client IP land on the same host (e.g. for warm local caching), with fallback when nodes fail.
- Implemented via [`gateway.NewLoadBalancer`](gateway/README.md).

### 3. Scatter-Gather Parallel Fan-Out (BFF)
When rendering a mobile dashboard requiring data from 4 separate microservices (Profile, Orders, Notifications, Recommendations):
- **Sequential Calls:** Total latency $= \sum_{i=1}^N T_i$. A slow recommendation service (500ms) pushes total response time $> 800\text{ms}$.
- **Parallel Fan-out with SLA Budget:** Total latency $= \max(T_1, T_2, \dots) \le \text{Timeout}$.
- [`gateway.ScatterGather`](gateway/README.md) executes tasks in parallel with `sync.WaitGroup`, enforces a strict overall context deadline, and allows non-critical components to degrade gracefully while essential data (Profile and Orders) returns successfully.

---

## 📦 Write Buffering vs Read Coalescing (The DataLoader Pattern)

I/O roundtrips—whether network hops to a database or disk writes—are the primary bottleneck in modern high-throughput architectures. The [`batcher`](batcher/README.md) package provides dual solutions for write and read optimization.

```
       WRITE PATH (Batch Accumulator)                     READ PATH (DataLoader)
50k writes/s                                        10k concurrent resolvers
      │                                                         │
      ▼                                                         ▼
┌──────────────────────────────┐              ┌──────────────────────────────────┐
│ batcher.BatchAccumulator     │              │ batcher.DataLoader               │
│ - Ring buffer / Channel      │              │ - 10ms Linger Coalescing         │
│ - MaxBatchSize: 500          │              │ - In-Memory TTL Cache            │
│ - Linger: 50ms               │              │ - Keys: ["u1", "u2", ...]        │
└──────────────┬───────────────┘              └─────────────────┬────────────────┘
               │                                                │
               ▼ Bulk Insert                                    ▼ Coalesced SELECT
    ┌──────────────────────┐                         ┌──────────────────────┐
    │  Postgres / Log Sink │                         │ Database / Redis     │
    └──────────────────────┘                         └──────────────────────┘
```

### 1. Write Path: The Batch Accumulator
Writing 50,000 individual audit records, analytics events, or metrics per second generates 50,000 distinct network packets and transaction commits, exhausting database WAL throughput.
- [`batcher.NewBatchAccumulator`](batcher/README.md) buffers items in memory and dispatches a bulk write when **either** `MaxBatchSize` is reached or `LingerDuration` expires.
- Provides `Flush()` for clean shutdown without data loss.

### 2. Read Path: The DataLoader Pattern
In GraphQL or nested REST APIs, rendering a list of 50 orders where each order references a `UserID` causes the infamous **N+1 query storm** (1 query for orders + 50 queries for individual users).
- [`batcher.NewDataLoader`](batcher/README.md) collects all `Load(key)` calls happening within a tiny window (e.g. 5–10ms) or single tick, deduplicates the keys, and invokes a single batch fetch function:
  $$\text{SELECT } * \text{ FROM users WHERE id IN } (k_1, k_2, \dots, k_N)$$
- Results are mapped back to their individual caller promises with optional per-request in-memory memoization.

---

## 🔐 Zero-Trust Secrets Management & Memory Safety

Production secrets (database passwords, API tokens, encryption keys) are high-value targets. Hardcoded strings, unredacted log lines, and memory scraping pose severe security vulnerabilities.

### 1. The Redaction & Memory Zeroing Invariant
- **Log Scraping:** Standard Go structs printed via `fmt.Printf("%+v", cfg)` or serialized with `json.Marshal` dump sensitive strings into plaintext log aggregators (Elasticsearch, CloudWatch).
- [`secrets.Secret`](secrets/README.md) overrides `String()`, `GoString()`, and implements `json.Marshaler` returning `"[REDACTED]"`.
- **Memory Residuals:** Garbage collectors do not overwrite deallocated heap bytes. A core dump or malicious memory scrape can recover passwords minutes after deallocation. [`Secret.Zero()`](secrets/README.md) cryptographically scrubs the underlying byte buffer with zeros upon cleanup.

### 2. Multi-Provider Fallback & Automated Rotation
- **Provider Priority:** Kubernetes Secret volumes (`FileProvider`) $\to$ Normalized Environment Variables (`EnvProvider`) $\to$ In-Memory Mocks for Tests (`MemoryProvider`).
- **Rotation without Restarts:** [`secrets.NewManager`](secrets/README.md) caches secrets in memory with a configurable TTL and runs background auto-refresh workers, pulling rotated credentials from upstream vaults without requiring pod termination.

---

## 🚀 Zero-Downtime Database Schema Migrations & Expand-Contract Pattern

Deploying database schema changes in high-availability environments requires coordinated execution and backward compatibility.

```
                         Kubernetes Rolling Update
             ┌───────────────────────┬───────────────────────┐
             ▼                       ▼                       ▼
      [ App Pod 1 ]           [ App Pod 2 ]           [ App Pod 3 ]
             │                       │                       │
             └───────────────────────┼───────────────────────┘
                                     │
                     Acquire Distributed Lock (TTL: 30s)
                                     │
                                     ▼
                      ┌─────────────────────────────┐
                      │    migration.Migrator       │
                      │  - schema_migrations table  │
                      │  - Atomic Transaction DDL   │
                      │  - Dirty State Detection    │
                      └──────────────┬──────────────┘
                                     │
                                     ▼
                            [ PostgreSQL / MySQL ]
```

### 1. The Multi-Replica Migration Race
When a new version of a microservice rolls out across 50 Kubernetes pods, all pods start concurrently. If each pod attempts to run `CREATE TABLE` or `ALTER TABLE` simultaneously:
- Database locks deadlock.
- Schema version tables collide with primary key violations.
- [`migration.WithLocker`](migration/README.md) integrates with [`distlock.Locker`](distlock/README.md), serializing execution so exactly **one pod** runs the migration while other replicas await completion.

### 2. The Expand and Contract Pattern (Parallel Run)
Never alter or drop a column in a single step while old code is still running:
1. **Expand**: Add the new column as nullable (`ALTER TABLE users ADD COLUMN full_name TEXT`). Deploy code version $V_1$ that writes to both old and new columns, reading from old.
2. **Backfill**: Migrate historical records in batches (`UPDATE users SET full_name = ... WHERE ...`).
3. **Switch**: Deploy code version $V_2$ reading and writing exclusively to `full_name`.
4. **Contract**: Drop the deprecated old column (`ALTER TABLE users DROP COLUMN first_name, DROP COLUMN last_name`).

### 3. Dirty Migration Recovery
If a DDL statement fails midway (e.g. unique constraint violation during an index creation):
- [`migration.Migrator`](migration/README.md) records `dirty = true` in `schema_migrations`.
- Subsequent deployments refuse to run on dirty databases, preventing compounded schema drift.
- Operators review the failure, manually fix the index, and invoke [`migrator.Force(version)`](migration/README.md) to restore clean state.

---

## 🔄 Finite State Machines & Optimistic Concurrency Invariants

Complex business processes (KYC approval, order delivery, loan underwriting) must never allow illegal transitions (e.g. shipping a cancelled order, or approving a rejected loan).

```
[ PENDING ] ──(PAYMENT_SUCCESS)──> [ PAID ] ──(SHIP_ORDER)──> [ SHIPPED ]
     │                                │                             │
     │ (CANCEL)                       │ (REFUND)                    │ (DELIVER)
     ▼                                ▼                             ▼
[ CANCELLED ]                   [ REFUNDED ]                  [ DELIVERED ]
```

### 1. Guard-Enforced Determinism
- Ad-hoc database string updates (`UPDATE orders SET status = 'DELIVERED' WHERE id = 1`) lack invariant enforcement.
- [`workflow.Engine`](workflow/README.md) registers allowed `Transition{From, To, Event}` tuples and executes conditional `Guard` functions before permitting any state mutation.

### 2. Optimistic Concurrency Control (Version Locking)
To prevent two concurrent requests from both transitioning an order simultaneously:
- Include a monotonic `version` column in the domain entity.
- The workflow engine validates that the entity's version matches before committing the transition:
  $$\text{UPDATE orders SET status = 'PAID', version = version + 1 WHERE id = 1 AND version = 3}$$
- If another worker transitioned the entity first, row count $= 0$, and the engine aborts with a concurrency conflict error.

---

## 🏢 Multi-Tenant Data Isolation: Postgres Row-Level Security (RLS)

In multi-tenant B2B architectures, housing each tenant in a separate database instance is cost-prohibitive. However, sharing a database poses the existential risk of **cross-tenant data leakage** caused by a developer forgetting `WHERE tenant_id = ?` in a complex query.

```
Incoming Request (Header: X-Tenant-ID: tenant_acme)
          │
          ▼
┌─────────────────────────────────────────────────────────────┐
│              multitenancy.TenantMiddleware                  │
│  - Extracts & validates tenant context                      │
│  - Executes: SET LOCAL app.current_tenant_id = 'tenant_acme'│
└──────────────┬──────────────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────────────┐
│                    PostgreSQL Engine                        │
│  POLICY: WHERE tenant_id = current_setting('app.tenant_id') │
│                                                             │
│  Developer runs: SELECT * FROM documents;                   │
│  Postgres executes: Only returns 'tenant_acme' rows!        │
└─────────────────────────────────────────────────────────────┘
```

### 1. Defense-in-Depth with PostgreSQL RLS
- Application-level filters (`GORM.Where("tenant_id = ?", tenantID)`) are vulnerable to developer oversight.
- [`multitenancy`](multitenancy/README.md) combines application scopes with database-level Row-Level Security (RLS).
- By setting session variables on each pooled connection (`SET LOCAL app.current_tenant_id = :tenantID`), Postgres itself rejects or hides rows belonging to other tenants, even on raw un-scoped SQL queries.

### 2. Scoped Caching & Noisy Neighbors
- **Cache Isolation:** Prefix all cache keys with tenant identifiers: `tenant:{id}:user:{uid}` to prevent cache collisions and cache poisoning across tenants.
- **Fair Resource Sharing:** A single whale tenant firing 10,000 QPS must not exhaust server thread pools. [`bulkhead.NewTenantBulkhead`](bulkhead/README.md) allocates per-tenant concurrency semaphores, isolating noisy tenants while guaranteeing headroom for smaller tenants.

---

## 🔍 In-Memory Full-Text Search: Inverted Index & Okapi BM25 Ranking

When microservices need search capabilities (user directories, product catalogs, audit logs, tenant policies), teams often default to spinning up Elasticsearch or OpenSearch clusters. In many production architectures, this introduces unnecessary complexity:
- **Operational Drag:** Cluster sizing, JVM heap tuning, garbage collection pauses, shard rebalancing, and snapshot management.
- **Network Overhead:** Every search involves HTTP/REST serialization and 10–50ms network round-trips.
- **Consistency Drifts:** Asynchronous ingestion creates lag where newly created records are invisible to users.

[`search`](search/README.md) solves this with an in-process, thread-safe inverted index delivering sub-millisecond query evaluation.

```
Incoming Document ──> Text Analysis Pipeline
                       │ - Standard / Whitespace Tokenizer
                       │ - Edge N-Gram Generator (Typeahead)
                       │ - Lowercase + ASCII Folding (diacritics)
                       │ - Stop-Word Removal & Stemmer
                       ▼
               Inverted Index Postings
               Term: "cache" -> [Doc1: freq=3, pos=[0, 4, 12]], [Doc2: freq=1, pos=[2]]
                       ▲
                       │
Query: MatchQuery("content", "cache invalidation")
                       │
             Okapi BM25 Ranking Engine
               IDF(q) * (TF * (k1 + 1)) / (TF + k1 * (1 - b + b * (docLen / avgLen)))
                       │
                       ▼
             Sorted Relevance Hits (p99 < 500µs)
```

### 1. Okapi BM25 vs Raw Term Frequency
Simple TF-IDF scoring overvalues term repetition: a document mentioning "cache" 50 times shouldn't score 50 times higher than one mentioning it twice. 
- **Okapi BM25** introduces an asymptotic term saturation curve controlled by parameter $k_1 \in [1.2, 2.0]$.
- Document length normalization is controlled by parameter $b \in [0.5, 0.8]$: long documents mentioning a term randomly are penalized relative to short, focused documents.

### 2. Live Typeahead via Character Edge N-Grams
Instead of executing expensive leading-wildcard queries (`LIKE '%term%'` or regex scans), the search engine decomposes terms into character prefix grams during indexing:
- Word `"microservice"` with `EdgeNGramTokenizer(2, 6)` indexes: `["mi", "mic", "micr", "micro", "micros"]`.
- When a user types `"micr"` in a search box, a constant-time `TermQuery` matches the pre-computed edge n-gram, returning sub-millisecond typeahead suggestions.

---

## 📦 Multi-Codec Compression & Zero-GC Buffer Pooling (Zstandard, Brotli, Gzip)

At massive enterprise scale ($100\text{k QPS}$), network bandwidth and serialization overhead can easily become the primary bottleneck and cost center.

### 1. Hardware Reality & The Egress Calculation
Consider an API serving $100\text{k QPS}$ where each JSON response averages $50\text{ KB}$:
- **Uncompressed Egress**: $100{,}000 \times 50\text{ KB} = 5\text{ GB/sec} = 40\text{ Gbps}$ bandwidth!
  - At AWS standard egress rate ($\$0.08/\text{GB}$), this generates **$\$1.44\text{ million/month}$** in network transit bills alone.
  - Saturated 10G/40G NICs trigger packet drops and latency spikes.
- **With Zstandard / Brotli Compression (75–85% reduction)**:
  - Average payload size drops from $50\text{ KB} \to 8\text{ KB}$.
  - Egress bandwidth collapses from **$40\text{ Gbps} \to 6.4\text{ Gbps}$** ($\$1.2\text{M}$ monthly cost savings).

### 2. The Per-Request Allocation Trap
Naive compression in Go (`gzip.NewWriter(w)`) allocates new compression state and hash tables on the heap **for every incoming HTTP request**:
- A single Gzip encoder allocates $\sim 60\text{ KB} - 250\text{ KB}$.
- At $50\text{k QPS}$, naive allocation generates **$12.5\text{ GB/sec}$ of short-lived garbage**, forcing the Go runtime into severe Stop-The-World (STW) garbage collection thrashing!
- [`compression`](compression/README.md) solves this by maintaining specialized `sync.Pool` instances for Gzip, Deflate, Brotli, and Zstandard. Writers are reset and recycled across requests, eliminating heap churn entirely.

### 3. Codec Selection Matrix
- **Zstandard (`zstd`)**: Best for **service-to-service internal RPCs** and cache payloads. Its decompression speed exceeds $1\text{ GB/sec}$ per core with near-zero latency degradation.
- **Brotli (`br`)**: Best for **external web and mobile clients** where network transit over 4G/5G mobile links is the primary user-facing latency factor.
- **Threshold Bypass (`MinLength`)**: Payloads smaller than $1\text{ KB}$ should never be compressed. Fixed headers and symbol tables in compressed formats often make payloads $< 500\text{ bytes}$ *larger* than uncompressed raw bytes.

---

## 📜 Event Sourcing & CQRS: Append-Only State, Time-Travel Auditability & Snapshotting

Traditional CRUD architectures mutate records in place (`UPDATE accounts SET balance = balance + 100 WHERE id = 'acc-1'`). While straightforward, in-place mutation has fatal architectural flaws in mission-critical, regulated domains:
- **Destructive State Loss:** Once overwritten, prior historical state is gone forever. You know the *current* balance, but cannot prove *how* or *why* it reached that number without relying on best-effort logs.
- **Race Condition Vulnerabilities:** Concurrent writes lead to lost update anomalies unless aggressive table-level locks or distributed mutexes are acquired.
- **Audit Deficits:** External regulators (SOC2, HIPAA, PCI-DSS, FedRAMP) require mathematical, tamper-evident proof of every change that ever occurred.

[`eventstore`](eventstore/README.md) transforms domain persistence from destructive mutation into a pure, append-only ledger of immutable domain facts.

```
       Write Side (Commands)                             Read Side (Queries)
   ┌───────────────────────────┐                     ┌───────────────────────────┐
   │      DepositMoneyCommand  │                     │   GetAccountBalanceQuery  │
   └─────────────┬─────────────┘                     └─────────────▲─────────────┘
                 │                                                 │
                 ▼                                                 │
      AggregateRoot.Deposit()                        Fast Denormalized Read Model
                 │                                       (PostgreSQL / Redis)
                 ▼                                                 ▲
      Repository[T].Save()                                         │
    (Optimistic Lock Check)                                        │
                 │                                                 │
                 ▼                                                 │
   ┌───────────────────────────┐                     ┌─────────────┴─────────────┐
   │     Event Store Engine    │──(Live & Catchup)──>│   SubscriptionManager     │
   │  (Append-Only Stream Log) │                     │   & Checkpoint Tracker    │
   └───────────────────────────┘                     └───────────────────────────┘
```

### 1. The Core Event Sourcing Mechanics
1. **Append-Only Streams:** Each domain entity (e.g. `BankAccount`, `Order`, `PatientChart`) has an isolated event stream identified by `StreamID`.
2. **Deterministic Rehydration:** When loading an entity, the `Repository` fetches historical events in sequential order and folds them into the aggregate root via `ApplyChange(event)`. State is a function of history:
   $$\text{State}_N = \sum_{i=1}^N \text{Event}_i$$
3. **Optimistic Concurrency Control:** When committing new events, callers specify `ExpectedVersion`. If a concurrent transaction appended an event in the interim, the version check fails with `ErrConcurrencyConflict`, preventing lost updates without locking the database table.

### 2. Snapshotting Strategy for Long-Lived Streams
Replaying 50,000 events to reconstruct an aggregate that has existed for five years would introduce intolerable $O(N)$ read latency. [`eventstore.SnapshotStore`](eventstore/README.md) solves this via periodic state snapshotting:
- At configured intervals (e.g. `EveryNEvents(100)`), the aggregate's point-in-time state is serialized to the snapshot store.
- Upon reload, the aggregate state is restored directly from snapshot at version $K$ ($O(1)$), and only delta events $K+1 \dots N$ are replayed. Rehydration latency remains $< 1\text{ms}$ regardless of stream lifetime.

### 3. Asynchronous CQRS Projections & Checkpoints
In Event Sourcing, write models are optimized solely for command validation and invariant enforcement, not querying. 
- The [`SubscriptionManager`](eventstore/README.md) fans out committed events to read-side `Projection` handlers.
- Each projection maintains its own persistent `CheckpointStore` tracking the last successfully processed global position.
- If a projection service crashes or new read models are deployed, projections simply catch up from their last checkpoint or replay from position 0 to retroactively generate new query tables.

---

## 🛡️ Distributed Session Management: Anti-Hijacking, Fixation Immunity & Sliding Windows

Session management in distributed architectures is fraught with subtle security vulnerabilities and concurrency pitfalls:
- **Session Fixation:** An attacker tricks a victim into using a known session identifier, waits for the user to log in, and retains access because the session ID was not rotated upon privilege elevation.
- **Session Hijacking:** Stolen session tokens (via XSS, insecure network transit, or malicious proxies) are replayed by attackers from foreign IP addresses.
- **Race Condition Header Flushing:** In Go's `net/http`, once `w.Write()` or `w.WriteHeader()` executes, response headers are flushed down the TCP socket. Any session modifications attempted afterwards fail to set the `Set-Cookie` header.

[`session`](session/README.md) addresses each of these threat vectors systematically.

```
Incoming Request
      │
      ▼
Client Fingerprint = SHA-256(MaskedIP(/24) + "|" + UserAgent)
      │
      ▼
Cookie Present? ───No───> Generate New Session (Cryptographically Secure 256-bit ID)
      │
     Yes
      │
      ▼
Load Session from Store (Memory / Encrypted Cookie / Redis)
      │
      ├──> Absolute Timeout Exceeded (> 24h)?  ───Yes───> Revoke & Re-issue
      │
      ├──> Idle Timeout Exceeded (> 30m)?      ───Yes───> Revoke & Re-issue
      │
      └──> Fingerprint Mismatch?               ───Yes───> 403 Forbidden (ErrHijackingDetected)
      │
      ▼
Inject into Request Context
      │
      ▼
Handler Execution (Get / Set / Flash / Regenerate / Destroy)
      │
      ▼
sessionResponseWriter.WriteHeader() / Write()
(Intercepts header write: Flushes Set-Cookie BEFORE status code transmission)
```

### 1. Subnet-Tolerant Anti-Hijacking Fingerprinting
Naively binding sessions to exact client IP addresses causes frequent session dropouts on mobile devices as users roam between cellular towers and Wi-Fi networks:
- **Subnet Masking:** [`session.DefaultFingerprint`](session/README.md) applies a `/24` CIDR mask to IPv4 addresses (or `/48` to IPv6), masking individual host variations while anchoring the session to the client's local autonomous routing domain.
- Combined with the normalized `User-Agent` string into a SHA-256 digest, this detects unauthorized cross-regional or cross-device token replaying without annoying legitimate roaming mobile users.

### 2. Session Fixation Defense via Token Regeneration
Whenever a user's authorization tier changes (e.g., anonymous guest $\to$ authenticated user, or regular user $\to$ sudo/admin):
- Calling [`session.Regenerate()`](session/README.md) atomically destroys the previous session ID in the storage backend and allocates a fresh 256-bit cryptographically random token (`crypto/rand`), moving existing session payloads into the new token.
- Even if an attacker seeded the original cookie, their access is severed the instant the victim authenticates.

### 3. Early Cookie Header Interception
Go's standard `http.ResponseWriter` emits headers immediately when `WriteHeader` is invoked:
- The [`session.Manager.Middleware()`](session/README.md) wraps `http.ResponseWriter` with an internal `sessionResponseWriter`.
- The first call to `WriteHeader()` or `Write()` intercepts the execution pipeline, persists modified session state to the store, and appends `Set-Cookie` (`HttpOnly`, `SameSite=Lax/Strict`, `Secure`, `Partitioned`) *before* delegating the status code write down to the underlying socket.

---

## 🩺 Autonomous Runtime Diagnostics: Memory Watermarks, Goroutine Leak Tracking & Zero-Trust Profiling

In high-concurrency production Go microservices, memory leaks and runaway goroutines represent the most insidious failure modes:
- **Silent OOM Kills:** When a container breaches its Linux cgroup memory limit, the Linux kernel kernel Out-Of-Memory (OOM) killer abruptly terminates the process with `SIGKILL` (exit status 137). No panic is triggered, no Go deferred cleanups execute, and no stack trace is written to logs.
- **Runaway Goroutine Leaks:** Goroutines abandoned on blocked channel sends, unread network connections, or uncancelled contexts accumulate silently, bloating heap allocations and consuming CPU scheduler slices until node exhaustion.
- **Public Profiler Exposure:** Mounting default `net/http/pprof` endpoints without strict authentication exposes decrypted memory heaps, secret keys, and customer data to external port scanners.

[`diagnostics`](diagnostics/README.md) resolves each failure scenario through proactive automation and defensive design.

```
                      Runtime Memory Monitoring Loop
                      ──────────────────────────────
                        ticker := 5 * time.Second
                                    │
                                    ▼
                         runtime.ReadMemStats()
                                    │
              ┌─────────────────────┴─────────────────────┐
              ▼                                           ▼
       HeapAlloc < Limit                           HeapAlloc >= Limit
      (Continue Polling)                                  │
                                                          ▼
                                              Cooldown Window Active?
                                             ┌────────────┴────────────┐
                                             ▼                         ▼
                                            Yes                        No
                                        (Suppressed)                   │
                                                                       ▼
                                                          Capture Automated Heap Dump
                                                        (Write .pprof File & Alert Hook)
```

### 1. Watermark-Triggered Heap Snapshots
Instead of post-mortem guessing after a container has already died:
- The [`diagnostics.MemoryWatchdog`](diagnostics/README.md) evaluates `runtime.MemStats` on an active interval.
- When `HeapAlloc` breaches the configured safety threshold (e.g. 80% of container cgroup memory), it proactively executes `pprof.WriteHeapProfile()` to disk before the kernel kills the pod.
- **Disk IOPS Protection:** Under continuous memory saturation, taking dumps every 5 seconds would exhaust disk IOPS and fill the filesystem. The watchdog enforces a rate-limiting cooldown window (e.g. 3–5 minutes) between dumps.

### 2. Goroutine Leak Detection & Stack Diffing
Detecting leaks during development, CI/CD integration tests, and staging:
- [`diagnostics.CaptureGoroutines()`](diagnostics/README.md) captures all active goroutine IDs and parses their full stack traces.
- After running a concurrent workload, [`diagnostics.VerifyNoGoroutineLeak()`](diagnostics/README.md) computes the set difference between current and baseline goroutines.
- **Settlement Grace Period:** Asynchronous workers may take a few milliseconds to notice context cancellation. The detector applies a configurable polling settlement grace period (`WithSettleTimeout`) and pattern filtering (`WithIgnoredPatterns`) to ignore runtime internals while catching true application worker leaks.

### 3. Zero-Trust Authenticated Diagnostic Routing
Access to `/debug/pprof/*` endpoints must be restricted:
- [`diagnostics.NewHandler()`](diagnostics/README.md) requires constant-time Bearer token verification (`crypto/subtle.ConstantTimeCompare`) on all incoming diagnostic requests.
- CIDR subnet allowlisting enforces that only trusted internal management networks (e.g. VPC bastion `10.0.0.0/8` or local loopback `127.0.0.1/32`) can probe profiler endpoints, eliminating unauthorized reconnaissance.

---

## ⚡ High-Throughput Stream ETL, Windowing, and Fault Isolation

High-volume event processing, data ingestion, and asynchronous analytics pipelines face severe distributed streaming pitfalls:
- **Unbounded Channel Memory Exhaustion:** Using unbuffered or arbitrarily buffered Go channels without backpressure limits causes intermediate queues to balloon under traffic spikes, triggering out-of-memory crashes.
- **Cascading Pipeline Stalls:** A slow or stalled downstream consumer (such as a slow database batch insert) blocks the entire chain of upstream producers, starving workers and causing ingress timeouts.
- **Fatal Error Crashes & Lost Streams:** An unhandled error or runtime panic in a single data record can terminate the entire pipeline process, abandoning all queued in-flight events.

[`pipeline`](pipeline/README.md) provides an end-to-end framework for reliable, bounded streaming execution.

```
                           Pipeline Execution Graph
                           ────────────────────────
   Input Stream (<-chan In)
            │
            ▼
    ┌──────────────┐   buffer = N    ┌──────────────┐   buffer = N    ┌──────────────┐
    │ Stage 1: Map │ ──────────────► │Stage 2: Filt │ ──────────────► │ Stage 3: Par │ ──► Output
    └──────────────┘                 └──────────────┘                 └──────────────┘
            │                                                                 │
     (on error/panic)                                                  (on error/panic)
            │                                                                 │
            └─────────────────────────┬───────────────────────────────────────┘
                                      ▼
                        Dead-Letter Queue (DLQ Sink)
                      (Ring Buffer / Error Telemetry)
```

### 1. Bounded Backpressure & Upstream Rate Coupling
- Every intermediate stage connection is backed by a bounded Go channel of configurable capacity (`WithPipelineBufferSize`).
- When a downstream stage slows down, its input channel fills to capacity. The preceding stage automatically blocks on channel send, propagating backpressure naturally up the execution chain all the way to the ingress feeder without unbounded memory allocation.
- When the input stream closes, all intermediate stages cleanly drain their buffers downstream before closing output channels, ensuring zero message loss during graceful shutdown.

### 2. Micro-Batching & Temporal Windowing
- **Tumbling Windows:** Non-overlapping time windows (`TumblingWindow`) collect elements arriving within a fixed duration bucket (e.g. 10s), evaluate a user aggregator function with panic protection, and emit the summary downstream. Partial windows are guaranteed to flush when the input stream terminates.
- **Sliding Windows:** Overlapping temporal windows (`SlidingWindow`) evaluate events within a moving time horizon (e.g. 5-minute sliding window evaluated every 30 seconds), automatically pruning expired historical events from memory to prevent memory leaks.

### 3. Panic Isolation & Dead-Letter Routing
- **Worker Panic Recovery:** All worker pools and stage transformation functions run under deferred `recover()` guards, intercepting panics and converting them into typed `ErrStagePanicked` errors.
- **Dead-Letter Routing:** Rather than halting mission-critical pipelines upon encountering a malformed or poisoned record, stages route failed items, failure timestamps, and root-cause errors directly to a `DeadLetterSink` (`MemoryDeadLetterQueue` or custom stream sink), allowing healthy records to proceed uninterrupted.
- **Operational Metrics:** Live telemetry (`p.Stats()`) tracks `ItemsIn`, `ItemsOut`, `Errors`, `DeadLettered`, and cumulative latency per stage for real-time Prometheus / Grafana observability.








