# Multi-Tenancy (`gokit/multitenancy`)

`multitenancy` provides end-to-end data isolation and tenant context propagation for multi-tenant SaaS applications. It safeguards against cross-tenant data leaks across HTTP requests, PostgreSQL database queries (via GORM scopes and Row-Level Security), and shared Redis caches.

---

## Features

- **Context & HTTP Middleware**:
  - `multitenancy.RequireTenant(opts...)`: Resolves tenant identity from authenticated JWT claims (`WithTenantClaim`, `WithTenantResolver`) or `X-Tenant-ID`, rejecting header/claim mismatches with `403 Forbidden` (opt-in `WithTrustHeader(true)` for trusted internal service-to-service hops).
  - Context helpers: `WithTenantID(ctx, tenantID)` and `FromContext(ctx)`.
- **GORM Query Scoping**:
  - `ScopeFromContext(ctx)`: Automatically injects `WHERE tenant_id = ?` into SELECT, UPDATE, and DELETE queries.
- **PostgreSQL Row-Level Security (RLS)**:
  - `WithTenantTx(ctx, db, tenantID, fn)`: Safely sets the transaction-local tenant session variable via `SELECT set_config('app.current_tenant_id', ?, true)`.
  - Guarantees database-level enforcement even if application code omits a WHERE clause.
- **Tenant-Scoped Caching (`ScopedCache` / `ContextScopedCache`)**:
  - Automatically isolates cache entries in shared Redis or in-memory caches by prepending a namespace prefix (`tenant:<tenant_id>:<key>`).
  - Supports prefix-scoped eviction via `ClearTenant(ctx)` and `ClearPrefix(ctx, prefix)` using non-blocking `SCAN` + `UNLINK` instead of global `FLUSHDB`.

---

## Quick Start

### 1. HTTP Middleware & Context Extraction

```go
package main

import (
    "net/http"

    "github.com/dhawalhost/gokit/multitenancy"
    "github.com/go-chi/chi/v5"
)

r := chi.NewRouter()

// Enforces tenant identity on protected routes:
// - Returns 400 Bad Request when tenant ID is missing (configurable via WithStatusCode).
// - When JWT claims exist in context, returns 403 Forbidden if X-Tenant-ID != claim unless WithTrustHeader(true).
r.Use(multitenancy.RequireTenant(
    multitenancy.WithTenantClaim("tenant_id"),
))

r.Get("/api/v1/invoices", func(w http.ResponseWriter, r *http.Request) {
    tenantID, ok := multitenancy.FromContext(r.Context())
    // tenantID is safely available for downstream operations
})
```

---

### 2. GORM Query Scoping

```go
type Invoice struct {
    ID       string
    TenantID string `gorm:"index;not null"`
    Amount   float64
}

func GetInvoices(ctx context.Context, db *gorm.DB) ([]Invoice, error) {
    var invoices []Invoice
    // Automatically appends: WHERE tenant_id = '<current_tenant>'
    err := db.Scopes(multitenancy.ScopeFromContext(ctx)).Find(&invoices).Error
    return invoices, err
}
```

---

### 3. PostgreSQL Row-Level Security (RLS)

Enable Row-Level Security in your PostgreSQL migration:

```sql
ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_policy ON invoices
    FOR ALL
    TO tenant_app_user
    USING (tenant_id = current_setting('app.current_tenant_id', true));
```

Execute queries inside an RLS-scoped transaction:

```go
err := multitenancy.WithTenantTx(ctx, db, tenantID, func(tx *gorm.DB) error {
    // Queries in this closure automatically enforce RLS
    return tx.Find(&invoices).Error
})
```

---

### 4. Tenant-Scoped Caching

```go
import (
    "github.com/dhawalhost/gokit/cache"
    "github.com/dhawalhost/gokit/multitenancy"
)

// Wrap your shared Redis cache
scopedCache := multitenancy.NewContextScopedCache(redisCache)

// Key stored in Redis becomes: "tenant:acme-corp:user:101"
err := scopedCache.Set(ctx, "user:101", userProfile, time.Hour)

// Non-blocking SCAN + UNLINK eviction scoped only to this tenant or prefix:
tenantCache := multitenancy.NewScopedCache(redisCache, "acme-corp")
_ = tenantCache.ClearPrefix(ctx, "user:")
_ = tenantCache.ClearTenant(ctx)
```
