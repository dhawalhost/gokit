// Package health provides HTTP liveness and readiness check handlers.
package health

import (
	"context"
	"encoding/json"
	"net/http"
)

// Checker is implemented by components that can report their health.
type Checker interface {
	HealthCheck(ctx context.Context) error
}

// Handler aggregates named health checkers and exposes HTTP handlers.
type Handler struct {
	checkers map[string]Checker
}

// NewHandler creates a new Handler with no registered checkers.
func NewHandler() *Handler {
	return &Handler{checkers: make(map[string]Checker)}
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
func (h *Handler) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		failures := map[string]string{}
		for name, c := range h.checkers {
			if err := c.HealthCheck(r.Context()); err != nil {
				failures[name] = err.Error()
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
