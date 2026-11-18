package circuitbreaker_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

func TestGK13_CircuitBreakerHalfOpenAndCallback(t *testing.T) {
	t.Run("half-open admits at most MaxHalfOpenRequests (default 1)", func(t *testing.T) {
		cb := circuitbreaker.New(circuitbreaker.Config{
			Name:         "gk13-halfopen",
			MaxFailures:  1,
			ResetTimeout: 10 * time.Millisecond,
		})
		_ = cb.Execute(func() error { return errors.New("trip") })
		time.Sleep(20 * time.Millisecond)

		var entered atomic.Int32
		release := make(chan struct{})
		const total = 100
		var wg sync.WaitGroup
		wg.Add(total)
		start := make(chan struct{})
		for i := 0; i < total; i++ {
			go func() {
				defer wg.Done()
				<-start
				_ = cb.Execute(func() error {
					entered.Add(1)
					<-release
					return nil
				})
			}()
		}
		close(start)
		time.Sleep(30 * time.Millisecond)
		close(release)
		wg.Wait()

		if got := entered.Load(); got != 1 {
			t.Fatalf("GK-13: expected at most 1 call admitted in half-open state, got %d", got)
		}
	})

	t.Run("OnStateChange calling State() does not deadlock", func(t *testing.T) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			var cb *circuitbreaker.CircuitBreaker
			cb = circuitbreaker.New(circuitbreaker.Config{
				Name:        "gk13-callback",
				MaxFailures: 1,
				OnStateChange: func(_ string, _, _ circuitbreaker.State) {
					_ = cb.State()
				},
			})
			_ = cb.Execute(func() error { return errors.New("trip") })
		}()

		select {
		case <-done:
		case <-time.After(300 * time.Millisecond):
			t.Fatal("GK-13: OnStateChange deadlocked calling cb.State()")
		}
	})

	t.Run("configured MaxHalfOpenRequests=3 caps concurrent probes and recovers", func(t *testing.T) {
		cb := circuitbreaker.New(circuitbreaker.Config{
			Name:                "gk13-n3",
			MaxFailures:         1,
			ResetTimeout:        10 * time.Millisecond,
			MaxHalfOpenRequests: 3,
		})
		_ = cb.Execute(func() error { return errors.New("trip") })
		time.Sleep(20 * time.Millisecond)

		var entered atomic.Int32
		release := make(chan struct{})
		const total = 100
		var wg sync.WaitGroup
		wg.Add(total)
		start := make(chan struct{})
		for i := 0; i < total; i++ {
			go func() {
				defer wg.Done()
				<-start
				_ = cb.Execute(func() error {
					entered.Add(1)
					<-release
					return nil
				})
			}()
		}
		close(start)
		time.Sleep(30 * time.Millisecond)
		close(release)
		wg.Wait()

		if got := entered.Load(); got != 3 {
			t.Fatalf("GK-13: expected exactly 3 probes admitted in half-open, got %d", got)
		}
		if cb.State() != circuitbreaker.StateClosed {
			t.Fatalf("GK-13: expected StateClosed after 3 successful probes, got %v", cb.State())
		}
	})

	t.Run("rolling FailureRatio trips without consecutive failures and ExecuteCtx cancels cleanly", func(t *testing.T) {
		cb := circuitbreaker.New(circuitbreaker.Config{
			Name:             "gk13-ratio",
			MaxFailures:      100,
			FailureRatio:     0.5,
			MinRequests:      4,
			ExecutionTimeout: 20 * time.Millisecond,
		})

		// Alternate failure/success (50% failure ratio over 4 samples -> trips!).
		_ = cb.ExecuteCtx(context.Background(), func(ctx context.Context) error { return errors.New("fail-1") })
		_ = cb.ExecuteCtx(context.Background(), func(ctx context.Context) error { return nil })
		_ = cb.ExecuteCtx(context.Background(), func(ctx context.Context) error { return errors.New("fail-2") })
		_ = cb.ExecuteCtx(context.Background(), func(ctx context.Context) error { return nil })
		// Wait: on 4th call (nil) failures=2 out of 4 = 0.5; check on failure or next failure:
		_ = cb.ExecuteCtx(context.Background(), func(ctx context.Context) error { return errors.New("fail-3") })
		if cb.State() != circuitbreaker.StateOpen {
			t.Fatalf("GK-13: expected StateOpen once failure ratio >= 0.5, got %v", cb.State())
		}

		// ExecuteCtx cancels context on ExecutionTimeout without goroutine leak.
		cbTimeout := circuitbreaker.New(circuitbreaker.Config{
			Name:             "gk13-ctx-timeout",
			MaxFailures:      5,
			ExecutionTimeout: 15 * time.Millisecond,
		})
		ctxCanceled := make(chan struct{})
		err := cbTimeout.ExecuteCtx(context.Background(), func(ctx context.Context) error {
			<-ctx.Done()
			close(ctxCanceled)
			return ctx.Err()
		})
		if !errors.Is(err, circuitbreaker.ErrExecutionTimeout) {
			t.Fatalf("GK-13: expected ErrExecutionTimeout, got %v", err)
		}
		select {
		case <-ctxCanceled:
		default:
			t.Fatal("GK-13: ExecuteCtx did not cancel fn context on timeout")
		}
	})
}
