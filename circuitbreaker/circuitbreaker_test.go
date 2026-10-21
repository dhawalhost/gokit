package circuitbreaker_test

import (
	"errors"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/circuitbreaker"
)

func TestCircuitBreaker_StateTransitions_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "initial state defaults to closed",
			run: func(t *testing.T) {
				cb := circuitbreaker.New(circuitbreaker.Config{Name: "test-init"})
				if cb.State() != circuitbreaker.StateClosed {
					t.Fatalf("expected StateClosed, got %v", cb.State())
				}
			},
		},
		{
			name: "consecutive successes maintain closed state",
			run: func(t *testing.T) {
				cb := circuitbreaker.New(circuitbreaker.Config{Name: "test-success", MaxFailures: 3})
				for i := 0; i < 5; i++ {
					if err := cb.Execute(func() error { return nil }); err != nil {
						t.Fatalf("unexpected error on success iteration %d: %v", i, err)
					}
				}
				if cb.State() != circuitbreaker.StateClosed {
					t.Fatalf("expected StateClosed, got %v", cb.State())
				}
			},
		},
		{
			name: "reaching max failures trips state to open",
			run: func(t *testing.T) {
				cb := circuitbreaker.New(circuitbreaker.Config{Name: "test-trip", MaxFailures: 3})
				fail := errors.New("upstream failure")
				for i := 0; i < 3; i++ {
					_ = cb.Execute(func() error { return fail })
				}
				if cb.State() != circuitbreaker.StateOpen {
					t.Fatalf("expected StateOpen after 3 failures, got %v", cb.State())
				}
			},
		},
		{
			name: "open state rejects requests with ErrOpen",
			run: func(t *testing.T) {
				cb := circuitbreaker.New(circuitbreaker.Config{Name: "test-reject", MaxFailures: 1})
				_ = cb.Execute(func() error { return errors.New("initial failure") })

				err := cb.Execute(func() error { return nil })
				if !errors.Is(err, circuitbreaker.ErrOpen) {
					t.Fatalf("expected ErrOpen, got %v", err)
				}
			},
		},
		{
			name: "reset timeout transitions open breaker to half-open",
			run: func(t *testing.T) {
				cb := circuitbreaker.New(circuitbreaker.Config{
					Name:         "test-half-open",
					MaxFailures:  1,
					ResetTimeout: 10 * time.Millisecond,
				})
				_ = cb.Execute(func() error { return errors.New("fail") })
				if cb.State() != circuitbreaker.StateOpen {
					t.Fatal("expected StateOpen")
				}
				time.Sleep(20 * time.Millisecond)
				if cb.State() != circuitbreaker.StateHalfOpen {
					t.Fatalf("expected StateHalfOpen after reset timeout, got %v", cb.State())
				}
			},
		},
		{
			name: "successful probe in half-open state closes breaker",
			run: func(t *testing.T) {
				cb := circuitbreaker.New(circuitbreaker.Config{
					Name:         "test-recover",
					MaxFailures:  1,
					ResetTimeout: 10 * time.Millisecond,
				})
				_ = cb.Execute(func() error { return errors.New("fail") })
				time.Sleep(20 * time.Millisecond)

				if err := cb.Execute(func() error { return nil }); err != nil {
					t.Fatalf("unexpected error in half-open probe: %v", err)
				}
				if cb.State() != circuitbreaker.StateClosed {
					t.Fatalf("expected StateClosed after probe success, got %v", cb.State())
				}
			},
		},
		{
			name: "failed probe in half-open state re-opens breaker",
			run: func(t *testing.T) {
				cb := circuitbreaker.New(circuitbreaker.Config{
					Name:         "test-reopen",
					MaxFailures:  1,
					ResetTimeout: 10 * time.Millisecond,
				})
				_ = cb.Execute(func() error { return errors.New("fail") })
				time.Sleep(20 * time.Millisecond)

				_ = cb.Execute(func() error { return errors.New("fail again") })
				if cb.State() != circuitbreaker.StateOpen {
					t.Fatalf("expected StateOpen after half-open probe failure, got %v", cb.State())
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestCircuitBreaker_ExecutionModes_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "execution timeout trips ErrExecutionTimeout",
			run: func(t *testing.T) {
				cb := circuitbreaker.New(circuitbreaker.Config{
					Name:             "test-timeout",
					MaxFailures:      5,
					ExecutionTimeout: 10 * time.Millisecond,
				})
				err := cb.Execute(func() error {
					time.Sleep(100 * time.Millisecond)
					return nil
				})
				if !errors.Is(err, circuitbreaker.ErrExecutionTimeout) {
					t.Fatalf("expected ErrExecutionTimeout, got %v", err)
				}
			},
		},
		{
			name: "onStateChange callback tracks transitions accurately",
			run: func(t *testing.T) {
				type transition struct {
					from circuitbreaker.State
					to   circuitbreaker.State
				}
				var transitions []transition
				cb := circuitbreaker.New(circuitbreaker.Config{
					Name:        "cb-callback",
					MaxFailures: 1,
					OnStateChange: func(_ string, from, to circuitbreaker.State) {
						transitions = append(transitions, transition{from: from, to: to})
					},
					ResetTimeout: 10 * time.Millisecond,
				})

				_ = cb.Execute(func() error { return errors.New("trip") })
				time.Sleep(20 * time.Millisecond)
				_ = cb.Execute(func() error { return nil })

				expected := []transition{
					{from: circuitbreaker.StateClosed, to: circuitbreaker.StateOpen},
					{from: circuitbreaker.StateOpen, to: circuitbreaker.StateHalfOpen},
					{from: circuitbreaker.StateHalfOpen, to: circuitbreaker.StateClosed},
				}
				if len(transitions) != len(expected) {
					t.Fatalf("expected %d transitions, got %v", len(expected), transitions)
				}
				for i, exp := range expected {
					if transitions[i] != exp {
						t.Errorf("transition %d: expected %+v, got %+v", i, exp, transitions[i])
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
