package audit

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// extractIP extracts a client IP address, handling proxy headers (X-Forwarded-For, X-Real-IP).
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if parsed := net.ParseIP(ip); parsed != nil {
				return ip
			}
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		ip := strings.TrimSpace(xri)
		if parsed := net.ParseIP(ip); parsed != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// ContextMiddleware extracts client IP and user-agent from the HTTP request
// and injects them into the audit context metadata.
func ContextMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ip := extractIP(r)
			if ip != "" {
				ctx = WithMetadata(ctx, "client_ip", ip)
			}
			if ua := r.Header.Get("User-Agent"); ua != "" {
				ctx = WithMetadata(ctx, "user_agent", ua)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// statusWriter wraps http.ResponseWriter to capture the HTTP status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Middleware creates a middleware that records an audit event for HTTP mutating
// requests (POST, PUT, PATCH, DELETE).
func Middleware(recorder *Recorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only audit mutating methods automatically
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)

			duration := time.Since(start)

			status := StatusSuccess
			var reason string
			if sw.status == http.StatusUnauthorized || sw.status == http.StatusForbidden {
				status = StatusDenied
				reason = fmt.Sprintf("HTTP %d", sw.status)
			} else if sw.status >= http.StatusBadRequest {
				status = StatusFailure
				reason = fmt.Sprintf("HTTP %d", sw.status)
			}

			action := fmt.Sprintf("http.%s", strings.ToLower(r.Method))
			_ = recorder.NewEvent(action).
				WithResource("http_endpoint", r.URL.Path).
				WithStatus(status).
				WithReason(reason).
				WithMetadata("method", r.Method).
				WithMetadata("path", r.URL.Path).
				WithMetadata("status_code", sw.status).
				WithMetadata("duration_ms", duration.Milliseconds()).
				Record(r.Context())
		})
	}
}
