package multitenancy

import (
	"errors"
	"net/http"
	"strings"
)

// Sentinel errors returned by ResolveRequestTenant and tenant resolvers.
var (
	// ErrTenantMismatch is returned when the requested tenant header does not match the caller's identity.
	ErrTenantMismatch = errors.New("multitenancy: tenant does not match authenticated identity")
	// ErrTenantForbidden is returned when a TenantResolver rejects tenant access.
	ErrTenantForbidden = errors.New("multitenancy: tenant access denied")
)

// TenantResolver resolves and authorizes the tenant ID for an incoming HTTP request.
type TenantResolver func(r *http.Request) (string, error)

// MiddlewareOption configures tenant resolution and enforcement middlewares.
type MiddlewareOption func(*middlewareConfig)

type middlewareConfig struct {
	headerName  string
	status      int
	tenantClaim string
	trustHeader bool
	resolver    TenantResolver
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

// WithTenantResolver configures a custom TenantResolver to extract and verify the tenant ID.
func WithTenantResolver(resolver TenantResolver) MiddlewareOption {
	return func(c *middlewareConfig) {
		c.resolver = resolver
	}
}

// WithTenantClaim sets the authenticated claim key used to extract the caller's tenant ID
// (defaults to "tenant_id", with a fallback to "tid").
func WithTenantClaim(claim string) MiddlewareOption {
	return func(c *middlewareConfig) {
		if claim != "" {
			c.tenantClaim = claim
		}
	}
}

// WithTrustHeader explicitly opts in to accepting the tenant header even when it differs from
// or is absent in authenticated claims (intended only for trusted service-to-service calls).
func WithTrustHeader(trust bool) MiddlewareOption {
	return func(c *middlewareConfig) {
		c.trustHeader = trust
	}
}

func defaultMiddlewareConfig(opts ...MiddlewareOption) *middlewareConfig {
	cfg := &middlewareConfig{
		headerName:  "X-Tenant-ID",
		status:      http.StatusBadRequest,
		tenantClaim: "tenant_id",
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return cfg
}

// ResolveRequestTenant determines the authorized tenant ID for r using opts.
// When authenticated claims exist in context and WithTrustHeader(true) is not set,
// any tenant header must match the tenant claim or ErrTenantMismatch is returned.
func ResolveRequestTenant(r *http.Request, opts ...MiddlewareOption) (string, error) {
	cfg := defaultMiddlewareConfig(opts...)
	headerTenant := strings.TrimSpace(r.Header.Get(cfg.headerName))

	if cfg.resolver != nil {
		resolved, err := cfg.resolver(r)
		if err != nil {
			return "", ErrTenantForbidden
		}
		resolved = strings.TrimSpace(resolved)
		if !cfg.trustHeader && headerTenant != "" && resolved != "" && headerTenant != resolved {
			return "", ErrTenantMismatch
		}
		return resolved, nil
	}

	if claims, hasClaims := ClaimsMapFromContext(r.Context()); hasClaims {
		claimTenant := extractClaimTenant(claims, cfg.tenantClaim)
		if !cfg.trustHeader {
			if headerTenant != "" && headerTenant != claimTenant {
				return "", ErrTenantMismatch
			}
			if existing, ok := FromContext(r.Context()); ok {
				if claimTenant != "" && existing != claimTenant {
					return "", ErrTenantMismatch
				}
				return existing, nil
			}
			return claimTenant, nil
		}

		// Explicit WithTrustHeader(true): prefer header, fallback to claim or context.
		if headerTenant != "" {
			return headerTenant, nil
		}
		if existing, ok := FromContext(r.Context()); ok {
			return existing, nil
		}
		return claimTenant, nil
	}

	if existing, ok := FromContext(r.Context()); ok {
		return existing, nil
	}
	return headerTenant, nil
}

func extractClaimTenant(claims map[string]any, claimKey string) string {
	if claims == nil {
		return ""
	}
	if v, ok := claims[claimKey].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if claimKey == "tenant_id" {
		if v, ok := claims["tid"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// RequireTenant returns a middleware that strictly enforces tenant presence and identity binding.
// Requests without a valid tenant or with a mismatched tenant header are rejected with JSON errors.
func RequireTenant(opts ...MiddlewareOption) func(http.Handler) http.Handler {
	cfg := defaultMiddlewareConfig(opts...)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tid, err := ResolveRequestTenant(r, opts...)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				if errors.Is(err, ErrTenantMismatch) {
					_, _ = w.Write([]byte(`{"code":"TENANT_MISMATCH","message":"tenant header does not match authenticated identity"}`))
				} else {
					_, _ = w.Write([]byte(`{"code":"TENANT_FORBIDDEN","message":"tenant access denied"}`))
				}
				return
			}
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
