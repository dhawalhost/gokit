package authz

import (
	"errors"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func handleEnforceError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrUnauthorized) {
		writeJSON(w, http.StatusUnauthorized, `{"code":"UNAUTHORIZED","message":"authentication required"}`)
		return
	}
	writeJSON(w, http.StatusForbidden, `{"code":"FORBIDDEN","message":"insufficient permissions"}`)
}

// Require returns an HTTP middleware requiring the specified permission.
func Require(enforcer *Enforcer, permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := enforcer.Enforce(r.Context(), permission); err != nil {
				handleEnforceError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAny returns an HTTP middleware requiring at least one of the specified permissions.
func RequireAny(enforcer *Enforcer, permissions ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			var anyAllowed bool
			for _, p := range permissions {
				allowed, err := enforcer.HasPermission(ctx, p)
				if err != nil && errors.Is(err, ErrUnauthorized) {
					handleEnforceError(w, err)
					return
				}
				if allowed {
					anyAllowed = true
					break
				}
			}

			if !anyAllowed {
				handleEnforceError(w, ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAll returns an HTTP middleware requiring all of the specified permissions.
func RequireAll(enforcer *Enforcer, permissions ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			for _, p := range permissions {
				if err := enforcer.Enforce(ctx, p); err != nil {
					handleEnforceError(w, err)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole returns an HTTP middleware ensuring the subject possesses at least one of the given roles.
func RequireRole(enforcer *Enforcer, roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if _, ok := SubjectFromContext(ctx); !ok {
				handleEnforceError(w, ErrUnauthorized)
				return
			}
			for _, role := range roles {
				if enforcer.HasRole(ctx, role) {
					next.ServeHTTP(w, r)
					return
				}
			}
			handleEnforceError(w, ErrForbidden)
		})
	}
}
