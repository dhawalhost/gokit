// Package health provides HTTP liveness and readiness check handlers.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Checker is implemented by components that can report their health.
type Checker interface {
	HealthCheck(ctx context.Context) error
}

// Handler aggregates named health checkers and exposes HTTP handlers.
type Handler struct {
	checkers          map[string]Checker
	perCheckerTimeout time.Duration
	totalTimeout      time.Duration
}

// NewHandler creates a new Handler with no registered checkers.
// Default timeouts: 5s per checker, 10s total.
func NewHandler() *Handler {
	return &Handler{
		checkers:          make(map[string]Checker),
		perCheckerTimeout: 5 * time.Second,
		totalTimeout:      10 * time.Second,
	}
}

// NewHandlerWithTimeouts creates a new Handler with custom timeouts.
func NewHandlerWithTimeouts(perChecker, total time.Duration) *Handler {
	return &Handler{
		checkers:          make(map[string]Checker),
		perCheckerTimeout: perChecker,
		totalTimeout:      total,
	}
}

// Register adds a named checker to the handler.
func (h *Handler) Register(name string, checker Checker) {
	h.checkers[name] = checker
}

// LiveHandler returns an HTTP handler that always returns 200 OK (liveness probe).
func (h *Handler) LiveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

// ReadyHandler returns an HTTP handler that checks all registered checkers.
// It returns 200 if all pass, or 503 with a map of failures.
// Each checker is given perCheckerTimeout, and the total operation is limited by totalTimeout.
func (h *Handler) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), h.totalTimeout)
		defer cancel()

		type result struct {
			name string
			err  error
		}

		resultCh := make(chan result, len(h.checkers))
		var wg sync.WaitGroup

		for name, c := range h.checkers {
			wg.Add(1)
			go func(name string, checker Checker) {
				defer wg.Done()

				checkerCtx, checkerCancel := context.WithTimeout(ctx, h.perCheckerTimeout)
				defer checkerCancel()

				err := checker.HealthCheck(checkerCtx)
				resultCh <- result{name: name, err: err}
			}(name, c)
		}

		// Wait for all checks to complete
		go func() {
			wg.Wait()
			close(resultCh)
		}()

		failures := map[string]string{}
		for res := range resultCh {
			if res.err != nil {
				failures[res.name] = res.err.Error()
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if len(failures) > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":   "not ready",
				"failures": failures,
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	}
}
