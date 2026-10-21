// Package client provides a resilient, production-ready outbound HTTP client
// designed for distributed architectures and microservices.
package client

import (
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/circuitbreaker"
)

// RetryPolicy defines the rules for retrying failed HTTP requests.
type RetryPolicy struct {
	// MaxRetries is the maximum number of retry attempts (excluding the initial request).
	MaxRetries int
	// InitialBackoff is the minimum wait duration before the first retry.
	InitialBackoff time.Duration
	// MaxBackoff is the upper bound on backoff duration.
	MaxBackoff time.Duration
	// BackoffFactor is the multiplier for exponential backoff.
	BackoffFactor float64
	// Jitter enables randomized full jitter to prevent thundering herds.
	Jitter bool
	// RetryableStatusCodes specifies HTTP status codes that trigger a retry.
	RetryableStatusCodes []int
	// RetryCondition allows custom logic to determine whether an error/response is retryable.
	RetryCondition func(resp *http.Response, err error) bool
}

// DefaultRetryPolicy returns a sensible default retry policy for production.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     3 * time.Second,
		BackoffFactor:  2.0,
		Jitter:         true,
		RetryableStatusCodes: []int{
			http.StatusRequestTimeout,      // 408
			http.StatusTooManyRequests,     // 429
			http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusServiceUnavailable,  // 503
			http.StatusGatewayTimeout,      // 504
		},
	}
}

// Config contains the configuration settings for the HTTP Client.
type Config struct {
	// Name identifies the downstream service (used for circuit breaker and logging).
	Name string
	// Timeout is the end-to-end timeout for a request including all retries.
	Timeout time.Duration
	// AttemptTimeout is the timeout for a single HTTP attempt (0 means no per-attempt timeout).
	AttemptTimeout time.Duration
	// Retry is the retry policy.
	Retry RetryPolicy
	// CircuitBreaker is an optional circuit breaker protecting calls to the downstream service.
	CircuitBreaker *circuitbreaker.CircuitBreaker
	// BaseURL is an optional base URL prefixed to relative request paths.
	BaseURL string
	// DefaultHeaders are headers added to every outbound request.
	DefaultHeaders http.Header
	// Logger is the logger used for request logging and debugging.
	Logger *zap.Logger
	// PropagateRequestID enables automatic propagation of X-Request-ID from context.
	PropagateRequestID bool
	// PropagateTracing enables automatic propagation of W3C TraceContext headers from context.
	PropagateTracing bool
	// Transport is the underlying http.RoundTripper. If nil, DefaultTransport() is used.
	Transport http.RoundTripper
}

// Option configures a Client.
type Option func(*Config)

// WithName sets the downstream service name.
func WithName(name string) Option {
	return func(c *Config) {
		c.Name = name
	}
}

// WithTimeout sets the overall client timeout across all retry attempts.
func WithTimeout(d time.Duration) Option {
	return func(c *Config) {
		c.Timeout = d
	}
}

// WithAttemptTimeout sets the timeout for each individual attempt.
func WithAttemptTimeout(d time.Duration) Option {
	return func(c *Config) {
		c.AttemptTimeout = d
	}
}

// WithRetryPolicy sets the retry policy.
func WithRetryPolicy(rp RetryPolicy) Option {
	return func(c *Config) {
		c.Retry = rp
	}
}

// WithMaxRetries sets the maximum number of retry attempts.
func WithMaxRetries(n int) Option {
	return func(c *Config) {
		c.Retry.MaxRetries = n
	}
}

// WithCircuitBreaker binds a circuit breaker to the client.
func WithCircuitBreaker(cb *circuitbreaker.CircuitBreaker) Option {
	return func(c *Config) {
		c.CircuitBreaker = cb
	}
}

// WithBaseURL sets the base URL for the client.
func WithBaseURL(rawURL string) Option {
	return func(c *Config) {
		c.BaseURL = rawURL
	}
}

// WithDefaultHeader adds a default header to all outbound requests.
func WithDefaultHeader(key, value string) Option {
	return func(c *Config) {
		if c.DefaultHeaders == nil {
			c.DefaultHeaders = make(http.Header)
		}
		c.DefaultHeaders.Set(key, value)
	}
}

// WithLogger sets the logger for the client.
func WithLogger(l *zap.Logger) Option {
	return func(c *Config) {
		c.Logger = l
	}
}

// WithTransport sets a custom RoundTripper for the client.
func WithTransport(rt http.RoundTripper) Option {
	return func(c *Config) {
		c.Transport = rt
	}
}

// WithTracePropagation controls whether W3C tracecontext headers are injected.
func WithTracePropagation(enabled bool) Option {
	return func(c *Config) {
		c.PropagateTracing = enabled
	}
}

// WithRequestIDPropagation controls whether X-Request-ID is forwarded from context.
func WithRequestIDPropagation(enabled bool) Option {
	return func(c *Config) {
		c.PropagateRequestID = enabled
	}
}
