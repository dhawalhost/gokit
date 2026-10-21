package middleware

import (
	"net"
	"net/http"
	"strings"
)

// writeJSONError writes a JSON error response with the correct Content-Type.
// It is shared by all middleware in this package that need to reject requests.
func writeJSONError(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// StatusRecorder wraps ResponseWriter to capture the written status code.
// It is used by the Logger and observability middlewares.
type StatusRecorder struct {
	http.ResponseWriter
	Status int
}

// WriteHeader records the status code and delegates to the underlying ResponseWriter.
func (r *StatusRecorder) WriteHeader(status int) {
	r.Status = status
	r.ResponseWriter.WriteHeader(status)
}

// timeoutResponseWriter wraps ResponseWriter to track whether headers have
// been written, preventing a double-write when a timeout fires concurrently.
type timeoutResponseWriter struct {
	http.ResponseWriter
	written bool
	done    chan struct{}
}

func (tw *timeoutResponseWriter) WriteHeader(code int) {
	if !tw.written {
		tw.written = true
		tw.ResponseWriter.WriteHeader(code)
	}
}

func (tw *timeoutResponseWriter) Write(b []byte) (int, error) {
	tw.written = true
	return tw.ResponseWriter.Write(b)
}

// extractIP cleans and validates an IP string, stripping ports and spaces.
func extractIP(addr string) string {
	addr = strings.TrimSpace(addr)
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	if parsed := net.ParseIP(addr); parsed != nil {
		return parsed.String()
	}
	return addr
}

// IPKeyFunc returns the client IP address from the request, checking
// X-Real-IP and X-Forwarded-For before falling back to RemoteAddr.
// It strips ports, trims spaces, and validates IP format to prevent spoofing
// and port-based rate-limit bucket splitting.
func IPKeyFunc(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		cleaned := extractIP(ip)
		if net.ParseIP(cleaned) != nil {
			return cleaned
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For may contain multiple IPs (client, proxy1, proxy2).
		// Extract the first (client) IP.
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			cleaned := extractIP(parts[0])
			if net.ParseIP(cleaned) != nil {
				return cleaned
			}
		}
	}
	return extractIP(r.RemoteAddr)
}
