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
| **Hot Key / Partition Imbalance**     | 5% of 100k QPS hits a single key, melting Redis/DB CPU                     | [`cache.NewTwoTier`](file:///Users/dhawal.dyavanpalli/go/src/gokit/cache/README.md) (L1 local RAM absorbs hits in ~50ns)                                                                                  |
| **Cache Stampede (Thundering Herd)**  | Hot key expires; 10,000 requests hit DB in same millisecond                | [`cache.GetOrSet`](file:///Users/dhawal.dyavanpalli/go/src/gokit/cache/README.md) (Singleflight request coalescing)                                                                                       |
| **Row-Level Lock Contention**         | `UPDATE counters SET count=count+1` caps at ~2,000 writes/sec              | [`cache.NewShardedCounter`](file:///Users/dhawal.dyavanpalli/go/src/gokit/cache/README.md) (Stripes increments over $N$ slots)                                                                            |
| **Replica Lag Ghosting**              | User updates profile; refresh hits replica with 500ms lag; data disappears | [`database.ReadYourWritesMiddleware`](file:///Users/dhawal.dyavanpalli/go/src/gokit/database/README.md) (Sticky primary routing)                                                                          |
| **Double-Charge / Payment Race**      | Client retries timed-out payment; user charged twice                       | [`idempotency.New`](file:///Users/dhawal.dyavanpalli/go/src/gokit/idempotency/README.md) + [`distlock.New`](file:///Users/dhawal.dyavanpalli/go/src/gokit/distlock/README.md) (In-flight locking)         |
| **Distributed Multi-Service Drift**   | Payment succeeds, inventory reservation fails; inconsistent state          | [`saga.New`](file:///Users/dhawal.dyavanpalli/go/src/gokit/saga/README.md) (Orchestration with reverse compensations)                                                                                     |
| **Dual-Write Inconsistency**          | DB write commits but Redis/Kafka publish fails; permanent drift            | [`outbox.NewOutbox`](file:///Users/dhawal.dyavanpalli/go/src/gokit/outbox/README.md) + [`eventbus.New`](file:///Users/dhawal.dyavanpalli/go/src/gokit/eventbus/README.md) (`SKIP LOCKED` relay)           |
| **Cascading Downstream Failure**      | Slow payment gateway exhausts connection pool and crashes fleet            | [`circuitbreaker.New`](file:///Users/dhawal.dyavanpalli/go/src/gokit/circuitbreaker/README.md) + [`bulkhead.New`](file:///Users/dhawal.dyavanpalli/go/src/gokit/bulkhead/README.md) (Fast-fail isolation) |
| **Inbound Webhook Replay Attacks**    | Stolen webhook payload re-submitted by malicious actor                     | [`webhook.NewVerifier`](file:///Users/dhawal.dyavanpalli/go/src/gokit/webhook/README.md) (HMAC-SHA256 + tolerance window)                                                                                 |
| **Unsafe Deployments / Bad Rollouts** | Buggy new feature deploys to 100% of traffic, causing outage               | [`featureflag.NewClient`](file:///Users/dhawal.dyavanpalli/go/src/gokit/featureflag/README.md) (Sticky deterministic canary rollout)                                                                      |
| **Scatter-Gather Tail Latency**      | Query fans out to 100 shards; p99 hits 63% tail                            | [`idgen.ExtractShardID`](file:///Users/dhawal.dyavanpalli/go/src/gokit/idgen/README.md) (Time-sorted 64-bit ID with embedded routing)                                                                      |
| **LSM Write Amplification Debt**      | 50 MB/s logical writes trigger 500 MB/s disk I/O; write stalls              | [`jobqueue.NewBatcher`](file:///Users/dhawal.dyavanpalli/go/src/gokit/jobqueue/README.md) (Amortize disk/network writes via batching & linger)                                                             |
| **Dual-Write Search Desync**          | App writes to DB and ES independently; crashes cause silent drift          | [`outbox.NewOutbox`](file:///Users/dhawal.dyavanpalli/go/src/gokit/outbox/README.md) (CDC log tailing into search posting lists)                                                                           |
| **Consumer Lag Explosion**            | Consumer falls behind; unbounded queue triggers OOM crash                  | [`jobqueue.NewBatcher`](file:///Users/dhawal.dyavanpalli/go/src/gokit/jobqueue/README.md) (Configurable prefetch & backpressure watermarks)                                                                |
| **Broker Redelivery Duplicates**      | Network timeout triggers retry; transaction executes twice                 | [`idempotency.New`](file:///Users/dhawal.dyavanpalli/go/src/gokit/idempotency/README.md) (`INSERT ... ON CONFLICT DO NOTHING` / atomic token check)                                                        |

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
- **Viral SKU Hot Key:** If one product takes $40\%$ of traffic ($60\text{k QPS}$), hash partitioning does **not** help. The fix is local in-process L1 caching ([`cache.TwoTierCache`](file:///Users/dhawal.dyavanpalli/go/src/gokit/cache/README.md)) to absorb the key in RAM.

---

## ⚖️ Load Balancing & Graceful Connection Draining

1. **L4 vs L7 Trade-offs**:
   - **L4 (TCP/IP)**: Ultra-cheap, connection-aware, high packet throughput.
   - **L7 (HTTP/gRPC)**: Route on path/headers, TLS termination, WAF, canaries. Beware CPU and connection churn at 100k+ RPS.
2. **The Connection Drain Race**:
   - When deploying new pods, mark instances out of the pool, stop accepting new connections, and wait for in-flight requests to complete before killing the process.
   - **The Timeout Mismatch Bug:** If Load Balancer idle timeout is 60s but App keep-alive is 75s, silent connection resets occur under low traffic. `gokit/server` defaults `idleTimeout: 120s` to ensure edge LBs terminate idle connections first.
3. **Avoid the "Sticky Session Tax"**:
   - IP-hash or cookie-based sticky routing creates uneven pod load (e.g. corporate NAT hashing thousands of users to one node) and breaks autoscaling. Use stateless JWTs or external session stores ([`cache.RedisCache`](file:///Users/dhawal.dyavanpalli/go/src/gokit/cache/README.md)).

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
2. **PII Cache Poisoning Guard**: If a route contains an `Authorization` or `Cookie` header, it must never be cached publicly. Use [`middleware.CacheControl`](file:///Users/dhawal.dyavanpalli/go/src/gokit/middleware/README.md):
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
- **Monotonic Reads & Ghost Data**: Prevent users from seeing time travel (missing posts upon refresh) using [`database.ReadYourWritesMiddleware`](file:///Users/dhawal.dyavanpalli/go/src/gokit/database/README.md).

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
   - Implemented via [`idgen.NewShardIDGenerator(shardID)`](file:///Users/dhawal.dyavanpalli/go/src/gokit/idgen/README.md) and zero-cost routing via [`idgen.ExtractShardID(id)`](file:///Users/dhawal.dyavanpalli/go/src/gokit/idgen/README.md).
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
   - Smooth write spikes by fronting writes with an amortizing batcher ([`jobqueue.NewBatcher`](file:///Users/dhawal.dyavanpalli/go/src/gokit/jobqueue/README.md)).

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
> Always use **Change-Data-Capture (CDC)** or the Transactional Outbox pattern ([`outbox.NewOutbox`](file:///Users/dhawal.dyavanpalli/go/src/gokit/outbox/README.md)) to guarantee reliable ingestion into search indexes.

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
- Implemented in `gokit` via [`jobqueue.NewBatcher`](file:///Users/dhawal.dyavanpalli/go/src/gokit/jobqueue/README.md).

### 2. Prefetch (In-Flight Credit) Sizing
Prefetch determines how many unacknowledged messages a consumer holds:
$$\text{Optimal Prefetch} \approx \frac{\text{Target Latency Budget}}{\text{Per-Message Processing Time}}$$
- **Too Low**: Workers starve on network roundtrips.
- **Too High**: One slow consumer holds a massive queue, runs out of memory, and causes a rebalance storm when killed by OOM.
- **Memory Ceiling**: $N_{\text{consumers}} \times \text{Prefetch} \times \text{Msg Size} \le \text{Allocated RAM}$.

### 3. Backpressure Triad
1. **Broker High Watermark**: Reject produces when disk capacity reaches 85%.
2. **Consumer Lag SLO**: Alert when consumer lag exceeds time-to-drain budget.
3. **Edge Admission Control**: Shed traffic or return 429 at the API gateway ([`ratelimit`](file:///Users/dhawal.dyavanpalli/go/src/gokit/ratelimit/README.md) and [`bulkhead`](file:///Users/dhawal.dyavanpalli/go/src/gokit/bulkhead/README.md)) before unbuffered queues crash nodes.

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
3. **Transactional Outbox**: Store domain entities and outgoing event messages in the **same database transaction** ([`outbox.NewOutbox`](file:///Users/dhawal.dyavanpalli/go/src/gokit/outbox/README.md)). A background relay tails the table via `SELECT ... FOR UPDATE SKIP LOCKED` and publishes to Kafka.

### Hard Follow-up: Payment Webhook Re-Delivery after Successful Charge
- **Scenario**: A payment webhook charged the user's card, but network dropped the broker ack. The broker redelivers the same event 30 seconds later.
- **Consumer Path**:
  1. Check idempotency store with key `pay_event:<event_id>` (TTL 7 days).
  2. If status is `COMPLETED`, immediately ack the message and return stored transaction receipt (no re-execution).
  3. **The Edge Case (Charge Succeeded, but DB crashed before writing idempotency record)**:
     - The consumer queries the upstream payment gateway (Stripe/Adyen) using the client idempotency key.
     - The payment gateway returns the existing charge object instead of charging again.
     - The consumer writes the missing `COMPLETED` record to the database and acks the queue.

