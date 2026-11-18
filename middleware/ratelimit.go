package middleware

import (
	"context"
	"net/http"
	"net/netip"
	"strconv"
	"sync/atomic"
	"time"

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
	// FallbackStore is an optional local limiter (e.g. ratelimit.NewInMemoryStore()) used
	// when Store fails in FailOpen mode instead of admitting unbounded traffic.
	FallbackStore ratelimit.Store
	// FallbackNodeCount divides RequestsPerSecond and Burst across gateway instances
	// when evaluating FallbackStore (defaults to 1 when <= 1).
	FallbackNodeCount int
	// CostFunc optionally computes the token weight of each request (defaults to 1).
	CostFunc func(r *http.Request) int
	// Logger logs rate-limit store errors (defaults to logger.FromContext(r.Context())).
	Logger *zap.Logger
	// OnStoreError is invoked whenever Store.Allow returns an error (e.g. to increment metrics).
	OnStoreError func(r *http.Request, err error)
}

// RateLimit returns a middleware that enforces per-key rate limiting.
// It emits X-RateLimit-Limit (and X-RateLimit-Remaining / X-RateLimit-Reset when supported
// by DetailedStore) and returns HTTP 429 with Retry-After on rejection.
// On store errors, it defaults to failing open (evaluating FallbackStore with per-node
// quotas if configured, otherwise allowing requests), or returns HTTP 503 when fail-closed.
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
			cost := 1
			if cfg.CostFunc != nil {
				if c := cfg.CostFunc(r); c > 1 {
					cost = c
				}
			}

			res, detailed, err := evalRateLimit(r.Context(), cfg.Store, key, cost, cfg.RequestsPerSecond, cfg.Burst)
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
				if cfg.FallbackStore != nil {
					nodes := cfg.FallbackNodeCount
					if nodes < 1 {
						nodes = 1
					}
					fbRPS := cfg.RequestsPerSecond / float64(nodes)
					fbBurst := cfg.Burst / nodes
					if cfg.Burst > 0 && fbBurst < 1 {
						fbBurst = 1
					}
					fbRes, fbDetailed, fbErr := evalRateLimit(r.Context(), cfg.FallbackStore, key, cost, fbRPS, fbBurst)
					if fbErr == nil {
						serveRateLimitDecision(w, r, next, fbRes, fbDetailed, fbBurst)
						return
					}
				}
				next.ServeHTTP(w, r)
				return
			}

			serveRateLimitDecision(w, r, next, res, detailed, cfg.Burst)
		})
	}
}

func evalRateLimit(ctx context.Context, store ratelimit.Store, key string, cost int, rps float64, burst int) (ratelimit.Result, bool, error) {
	if ds, ok := store.(ratelimit.DetailedStore); ok {
		res, err := ds.AllowN(ctx, key, cost, rps, burst)
		return res, true, err
	}
	allowed, err := store.Allow(ctx, key, rps, burst)
	return ratelimit.Result{
		Allowed:    allowed,
		Limit:      burst,
		RetryAfter: time.Second,
	}, false, err
}

func serveRateLimitDecision(w http.ResponseWriter, r *http.Request, next http.Handler, res ratelimit.Result, detailed bool, burst int) {
	limit := res.Limit
	if limit <= 0 {
		limit = burst
	}
	if limit > 0 {
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	}
	if detailed {
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(ceilSeconds(res.ResetAfter, false), 10))
	}
	if !res.Allowed {
		w.Header().Set("Retry-After", strconv.FormatInt(ceilSeconds(res.RetryAfter, true), 10))
		writeJSONError(w, http.StatusTooManyRequests, `{"code":"TOO_MANY_REQUESTS","message":"rate limit exceeded"}`)
		return
	}
	next.ServeHTTP(w, r)
}

func ceilSeconds(d time.Duration, minOne bool) int64 {
	if d <= 0 {
		if minOne {
			return 1
		}
		return 0
	}
	sec := int64((d + time.Second - 1) / time.Second)
	if minOne && sec < 1 {
		return 1
	}
	return sec
}
