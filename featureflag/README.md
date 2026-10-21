# Feature Flag (`gokit/featureflag`)

`featureflag` provides a high-performance, in-memory feature flag and progressive rollout evaluation engine conforming to the [OpenFeature](https://openfeature.dev) specification. It enables dynamic feature gating, canary percentage rollouts with deterministic hashing, and tenant/user targeting rules without requiring code deployments.

---

## Features

- **Progressive Delivery & Canary Rollouts**:
  - Percentage-based rollouts (0.0% to 100.0%) utilizing deterministic FNV-1a hashing.
  - Guarantees that a user consistently receives the same variation across calls and that rollout increases (e.g. 10% -> 25%) are strictly monotonic.
- **Contextual Attribute Targeting**:
  - Operators: `equals`, `not_equals`, `in`, `not_in`, `contains`, `starts_with`, `ends_with`.
  - Target by tenant ID (`tenant_id in ["acme-corp", "globex"]`), email domains (`ends_with: "@company.internal"`), user roles, or custom plan tiers.
- **Typed Evaluations**:
  - First-class support for `BooleanValue`, `StringValue`, `IntValue`, and `FloatValue` with safe fallback defaults.
- **Zero Latency**:
  - Evaluates in sub-microsecond in-memory lookups without remote network calls on the critical request path.

---

## Quick Start

```go
package main

import (
    "context"
    "fmt"

    "github.com/dhawalhost/gokit/featureflag"
)

func main() {
    ctx := context.Background()

    // 1. Define flag with canary rollout and targeting rules
    aiSearchFlag := featureflag.Flag{
        Key:              "ai-search-v2",
        Enabled:          true,
        DefaultVariation: "false",
        Variations: map[string]any{
            "true":  true,
            "false": false,
        },
        Rules: []featureflag.Rule{
            // Internal staff always get the feature
            {
                Attribute: "email",
                Operator:  featureflag.OpEndsWith,
                Values:    []string{"@mycompany.com"},
                Variation: "true",
            },
            // Enterprise plan tenants always get the feature
            {
                Attribute: "tier",
                Operator:  featureflag.OpEquals,
                Values:    []string{"enterprise"},
                Variation: "true",
            },
        },
        // 20% canary rollout to all other users
        Rollout: &featureflag.RolloutStrategy{
            Percentage: 20.0,
        },
    }

    client := featureflag.NewClient(aiSearchFlag)

    // 2. Evaluate flag for a user
    userCtx := featureflag.NewContext("usr-102").
        WithAttribute("email", "customer@external.com").
        WithAttribute("tier", "pro")

    isEnabled := client.BooleanValue(ctx, "ai-search-v2", false, userCtx)
    fmt.Println("AI Search Enabled for user:", isEnabled)
}
```

---

## Supported Rule Operators

| Operator | Constant | Description |
|---|---|---|
| `equals` | `OpEquals` | String equality match |
| `not_equals` | `OpNotEquals` | String inequality match |
| `in` | `OpIn` | Matches any value in the provided slice |
| `not_in` | `OpNotIn` | Matches if value is absent from slice |
| `contains` | `OpContains` | Substring match |
| `starts_with`| `OpStartsWith`| Prefix match |
| `ends_with` | `OpEndsWith` | Suffix match (e.g. domain checks) |

---

## 🔌 Integration Guide

While `gokit/featureflag` includes a zero-dependency in-memory evaluation engine, production architectures typically pair an in-process evaluator with a central control plane.

### 1. Integrating with Flagura ([github.com/dhawalhost/flagura](https://github.com/dhawalhost/flagura))

[Flagura](https://github.com/dhawalhost/flagura) is an open-source feature management control plane and evaluation platform. It provides durable ACID governance (PostgreSQL/SQLite), a dashboard, RBAC, 4-Eyes change requests, and automated flag debt cleanup.

#### Pattern A: Direct Flagura SDK / OpenFeature Native
Applications using `gokit` can consume Flagura's native Go SDK (`github.com/dhawalhost/flagura/sdks/go`) or Flagura's OpenFeature provider:

```go
package main

import (
    "context"
    "fmt"
    "time"

    flaguraclient "github.com/dhawalhost/flagura/sdks/go"
)

func main() {
    ctx := context.Background()

    // Initialize local-evaluating Flagura client (~85ns CPU cache evaluation)
    client, err := flaguraclient.New(flaguraclient.Config{
        BaseURL:      "https://flagura.internal.net",
        SDKKey:       "flg_prod_abc123",
        SyncInterval: 30 * time.Second, // Background rule refresh
    })
    if err != nil {
        panic(err)
    }
    defer client.Close()

    evalCtx := flaguraclient.NewContext("user-101").
        With("email", "eng@mycompany.com").
        With("tier", "enterprise")

    enabled, _ := client.EvaluateBoolean(ctx, "new-payment-flow", false, evalCtx)
    fmt.Println("New payment flow active:", enabled)
}
```

#### Pattern B: Real-Time Cache Sync via `gokit/webhook`
When rules change in Flagura, Flagura emits an audit event. Your service receives the signed webhook via `gokit/webhook` and instantly updates `gokit/featureflag` without polling delays:

```go
package main

import (
    "encoding/json"
    "net/http"

    "github.com/dhawalhost/gokit/featureflag"
    "github.com/dhawalhost/gokit/webhook"
)

func RegisterFlaguraWebhook(mux *http.ServeMux, client *featureflag.Client, webhookSecret []byte) {
    verifier := webhook.NewVerifier(webhook.VerifierConfig{
        Secret: webhookSecret,
    })

    handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        payload, ok := webhook.PayloadFromContext(r.Context())
        if !ok {
            w.WriteHeader(http.StatusBadRequest)
            return
        }

        var event struct {
            Event string           `json:"event"`
            Flag  featureflag.Flag `json:"flag"`
        }
        if err := json.Unmarshal(payload, &event); err == nil && event.Event == "flag.updated" {
            client.RegisterFlag(event.Flag) // Hot-reload in-memory flag rules
        }

        w.WriteHeader(http.StatusOK)
    })

    mux.Handle("/webhooks/flagura", verifier.Middleware()(handler))
}
```

---

### 2. Supported Feature Flag Systems & Ecosystem

Because `gokit/featureflag` follows the CNCF [OpenFeature](https://openfeature.dev) standard paradigm, services can easily integrate with any of the following feature flag platforms:

| System | Type | Integration Pattern | Best Used For |
| :--- | :--- | :--- | :--- |
| **Flagura** | Open-source control plane & local engine | Flagura Go SDK / OpenFeature provider or Webhook sync | Fast in-memory evaluations (~85ns), active flag debt hygiene, 4-Eyes change approval, zero PII leakage |
| **flagd** (CNCF) | Open-source evaluation daemon | Sidecar or in-process provider via OpenFeature | Kubernetes microservices running flagd as a local sidecar daemon |
| **Flipt** | Open-source GitOps-friendly platform | REST/gRPC API sync or Flipt Go client | Teams managing feature flags via declarative GitOps workflows (YAML/Git) |
| **Unleash** | Open-source & Enterprise platform | `unleash-client-go` or OpenFeature Unleash provider | Large enterprise environments needing complex strategies and proxy architectures |
| **Flagsmith** | Open-source / Cloud | Flagsmith Go SDK | Remote configuration, multivariate testing, and edge flag evaluations |
| **LaunchDarkly** | Commercial Cloud SaaS | LaunchDarkly Go SDK / OpenFeature Provider | Enterprise cloud-managed feature flagging with real-time streaming updates |
| **PostHog** | Product Analytics & Feature Flags | PostHog Go Client (`posthog-go`) | Tying feature flags directly to product analytics and user event funnels |
| **Redis / Consul / Etcd** | Distributed KV Store / Configuration | Sync serialized `featureflag.Flag` JSON via `gokit/eventbus` pub/sub | Self-hosted microservices synchronizing flags across a cluster without an external control plane |

---

### 3. OpenFeature Compatibility

To wrap `gokit/featureflag` or any external provider under a unified API:

```go
// OpenFeature specification interface allows hot-swapping providers:
// - Flagura Provider
// - In-Memory gokit/featureflag Provider
// - flagd Sidecar Provider
// - LaunchDarkly / Unleash Provider
//
// The application code only interacts with the OpenFeature Client:
// openfeature.GetBooleanValue(ctx, "flag-key", defaultValue, evalCtx)
```
