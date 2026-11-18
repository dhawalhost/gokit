package client

import (
	"context"
	"errors"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type budgetBucket struct {
	slot     int64
	requests int64
	retries  int64
}

// RetryBudget limits aggregate retry amplification over a sliding window.
type RetryBudget struct {
	mu            sync.Mutex
	maxRetryRatio float64
	minRetries    float64
	bucketWidth   time.Duration
	buckets       []budgetBucket
}

// NewRetryBudget constructs a sliding-window RetryBudget permitting up to maxRetryRatio
// of recent base requests as retries, with an optional minRetriesPerSec baseline floor.
func NewRetryBudget(maxRetryRatio float64, minRetriesPerSec int, window time.Duration) *RetryBudget {
	const numBuckets = 10
	if window <= 0 {
		window = 10 * time.Second
	}
	width := window / numBuckets
	if width <= 0 {
		width = time.Millisecond
		window = numBuckets * width
	}
	if maxRetryRatio < 0 {
		maxRetryRatio = 0
	}
	if maxRetryRatio == 0 && minRetriesPerSec <= 0 {
		maxRetryRatio = 0.10
	}
	minRetries := float64(minRetriesPerSec) * window.Seconds()
	if minRetries < 0 {
		minRetries = 0
	}

	buckets := make([]budgetBucket, numBuckets)
	for i := range buckets {
		buckets[i].slot = -1
	}

	return &RetryBudget{
		maxRetryRatio: maxRetryRatio,
		minRetries:    minRetries,
		bucketWidth:   width,
		buckets:       buckets,
	}
}

// RecordRequest records an initial outbound request into the budget window.
func (b *RetryBudget) RecordRequest() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentBucketLocked(time.Now()).requests++
}

// TryAcquireRetry atomically checks if a retry is within budget and consumes one retry slot.
func (b *RetryBudget) TryAcquireRetry() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	reqCount, retryCount := b.totalsLocked(now)
	ratioAllowed := math.Floor(b.maxRetryRatio * float64(reqCount))
	allowed := math.Max(b.minRetries, ratioAllowed)
	if float64(retryCount+1) > allowed {
		return false
	}
	b.currentBucketLocked(now).retries++
	return true
}

func (b *RetryBudget) slotFor(now time.Time) int64 {
	return now.UnixNano() / b.bucketWidth.Nanoseconds()
}

func (b *RetryBudget) currentBucketLocked(now time.Time) *budgetBucket {
	slot := b.slotFor(now)
	n := int64(len(b.buckets))
	idx := int(((slot % n) + n) % n)
	if b.buckets[idx].slot != slot {
		b.buckets[idx] = budgetBucket{slot: slot}
	}
	return &b.buckets[idx]
}

func (b *RetryBudget) totalsLocked(now time.Time) (int64, int64) {
	curr := b.slotFor(now)
	n := int64(len(b.buckets))
	var reqs, retries int64
	for i := range b.buckets {
		if b.buckets[i].slot >= 0 && curr-b.buckets[i].slot < n {
			reqs += b.buckets[i].requests
			retries += b.buckets[i].retries
		}
	}
	return reqs, retries
}

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

var defaultRetryableMethods = []string{
	http.MethodGet,
	http.MethodHead,
	http.MethodOptions,
	http.MethodPut,
	http.MethodDelete,
}

func (rp *RetryPolicy) isMethodRetryable(req *http.Request) bool {
	if rp.RetryNonIdempotent || req == nil {
		return true
	}
	if req.Header.Get("Idempotency-Key") != "" || req.Header.Get("X-Idempotency-Key") != "" {
		return true
	}

	methods := rp.RetryableMethods
	if len(methods) == 0 {
		methods = defaultRetryableMethods
	}
	method := strings.ToUpper(req.Method)
	for _, m := range methods {
		if strings.ToUpper(m) == method {
			return true
		}
	}
	return false
}

// isRetryable evaluates whether a response or error warrants a retry attempt.
func (rp *RetryPolicy) isRetryable(ctx context.Context, req *http.Request, resp *http.Response, err error) bool {
	// If context was explicitly canceled or deadline exceeded, do not retry
	if ctx != nil && ctx.Err() != nil {
		return false
	}

	// Custom user condition takes precedence if configured
	if rp.RetryCondition != nil {
		return rp.RetryCondition(resp, err)
	}

	// Never retry non-idempotent methods unless an Idempotency-Key is present or explicitly opted in.
	if !rp.isMethodRetryable(req) {
		return false
	}

	// Network / connection errors
	if err != nil {
		// Check for connection refused, reset, EOF, or timeout
		var netErr net.Error
		if errors.As(err, &netErr) {
			return true
		}
		if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
			errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
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
