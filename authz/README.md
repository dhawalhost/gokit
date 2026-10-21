# Authorization (`gokit/authz`)

`authz` provides a fine-grained, unified authorization engine supporting both **Role-Based Access Control (RBAC)** and **Attribute-Based Access Control (ABAC)**. It features hierarchical role inheritance, wildcard glob permission matching, multi-tenant boundary enforcement, and pluggable HTTP route middlewares.

---

## Features

- **Granular Wildcard Matching**:
  - `*`: Matches any action (e.g., `invoices:*` grants all invoice operations; `*` grants superuser access).
  - `?`: Matches single character wildcards.
  - Prefix/Suffix matching (e.g., `documents:read:*`, `billing:*:view`).
- **Hierarchical Role Inheritance**:
  - Declaratively define role inheritance DAGs (e.g., `admin` inherits `manager`, which inherits `viewer`).
  - Permissions assigned to lower roles automatically cascade to parent roles.
- **Dynamic ABAC Rules**:
  - Evaluate dynamic policies based on resource ownership, environmental attributes, or business hours.
- **Tenant Boundary Isolation**:
  - Pre-evaluates whether the actor's tenant context matches the target resource tenant, blocking cross-tenant privilege escalation before checking permissions.
- **Chi HTTP Middlewares**:
  - `authz.Require(enforcer, permission)`: Route guard by permission string.
  - `authz.RequireRole(enforcer, role)`: Route guard by minimum role.

---

## Quick Start

### 1. Initializing the Enforcer

```go
package main

import (
    "context"

    "github.com/dhawalhost/gokit/authz"
)

func SetupAuthz() *authz.Enforcer {
    // 1. Define role inheritance hierarchy
    hierarchy := map[string][]string{
        "admin":   {"finance", "support"},
        "finance": {"viewer"},
        "support": {"viewer"},
    }

    // 2. Define role permissions with wildcards
    permissions := map[string][]string{
        "viewer":  {"*:read", "dashboard:view"},
        "finance": {"invoices:*", "payments:*"},
        "support": {"tickets:*", "users:read"},
        "admin":   {"*"},
    }

    return authz.NewEnforcer(authz.Config{
        RoleHierarchy:   hierarchy,
        RolePermissions: permissions,
    })
}
```

---

### 2. Protecting HTTP Routes

```go
import (
    "github.com/dhawalhost/gokit/authz"
    "github.com/go-chi/chi/v5"
)

r := chi.NewRouter()
enforcer := SetupAuthz()

// Protect by specific permission
r.With(authz.Require(enforcer, "invoices:create")).
    Post("/api/v1/invoices", createInvoiceHandler)

// Protect by required role
r.With(authz.RequireRole(enforcer, "admin")).
    Delete("/api/v1/invoices/{id}", deleteInvoiceHandler)
```

---

### 3. Programmatic & Contextual ABAC Evaluation

```go
func HandleDocumentAccess(ctx context.Context, enforcer *authz.Enforcer, doc Document) error {
    req := authz.EvaluationRequest{
        SubjectRole:     "finance",
        SubjectTenantID: "tenant-acme",
        SubjectUserID:   "usr-101",
        Permission:      "documents:delete",
        Resource: authz.Resource{
            Type:     "document",
            ID:       doc.ID,
            TenantID: doc.TenantID,
            OwnerID:  doc.AuthorID,
        },
    }

    return enforcer.Evaluate(ctx, req)
}
```
