package multitenancy

import (
	"net/http"
	"strings"
)

// MiddlewareOption configures the RequireTenant middleware.
type MiddlewareOption func(*middlewareConfig)

type middlewareConfig struct {
	headerName string
	status     int
}

// WithHeader sets the HTTP request header to read tenant ID from (defaults to "X-Tenant-ID").
func WithHeader(name string) MiddlewareOption {
	return func(c *middlewareConfig) {
		if name != "" {
			c.headerName = name
		}
	}
}

// WithStatusCode sets the status code returned when tenant ID is missing (defaults to 400 Bad Request).
func WithStatusCode(code int) MiddlewareOption {
	return func(c *middlewareConfig) {
		if code > 0 {
			c.status = code
		}
	}
}

// RequireTenant returns a middleware that strictly enforces tenant presence on HTTP endpoints.
// Requests without a tenant in context or the header are rejected with JSON error.
func RequireTenant(opts ...MiddlewareOption) func(http.Handler) http.Handler {
	cfg := &middlewareConfig{
		headerName: "X-Tenant-ID",
		status:     http.StatusBadRequest,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if already in context
			if _, ok := FromContext(r.Context()); ok {
				next.ServeHTTP(w, r)
				return
			}

			// Check request header
			tid := strings.TrimSpace(r.Header.Get(cfg.headerName))
			if tid == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(cfg.status)
				_, _ = w.Write([]byte(`{"code":"TENANT_REQUIRED","message":"missing required tenant identity"}`))
				return
			}

			ctx := WithTenantID(r.Context(), tid)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
