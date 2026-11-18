package middleware

import (
	"net"
	"net/http"
	"net/netip"
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

// IPKeyFunc returns the direct peer IP address from r.RemoteAddr.
// Forwarded headers (X-Forwarded-For, X-Real-IP) are intentionally ignored because
// untrusted clients can spoof them to bypass rate limiting. Use NewTrustedIPKeyFunc
// when running behind trusted reverse proxies.
func IPKeyFunc(r *http.Request) string {
	return extractIP(r.RemoteAddr)
}

// NewTrustedIPKeyFunc returns a key function that inspects X-Forwarded-For (right-to-left,
// returning the first untrusted hop) and X-Real-IP only when r.RemoteAddr belongs to one
// of trustedProxies. If r.RemoteAddr is not a trusted proxy or no forwarded IP is valid,
// it falls back to r.RemoteAddr.
func NewTrustedIPKeyFunc(trustedProxies []netip.Prefix) func(*http.Request) string {
	prefixes := make([]netip.Prefix, len(trustedProxies))
	copy(prefixes, trustedProxies)

	return func(r *http.Request) string {
		remoteIP := extractIP(r.RemoteAddr)
		if len(prefixes) == 0 {
			return remoteIP
		}

		remoteAddr, err := netip.ParseAddr(remoteIP)
		if err != nil {
			return remoteIP
		}
		remoteAddr = remoteAddr.Unmap()
		if !isTrustedProxy(remoteAddr, prefixes) {
			return remoteAddr.String()
		}

		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			candidate := remoteAddr.String()
			for i := len(parts) - 1; i >= 0; i-- {
				hopStr := extractIP(parts[i])
				hopAddr, parseErr := netip.ParseAddr(hopStr)
				if parseErr != nil {
					return candidate
				}
				hopAddr = hopAddr.Unmap()
				if !isTrustedProxy(hopAddr, prefixes) {
					return hopAddr.String()
				}
				candidate = hopAddr.String()
			}
			return candidate
		}

		if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
			if realAddr, parseErr := netip.ParseAddr(extractIP(xrip)); parseErr == nil {
				return realAddr.Unmap().String()
			}
		}

		return remoteAddr.String()
	}
}

func isTrustedProxy(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
