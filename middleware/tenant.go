package middleware

import (
	"context"
	"net/http"
)

type tenantIDKey struct{}

// TenantID returns a middleware that reads the X-Tenant-ID header and stores its
// value in the request context.
func TenantID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tid := r.Header.Get("X-Tenant-ID")
			ctx := context.WithValue(r.Context(), tenantIDKey{}, tid)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// TenantIDFromContext retrieves the tenant ID stored by the TenantID middleware.
func TenantIDFromContext(ctx context.Context) (string, bool) {
	tid, ok := ctx.Value(tenantIDKey{}).(string)
	return tid, ok && tid != ""
}
