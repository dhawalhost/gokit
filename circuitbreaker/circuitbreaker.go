// Package circuitbreaker provides a thread-safe circuit-breaker implementation.
package circuitbreaker

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// State represents the circuit-breaker state.
type State int

const (
	// StateClosed means requests flow through normally.
	StateClosed State = iota
	// StateOpen means requests are blocked.
	StateOpen
	// StateHalfOpen means trial requests up to MaxHalfOpenRequests are allowed.
	StateHalfOpen
)

// Sentinel errors returned by this package.
var (
	// ErrOpen is returned when the circuit is open or half-open trial capacity is exhausted.
	ErrOpen = errors.New("circuitbreaker: circuit is open")

	// ErrExecutionTimeout is returned when a function exceeds the configured ExecutionTimeout.
	ErrExecutionTimeout = errors.New("circuitbreaker: execution timeout")
)

// Config holds circuit-breaker configuration.
type Config struct {
	// Name is an identifier used in callbacks and errors.
	Name string
	// MaxFailures is the number of consecutive failures before opening the circuit (default: 5).
	MaxFailures int
	// ResetTimeout is how long the circuit stays open before moving to half-open (default: 30s).
	ResetTimeout time.Duration
	// ResetTimeoutJitter adds randomized backoff fraction in [0, 1] on top of ResetTimeout
	// (e.g. 0.2 adds up to 20% jitter) so replicas do not stampede an injured downstream.
	ResetTimeoutJitter float64
	// ExecutionTimeout is the maximum time allowed for a single execution (0 = no timeout).
	ExecutionTimeout time.Duration
	// SlowCallThreshold marks calls taking >= SlowCallThreshold as circuit failures even if err == nil.
	SlowCallThreshold time.Duration
	// MaxHalfOpenRequests is the maximum concurrent trial requests permitted in StateHalfOpen (default: 1).
	MaxHalfOpenRequests int
	// FailureRatio, when > 0 (e.g. 0.5 for 50%), opens the circuit when at least MinRequests
	// have executed in the current RollingWindow and failure ratio >= FailureRatio.
	FailureRatio float64
	// MinRequests is the minimum sample count before FailureRatio is evaluated (default: 10 when FailureRatio > 0).
	MinRequests int
	// RollingWindow is the observation window for FailureRatio evaluation (default: 60s).
	RollingWindow time.Duration
	// OnStateChange is called whenever the state transitions (invoked without holding internal locks).
	OnStateChange func(name string, from, to State)
}

type stateTransition struct {
	from State
	to   State
}

// CircuitBreaker is a thread-safe circuit-breaker.
type CircuitBreaker struct {
	cfg               Config
	mu                sync.Mutex
	state             State
	failures          int
	lastFail          time.Time
	openWait          time.Duration
	halfOpenInFlight  int
	halfOpenSuccesses int
	windowStart       time.Time
	windowRequests    int
	windowFailures    int
}

// New creates a new CircuitBreaker with the given configuration.
func New(cfg Config) *CircuitBreaker {
	if cfg.MaxFailures == 0 {
		cfg.MaxFailures = 5
	}
	if cfg.ResetTimeout == 0 {
		cfg.ResetTimeout = 30 * time.Second
	}
	if cfg.ResetTimeoutJitter < 0 {
		cfg.ResetTimeoutJitter = 0
	} else if cfg.ResetTimeoutJitter > 1 {
		cfg.ResetTimeoutJitter = 1
	}
	if cfg.MaxHalfOpenRequests <= 0 {
		cfg.MaxHalfOpenRequests = 1
	}
	if cfg.FailureRatio > 0 {
		if cfg.MinRequests <= 0 {
			cfg.MinRequests = 10
		}
		if cfg.RollingWindow <= 0 {
			cfg.RollingWindow = 60 * time.Second
		}
	}
	return &CircuitBreaker{
		cfg:         cfg,
		windowStart: time.Now(),
	}
}

func (cb *CircuitBreaker) notifyTransition(tr *stateTransition) {
	if tr != nil && cb.cfg.OnStateChange != nil {
		cb.cfg.OnStateChange(cb.cfg.Name, tr.from, tr.to)
	}
}

// State returns the current circuit-breaker state.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	state, tr := cb.currentStateLocked()
	cb.mu.Unlock()
	cb.notifyTransition(tr)
	return state
}

// currentStateLocked must be called with cb.mu held.
func (cb *CircuitBreaker) currentStateLocked() (State, *stateTransition) {
	wait := cb.openWait
	if wait <= 0 {
		wait = cb.cfg.ResetTimeout
	}
	if cb.state == StateOpen && time.Since(cb.lastFail) >= wait {
		tr := cb.transitionLocked(StateHalfOpen)
		return cb.state, tr
	}
	return cb.state, nil
}

// Execute runs fn through the circuit breaker.
// If ExecutionTimeout is configured, Execute waits up to ExecutionTimeout; prefer ExecuteCtx
// so functions can honor context cancellation directly without spawning extra goroutines.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	return cb.execute(context.Background(), func(ctx context.Context) error {
		if cb.cfg.ExecutionTimeout <= 0 {
			return fn()
		}
		done := make(chan error, 1)
		go func() {
			done <- fn()
		}()
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return fmt.Errorf("circuitbreaker: %s: execution timeout after %v: %w", cb.cfg.Name, cb.cfg.ExecutionTimeout, ErrExecutionTimeout)
		}
	})
}

// ExecuteCtx runs fn with a context through the circuit breaker.
// When ExecutionTimeout is configured, the context passed to fn carries the deadline
// without spawning an extra goroutine.
func (cb *CircuitBreaker) ExecuteCtx(ctx context.Context, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return cb.execute(ctx, fn)
}

func (cb *CircuitBreaker) execute(ctx context.Context, fn func(context.Context) error) error {
	cb.mu.Lock()
	state, preTr := cb.currentStateLocked()
	if state == StateOpen {
		cb.mu.Unlock()
		cb.notifyTransition(preTr)
		return fmt.Errorf("circuitbreaker: %s: %w", cb.cfg.Name, ErrOpen)
	}
	isHalfOpenProbe := false
	if state == StateHalfOpen {
		if cb.halfOpenInFlight >= cb.cfg.MaxHalfOpenRequests {
			cb.mu.Unlock()
			cb.notifyTransition(preTr)
			return fmt.Errorf("circuitbreaker: %s: %w", cb.cfg.Name, ErrOpen)
		}
		cb.halfOpenInFlight++
		isHalfOpenProbe = true
	}
	cb.mu.Unlock()
	cb.notifyTransition(preTr)

	completed := false
	defer func() {
		if !completed && isHalfOpenProbe {
			cb.mu.Lock()
			if cb.halfOpenInFlight > 0 {
				cb.halfOpenInFlight--
			}
			cb.mu.Unlock()
		}
	}()

	execCtx := ctx
	var cancel context.CancelFunc
	if cb.cfg.ExecutionTimeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, cb.cfg.ExecutionTimeout)
	}
	callStart := time.Now()
	err := fn(execCtx)
	callDuration := time.Since(callStart)
	timedOut := cancel != nil && errors.Is(execCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil && (err == nil || errors.Is(err, context.DeadlineExceeded))
	if cancel != nil {
		cancel()
	}
	if timedOut {
		err = fmt.Errorf("circuitbreaker: %s: execution timeout after %v: %w", cb.cfg.Name, cb.cfg.ExecutionTimeout, ErrExecutionTimeout)
	}

	cb.mu.Lock()
	if isHalfOpenProbe && cb.halfOpenInFlight > 0 {
		cb.halfOpenInFlight--
	}
	completed = true
	slowCall := cb.cfg.SlowCallThreshold > 0 && callDuration >= cb.cfg.SlowCallThreshold
	var postTr *stateTransition
	if err != nil || slowCall {
		postTr = cb.onFailureLocked()
	} else {
		postTr = cb.onSuccessLocked()
	}
	cb.mu.Unlock()
	cb.notifyTransition(postTr)
	return err
}

func (cb *CircuitBreaker) onSuccessLocked() *stateTransition {
	cb.failures = 0
	if cb.state == StateHalfOpen {
		cb.halfOpenSuccesses++
		if cb.halfOpenSuccesses >= cb.cfg.MaxHalfOpenRequests {
			return cb.transitionLocked(StateClosed)
		}
		return nil
	}
	cb.recordWindowSampleLocked(false)
	return nil
}

func (cb *CircuitBreaker) onFailureLocked() *stateTransition {
	now := time.Now()
	cb.failures++
	cb.lastFail = now
	if cb.state == StateHalfOpen {
		return cb.transitionLocked(StateOpen)
	}
	cb.recordWindowSampleLocked(true)
	ratioTripped := cb.cfg.FailureRatio > 0 &&
		cb.windowRequests >= cb.cfg.MinRequests &&
		float64(cb.windowFailures)/float64(cb.windowRequests) >= cb.cfg.FailureRatio
	if cb.failures >= cb.cfg.MaxFailures || ratioTripped {
		return cb.transitionLocked(StateOpen)
	}
	return nil
}

func (cb *CircuitBreaker) recordWindowSampleLocked(failed bool) {
	if cb.cfg.FailureRatio <= 0 {
		return
	}
	now := time.Now()
	if now.Sub(cb.windowStart) >= cb.cfg.RollingWindow {
		cb.windowStart = now
		cb.windowRequests = 0
		cb.windowFailures = 0
	}
	cb.windowRequests++
	if failed {
		cb.windowFailures++
	}
}

func (cb *CircuitBreaker) transitionLocked(to State) *stateTransition {
	from := cb.state
	cb.state = to
	switch to {
	case StateClosed:
		cb.failures = 0
		cb.halfOpenInFlight = 0
		cb.halfOpenSuccesses = 0
		cb.windowStart = time.Now()
		cb.windowRequests = 0
		cb.windowFailures = 0
	case StateHalfOpen:
		cb.halfOpenInFlight = 0
		cb.halfOpenSuccesses = 0
	case StateOpen:
		cb.halfOpenInFlight = 0
		cb.halfOpenSuccesses = 0
		wait := cb.cfg.ResetTimeout
		if cb.cfg.ResetTimeoutJitter > 0 && wait > 0 {
			jitterMax := float64(wait) * cb.cfg.ResetTimeoutJitter
			wait += time.Duration(rand.Float64() * jitterMax) //nolint:gosec // jitter avoids thundering herd probes
		}
		cb.openWait = wait
	}
	if from == to {
		return nil
	}
	return &stateTransition{from: from, to: to}
}
