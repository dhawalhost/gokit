// Package diagnostics provides runtime memory watchdog automation, goroutine leak detection,
// on-demand profiling helpers, and authenticated diagnostic HTTP endpoints for Go microservices.
package diagnostics

import (
	"errors"
	"fmt"
)

var (
	// ErrWatermarkExceeded is returned when runtime heap or system memory breaches the configured threshold.
	ErrWatermarkExceeded = errors.New("diagnostics: memory watermark threshold exceeded")
	// ErrUnauthorized is returned when accessing protected diagnostic endpoints without a valid token.
	ErrUnauthorized = errors.New("diagnostics: unauthorized access to diagnostic endpoints")
	// ErrForbidden is returned when the client IP is not in the allowed diagnostic CIDR subnets.
	ErrForbidden = errors.New("diagnostics: forbidden; client IP not in allowlist")
	// ErrGoroutineLeak is returned when leaked goroutines are detected beyond the allowed tolerance.
	ErrGoroutineLeak = errors.New("diagnostics: goroutine leak detected")
	// ErrProfilingInProgress is returned when a CPU profile or dump is already running.
	ErrProfilingInProgress = errors.New("diagnostics: profiling session already in progress")
	// ErrWatchdogStopped is returned when operating on a stopped memory watchdog.
	ErrWatchdogStopped = errors.New("diagnostics: memory watchdog is stopped")
	// ErrNilWriter is returned when a nil destination writer is provided for profile dumps.
	ErrNilWriter = errors.New("diagnostics: destination writer cannot be nil")
)

// GoroutineLeakError provides rich diagnostic details on leaked goroutine stack traces.
type GoroutineLeakError struct {
	LeakedCount int
	Details     string
}

func (e *GoroutineLeakError) Error() string {
	return fmt.Sprintf("diagnostics: detected %d leaked goroutines:\n%s", e.LeakedCount, e.Details)
}

func (e *GoroutineLeakError) Unwrap() error {
	return ErrGoroutineLeak
}
