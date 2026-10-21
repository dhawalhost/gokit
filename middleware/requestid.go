// Package middleware provides composable net/http middleware functions.
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type requestIDKey struct{}

// isValidRequestID verifies that a request ID is safe to use in headers and logs.
// It allows alphanumeric characters, hyphens, and underscores up to 64 characters.
func isValidRequestID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// RequestID is a middleware that injects a unique request ID into every request.
// It reads X-Request-ID from the incoming request if present and valid;
// otherwise it generates a new UUID v4. The ID is stored in the request context
// and set on the response header.
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
			if !isValidRequestID(id) {
				id = uuid.New().String()
			}
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			w.Header().Set("X-Request-ID", id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFromContext retrieves the request ID from ctx.
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}
