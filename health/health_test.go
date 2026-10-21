package health_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/health"
)

type okChecker struct{}

func (okChecker) HealthCheck(_ context.Context) error { return nil }

type failChecker struct{ msg string }

func (f failChecker) HealthCheck(_ context.Context) error { return fmt.Errorf("%s", f.msg) }

func TestLiveHandler(t *testing.T) {
	h := health.NewHandler()
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/health/live", nil)
	h.LiveHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", body["status"])
	}
}

func TestReadyHandlerAllOK(t *testing.T) {
	h := health.NewHandler()
	h.Register("db", okChecker{})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/health/ready", nil)
	h.ReadyHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ready" {
		t.Errorf("expected 'ready', got %q", body["status"])
	}
}

func TestReadyHandlerWithFailure(t *testing.T) {
	h := health.NewHandler()
	h.Register("db", okChecker{})
	h.Register("redis", failChecker{msg: "connection refused"})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/health/ready", nil)
	h.ReadyHandler().ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestReadyHandlerNoCheckers(t *testing.T) {
	h := health.NewHandler()
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/health/ready", nil)
	h.ReadyHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestNewHandlerWithTimeouts(t *testing.T) {
	h := health.NewHandlerWithTimeouts(2*time.Second, 5*time.Second)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/health/live", nil)
	h.LiveHandler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
