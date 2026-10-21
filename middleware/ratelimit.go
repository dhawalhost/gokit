package middleware

import (
	"net/http"
	"strconv"

	"github.com/dhawalhost/gokit/ratelimit"
)

// RateLimitConfig holds configuration for the rate-limit middleware.
type RateLimitConfig struct {
	// RequestsPerSecond is the sustained request rate allowed per key.
	RequestsPerSecond float64
	// Burst is the maximum burst size.
	Burst int
	// KeyFunc derives a per-request key (e.g. client IP). Defaults to IPKeyFunc.
	KeyFunc func(r *http.Request) string
	// Store is the backend that enforces the limit.
	Store ratelimit.Store
}

// RateLimit returns a middleware that enforces per-key rate limiting.
// On rate-limit rejection, it returns HTTP 429 with standard Retry-After
// and X-RateLimit-Limit headers.
func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
	if cfg.KeyFunc == nil {
		cfg.KeyFunc = IPKeyFunc
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := cfg.KeyFunc(r)
			allowed, err := cfg.Store.Allow(r.Context(), key, cfg.RequestsPerSecond, cfg.Burst)
			if err != nil || !allowed {
				if cfg.Burst > 0 {
					w.Header().Set("X-RateLimit-Limit", strconv.Itoa(cfg.Burst))
				}
				w.Header().Set("Retry-After", "1")
				writeJSONError(w, http.StatusTooManyRequests, `{"code":"TOO_MANY_REQUESTS","message":"rate limit exceeded"}`)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
