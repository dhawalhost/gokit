package diagnostics

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"time"
)

// GoroutineSnapshot represents a point-in-time capture of all active goroutines and their stack traces.
type GoroutineSnapshot struct {
	Count  int
	Stacks map[string]string // Key: goroutine ID or header, Value: full stack trace
}

// CaptureGoroutines captures all currently running goroutines and parses their individual stack traces.
func CaptureGoroutines() *GoroutineSnapshot {
	buf := make([]byte, 1024*64)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}

	stacks := parseGoroutineStacks(buf)
	return &GoroutineSnapshot{
		Count:  len(stacks),
		Stacks: stacks,
	}
}

func parseGoroutineStacks(raw []byte) map[string]string {
	result := make(map[string]string)
	sections := bytes.Split(raw, []byte("\n\n"))

	for _, sec := range sections {
		trimmed := bytes.TrimSpace(sec)
		if len(trimmed) == 0 {
			continue
		}
		lines := bytes.SplitN(trimmed, []byte("\n"), 2)
		header := string(lines[0])
		result[header] = string(trimmed)
	}

	return result
}

// LeakOption configures goroutine leak verification.
type LeakOption func(*leakConfig)

type leakConfig struct {
	tolerance       int
	settleTimeout   time.Duration
	ignoredPatterns []string
}

// WithTolerance allows up to n leaked goroutines without triggering an error (useful for runtime system routines).
func WithTolerance(n int) LeakOption {
	return func(c *leakConfig) {
		if n >= 0 {
			c.tolerance = n
		}
	}
}

// WithSettleTimeout configures a grace period to wait for asynchronous goroutines to terminate before verification.
func WithSettleTimeout(d time.Duration) LeakOption {
	return func(c *leakConfig) {
		if d > 0 {
			c.settleTimeout = d
		}
	}
}

// WithIgnoredPatterns specifies substring patterns in stack traces that should be ignored during leak evaluation.
func WithIgnoredPatterns(patterns ...string) LeakOption {
	return func(c *leakConfig) {
		c.ignoredPatterns = append(c.ignoredPatterns, patterns...)
	}
}

// VerifyNoGoroutineLeak compares current active goroutines against a baseline snapshot,
// returning a rich GoroutineLeakError if excess goroutines are still executing.
func VerifyNoGoroutineLeak(baseline *GoroutineSnapshot, opts ...LeakOption) error {
	cfg := leakConfig{
		tolerance:     0,
		settleTimeout: 100 * time.Millisecond,
		ignoredPatterns: []string{
			"runtime.ensureSigM",
			"runtime/pprof.writeHeap",
			"signal.loop",
		},
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	deadline := time.Now().Add(cfg.settleTimeout)
	var lastLeaked []string

	for {
		current := CaptureGoroutines()
		var leaked []string

		for header, stack := range current.Stacks {
			if baseline != nil {
				if _, existed := baseline.Stacks[header]; existed {
					continue
				}
			}

			// Check ignored patterns
			ignored := false
			for _, pat := range cfg.ignoredPatterns {
				if strings.Contains(stack, pat) {
					ignored = true
					break
				}
			}

			if !ignored {
				leaked = append(leaked, stack)
			}
		}

		if len(leaked) <= cfg.tolerance {
			return nil
		}

		lastLeaked = leaked
		if time.Now().After(deadline) {
			break
		}

		time.Sleep(15 * time.Millisecond)
	}

	var sb strings.Builder
	for i, stack := range lastLeaked {
		fmt.Fprintf(&sb, "--- Leaked Goroutine #%d ---\n%s\n\n", i+1, stack)
	}

	return &GoroutineLeakError{
		LeakedCount: len(lastLeaked),
		Details:     sb.String(),
	}
}
