package middleware

import (
	"context"
	"net/http"

	"github.com/dhawalhost/gokit/multitenancy"
)

// TenantID returns a middleware that reads the X-Tenant-ID header and stores its
// value in the request context using gokit/multitenancy.
//
// For strict enforcement (rejecting missing tenant headers with 400 Bad Request JSON errors),
// database query scoping, or tenant-isolated caching, use multitenancy.RequireTenant.
func TenantID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tid := r.Header.Get("X-Tenant-ID")
			if tid != "" {
				r = r.WithContext(multitenancy.WithTenantID(r.Context(), tid))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WithTenantID sets the tenant ID on the context.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return multitenancy.WithTenantID(ctx, tenantID)
}

// TenantIDFromContext retrieves the tenant ID stored in the context.
func TenantIDFromContext(ctx context.Context) (string, bool) {
	return multitenancy.FromContext(ctx)
}
