package multitenancy

import (
	"context"

	"github.com/dhawalhost/gokit/middleware"
)

type tenantContextKey struct{}

// Tenant represents an authenticated tenant identity.
type Tenant struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

// WithTenant injects a Tenant into the context.
func WithTenant(ctx context.Context, tenant Tenant) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tenant)
}

// WithTenantID injects a tenant ID into the context.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, Tenant{ID: tenantID})
}

// FromContext retrieves the tenant ID from the context.
// It checks the multitenancy context key, falling back to middleware.TenantIDFromContext.
func FromContext(ctx context.Context) (string, bool) {
	if t, ok := ctx.Value(tenantContextKey{}).(Tenant); ok && t.ID != "" {
		return t.ID, true
	}
	return middleware.TenantIDFromContext(ctx)
}

// TenantFromContext retrieves the full Tenant object from the context if available.
func TenantFromContext(ctx context.Context) (Tenant, bool) {
	if t, ok := ctx.Value(tenantContextKey{}).(Tenant); ok && t.ID != "" {
		return t, true
	}
	if tid, ok := middleware.TenantIDFromContext(ctx); ok && tid != "" {
		return Tenant{ID: tid}, true
	}
	return Tenant{}, false
}
