package loadshed

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// PriorityExtractor extracts a Priority level from an HTTP request.
type PriorityExtractor func(r *http.Request) Priority

// MiddlewareConfig holds configuration for the loadshedding HTTP middleware.
type MiddlewareConfig struct {
	Extractor    PriorityExtractor
	StatusCode   int
	RetryAfter   string
	OnShed       func(r *http.Request, stats Stats)
	ErrorHandler func(w http.ResponseWriter, r *http.Request, stats Stats)
}

// MiddlewareOption configures the HTTP middleware.
type MiddlewareOption func(*MiddlewareConfig)

// WithPriorityExtractor sets a custom priority extractor function.
func WithPriorityExtractor(fn PriorityExtractor) MiddlewareOption {
	return func(c *MiddlewareConfig) {
		if fn != nil {
			c.Extractor = fn
		}
	}
}

// WithStatusCode overrides the default HTTP status code when a request is shed (default: 503).
func WithStatusCode(code int) MiddlewareOption {
	return func(c *MiddlewareConfig) {
		if code > 0 {
			c.StatusCode = code
		}
	}
}

// WithRetryAfter sets the Retry-After header value in seconds (default: "1").
func WithRetryAfter(seconds string) MiddlewareOption {
	return func(c *MiddlewareConfig) {
		c.RetryAfter = seconds
	}
}

// WithOnShed registers a hook called whenever a request is shed.
func WithOnShed(fn func(r *http.Request, stats Stats)) MiddlewareOption {
	return func(c *MiddlewareConfig) {
		c.OnShed = fn
	}
}

// WithErrorHandler sets a custom handler for writing shed error responses.
func WithErrorHandler(fn func(w http.ResponseWriter, r *http.Request, stats Stats)) MiddlewareOption {
	return func(c *MiddlewareConfig) {
		c.ErrorHandler = fn
	}
}

// DefaultPriorityExtractor inspects the X-Priority header and request path.
func DefaultPriorityExtractor(r *http.Request) Priority {
	val := strings.ToLower(r.Header.Get("X-Priority"))
	switch val {
	case "critical", "high":
		return PriorityCritical
	case "low", "batch", "background":
		return PriorityLow
	}

	// Health check endpoints are always treated as Critical so pods don't get killed during load spikes
	p := r.URL.Path
	if p == "/health" || p == "/healthz" || p == "/live" || p == "/ready" || p == "/readyz" {
		return PriorityCritical
	}

	return PriorityStandard
}

// statusWriter wraps http.ResponseWriter to capture status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Middleware returns a standard HTTP middleware that rejects requests when system load spikes.
func Middleware(limiter *Limiter, opts ...MiddlewareOption) func(http.Handler) http.Handler {
	cfg := MiddlewareConfig{
		Extractor:  DefaultPriorityExtractor,
		StatusCode: http.StatusServiceUnavailable,
		RetryAfter: "1",
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			priority := cfg.Extractor(r)

			done, err := limiter.Acquire(r.Context(), priority)
			if err != nil {
				stats := limiter.Stats()

				w.Header().Set("Retry-After", cfg.RetryAfter)
				w.Header().Set("X-LoadShed", "true")
				w.Header().Set("X-LoadShed-Limit", fmt.Sprintf("%.0f", stats.CurrentLimit))
				w.Header().Set("X-LoadShed-InFlight", fmt.Sprintf("%d", stats.InFlight))

				if cfg.OnShed != nil {
					cfg.OnShed(r, stats)
				}

				if cfg.ErrorHandler != nil {
					cfg.ErrorHandler(w, r, stats)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(cfg.StatusCode)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":       "system_overloaded",
					"message":     "Service is experiencing extreme load. Please retry shortly.",
					"retry_after": cfg.RetryAfter,
				})
				return
			}

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			defer func() {
				var execErr error
				if sw.status >= http.StatusInternalServerError {
					execErr = errors.New("server error")
				}
				done(execErr)
			}()

			next.ServeHTTP(sw, r)
		})
	}
}
