package multitenancy

import (
	"context"
)

type tenantContextKey struct{}
type claimsMapContextKey struct{}

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
func FromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	if t, ok := ctx.Value(tenantContextKey{}).(Tenant); ok && t.ID != "" {
		return t.ID, true
	}
	return "", false
}

// TenantFromContext retrieves the full Tenant object from the context if available.
func TenantFromContext(ctx context.Context) (Tenant, bool) {
	if ctx == nil {
		return Tenant{}, false
	}
	if t, ok := ctx.Value(tenantContextKey{}).(Tenant); ok && t.ID != "" {
		return t, true
	}
	return Tenant{}, false
}

// WithClaimsMap injects authenticated token claims into ctx so tenant resolution
// middlewares can verify caller identity without importing JWT libraries directly.
func WithClaimsMap(ctx context.Context, claims map[string]any) context.Context {
	return context.WithValue(ctx, claimsMapContextKey{}, claims)
}

// ClaimsMapFromContext retrieves authenticated token claims stored via WithClaimsMap.
func ClaimsMapFromContext(ctx context.Context) (map[string]any, bool) {
	if ctx == nil {
		return nil, false
	}
	claims, ok := ctx.Value(claimsMapContextKey{}).(map[string]any)
	return claims, ok
}
