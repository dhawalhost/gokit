package circuitbreaker_test

import (
	"errors"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/circuitbreaker"
)

func TestNewDefaults(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{Name: "test"})
	if cb.State() != circuitbreaker.StateClosed {
		t.Fatalf("expected StateClosed, got %v", cb.State())
	}
}

func TestSuccessKeepsClosed(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{Name: "test", MaxFailures: 3})
	for i := 0; i < 5; i++ {
		if err := cb.Execute(func() error { return nil }); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if cb.State() != circuitbreaker.StateClosed {
		t.Fatalf("expected StateClosed, got %v", cb.State())
	}
}

func TestOpensAfterMaxFailures(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{Name: "test", MaxFailures: 3})
	fail := errors.New("failure")

	for i := 0; i < 3; i++ {
		_ = cb.Execute(func() error { return fail })
	}
	if cb.State() != circuitbreaker.StateOpen {
		t.Fatalf("expected StateOpen after %d failures, got %v", 3, cb.State())
	}
}

func TestOpenRejectsRequests(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{Name: "test", MaxFailures: 1})
	_ = cb.Execute(func() error { return errors.New("fail") })

	err := cb.Execute(func() error { return nil })
	if !errors.Is(err, circuitbreaker.ErrOpen) {
		t.Fatalf("expected ErrOpen, got %v", err)
	}
}

func TestHalfOpenAfterResetTimeout(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:         "test",
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
}

func TestHalfOpenSuccessCloses(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:         "test",
		MaxFailures:  1,
		ResetTimeout: 10 * time.Millisecond,
	})
	_ = cb.Execute(func() error { return errors.New("fail") })
	time.Sleep(20 * time.Millisecond)

	if err := cb.Execute(func() error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cb.State() != circuitbreaker.StateClosed {
		t.Fatalf("expected StateClosed after successful half-open, got %v", cb.State())
	}
}

func TestHalfOpenFailureReopens(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:         "test",
		MaxFailures:  1,
		ResetTimeout: 10 * time.Millisecond,
	})
	_ = cb.Execute(func() error { return errors.New("fail") })
	time.Sleep(20 * time.Millisecond)

	_ = cb.Execute(func() error { return errors.New("fail again") })
	if cb.State() != circuitbreaker.StateOpen {
		t.Fatalf("expected StateOpen after half-open failure, got %v", cb.State())
	}
}

func TestOnStateChangeCallback(t *testing.T) {
	var transitions []string
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:        "cb",
		MaxFailures: 1,
		OnStateChange: func(name string, from, to circuitbreaker.State) {
			transitions = append(transitions, name)
		},
	})
	_ = cb.Execute(func() error { return errors.New("fail") })
	if len(transitions) == 0 {
		t.Fatal("expected OnStateChange to be called")
	}
}
