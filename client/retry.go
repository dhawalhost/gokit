package client

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"
)

// calculateBackoff computes the delay before retry attempt (0-indexed).
func (rp *RetryPolicy) calculateBackoff(attempt int, resp *http.Response) time.Duration {
	// 1. Check if the server suggested a Retry-After header
	if resp != nil {
		if delay, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok && delay > 0 {
			if rp.MaxBackoff > 0 && delay > rp.MaxBackoff {
				return rp.MaxBackoff
			}
			return delay
		}
	}

	initial := rp.InitialBackoff
	if initial <= 0 {
		initial = 100 * time.Millisecond
	}

	factor := rp.BackoffFactor
	if factor <= 1.0 {
		factor = 2.0
	}

	// Exponential backoff: initial * (factor ^ attempt)
	backoff := float64(initial)
	for i := 0; i < attempt; i++ {
		backoff *= factor
		if rp.MaxBackoff > 0 && backoff > float64(rp.MaxBackoff) {
			backoff = float64(rp.MaxBackoff)
			break
		}
	}

	maxDelay := time.Duration(backoff)
	if rp.MaxBackoff > 0 && maxDelay > rp.MaxBackoff {
		maxDelay = rp.MaxBackoff
	}

	// Full jitter: uniformly random between [0, maxDelay]
	if rp.Jitter && maxDelay > 0 {
		// Use pseudo-random jitter
		jittered := rand.Int63n(int64(maxDelay)) //nolint:gosec // math/rand is sufficient for network backoff jitter
		return time.Duration(jittered)
	}

	return maxDelay
}

// parseRetryAfter attempts to parse a Retry-After header value as either
// integer seconds or an HTTP-date.
func parseRetryAfter(val string) (time.Duration, bool) {
	if val == "" {
		return 0, false
	}

	// Try seconds first
	if seconds, err := strconv.Atoi(val); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}

	// Try RFC1123 HTTP-date
	if t, err := http.ParseTime(val); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}

	return 0, false
}

// isRetryable evaluates whether a response or error warrants a retry attempt.
func (rp *RetryPolicy) isRetryable(ctx context.Context, resp *http.Response, err error) bool {
	// If context was explicitly canceled or deadline exceeded, do not retry
	if ctx != nil && ctx.Err() != nil {
		return false
	}

	// Custom user condition takes precedence if configured
	if rp.RetryCondition != nil {
		return rp.RetryCondition(resp, err)
	}

	// Network / connection errors
	if err != nil {
		// Check for connection refused, reset, or timeout
		var netErr net.Error
		if errors.As(err, &netErr) {
			return true
		}
		if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
			return true
		}
		return false
	}

	// Status code check
	if resp != nil {
		for _, code := range rp.RetryableStatusCodes {
			if resp.StatusCode == code {
				return true
			}
		}
	}

	return false
}
