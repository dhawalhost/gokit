package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/dhawalhost/gokit/multitenancy"
)

// TenantID returns a middleware that resolves the tenant ID and stores its value
// in the request context using gokit/multitenancy.
//
// When authenticated JWT claims are present in the request context, the tenant ID
// is bound to the caller's identity claim (default "tenant_id"), and mismatched
// X-Tenant-ID headers are rejected with 403 Forbidden unless multitenancy.WithTrustHeader(true)
// or a custom multitenancy.WithTenantResolver is configured.
//
// For strict enforcement (rejecting missing tenant identities with 400 Bad Request),
// use multitenancy.RequireTenant.
func TenantID(opts ...multitenancy.MiddlewareOption) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tid, err := multitenancy.ResolveRequestTenant(r, opts...)
			if err != nil {
				if errors.Is(err, multitenancy.ErrTenantMismatch) {
					writeJSONError(w, http.StatusForbidden, `{"code":"TENANT_MISMATCH","message":"tenant header does not match authenticated identity"}`)
				} else {
					writeJSONError(w, http.StatusForbidden, `{"code":"TENANT_FORBIDDEN","message":"tenant access denied"}`)
				}
				return
			}
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
