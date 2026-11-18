package middleware

import (
	"net/http"
	"net/netip"
	"strconv"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/logger"
	"github.com/dhawalhost/gokit/ratelimit"
)

var rateLimitStoreErrors atomic.Uint64

// RateLimitStoreErrors returns the total number of rate-limit store errors observed.
func RateLimitStoreErrors() uint64 {
	return rateLimitStoreErrors.Load()
}

// Bool returns a pointer to v for optional boolean config fields such as RateLimitConfig.FailOpen.
func Bool(v bool) *bool {
	return &v
}

// RateLimitConfig holds configuration for the rate-limit middleware.
type RateLimitConfig struct {
	// RequestsPerSecond is the sustained request rate allowed per key.
	RequestsPerSecond float64
	// Burst is the maximum burst size.
	Burst int
	// KeyFunc derives a per-request key (e.g. client IP). Defaults to IPKeyFunc
	// (or NewTrustedIPKeyFunc when TrustedProxies is non-empty).
	KeyFunc func(r *http.Request) string
	// TrustedProxies lists proxy CIDR prefixes whose X-Forwarded-For / X-Real-IP
	// headers are trusted when KeyFunc is nil.
	TrustedProxies []netip.Prefix
	// Store is the backend that enforces the limit.
	Store ratelimit.Store
	// FailOpen controls behavior when Store.Allow returns an error.
	// Defaults to true (nil or true fails open, allowing requests while logging the error).
	FailOpen *bool
	// FailClosed, when true, rejects requests with HTTP 503 Service Unavailable on store errors,
	// overriding the fail-open default.
	FailClosed bool
	// Logger logs rate-limit store errors (defaults to logger.FromContext(r.Context())).
	Logger *zap.Logger
	// OnStoreError is invoked whenever Store.Allow returns an error (e.g. to increment metrics).
	OnStoreError func(r *http.Request, err error)
}

// RateLimit returns a middleware that enforces per-key rate limiting.
// On rate-limit rejection, it returns HTTP 429 with standard Retry-After
// and X-RateLimit-Limit headers.
// On store errors, it defaults to failing open (logging and counting the error),
// or returns HTTP 503 Service Unavailable when fail-closed is configured.
func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
	if cfg.KeyFunc == nil {
		if len(cfg.TrustedProxies) > 0 {
			cfg.KeyFunc = NewTrustedIPKeyFunc(cfg.TrustedProxies)
		} else {
			cfg.KeyFunc = IPKeyFunc
		}
	}
	failClosed := cfg.FailClosed || (cfg.FailOpen != nil && !*cfg.FailOpen)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := cfg.KeyFunc(r)
			allowed, err := cfg.Store.Allow(r.Context(), key, cfg.RequestsPerSecond, cfg.Burst)
			if err != nil {
				rateLimitStoreErrors.Add(1)
				log := cfg.Logger
				if log == nil {
					log = logger.FromContext(r.Context())
				}
				log.Warn("rate limit store error",
					zap.String("key", key),
					zap.Bool("fail_closed", failClosed),
					zap.Error(err),
				)
				if cfg.OnStoreError != nil {
					cfg.OnStoreError(r, err)
				}
				if failClosed {
					writeJSONError(w, http.StatusServiceUnavailable, `{"code":"SERVICE_UNAVAILABLE","message":"rate limit store unavailable"}`)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
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
