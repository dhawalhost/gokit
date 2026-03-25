// Package circuitbreaker provides a thread-safe circuit-breaker implementation.
package circuitbreaker

import (
	"errors"
	"fmt"
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
	// StateHalfOpen means one trial request is allowed.
	StateHalfOpen
)

// ErrOpen is returned when the circuit is open.
var ErrOpen = errors.New("circuitbreaker: circuit is open")

// Config holds circuit-breaker configuration.
type Config struct {
	// Name is an identifier used in callbacks and errors.
	Name string
	// MaxFailures is the number of consecutive failures before opening the circuit.
	MaxFailures int
	// ResetTimeout is how long the circuit stays open before moving to half-open.
	ResetTimeout time.Duration
	// OnStateChange is called whenever the state transitions.
	OnStateChange func(name string, from, to State)
}

// CircuitBreaker is a thread-safe circuit-breaker.
type CircuitBreaker struct {
	cfg      Config
	mu       sync.Mutex
	state    State
	failures int
	lastFail time.Time
}

// New creates a new CircuitBreaker with the given configuration.
func New(cfg Config) *CircuitBreaker {
	if cfg.MaxFailures == 0 {
		cfg.MaxFailures = 5
	}
	if cfg.ResetTimeout == 0 {
		cfg.ResetTimeout = 30 * time.Second
	}
	return &CircuitBreaker{cfg: cfg}
}

// State returns the current circuit-breaker state.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.currentState()
}

// currentState must be called with cb.mu held.
func (cb *CircuitBreaker) currentState() State {
	if cb.state == StateOpen && time.Since(cb.lastFail) >= cb.cfg.ResetTimeout {
		cb.transition(StateHalfOpen)
	}
	return cb.state
}

// Execute runs fn through the circuit breaker.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	cb.mu.Lock()
	state := cb.currentState()
	if state == StateOpen {
		cb.mu.Unlock()
		return fmt.Errorf("circuitbreaker: %s: %w", cb.cfg.Name, ErrOpen)
	}
	cb.mu.Unlock()

	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if err != nil {
		cb.onFailure()
	} else {
		cb.onSuccess()
	}
	return err
}

func (cb *CircuitBreaker) onSuccess() {
	cb.failures = 0
	if cb.state != StateClosed {
		cb.transition(StateClosed)
	}
}

func (cb *CircuitBreaker) onFailure() {
	cb.failures++
	cb.lastFail = time.Now()
	if cb.state == StateHalfOpen || cb.failures >= cb.cfg.MaxFailures {
		cb.transition(StateOpen)
	}
}

func (cb *CircuitBreaker) transition(to State) {
	from := cb.state
	cb.state = to
	if cb.cfg.OnStateChange != nil {
		cb.cfg.OnStateChange(cb.cfg.Name, from, to)
	}
}
