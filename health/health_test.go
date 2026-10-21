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

func TestHealth_LiveAndReady_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		setup      func() (*health.Handler, string)
		wantStatus int
		verify     func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name: "live endpoint returns 200 OK with status ok",
			setup: func() (*health.Handler, string) {
				return health.NewHandler(), "/health/live"
			},
			wantStatus: http.StatusOK,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				var body map[string]string
				if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if body["status"] != "ok" {
					t.Errorf("expected status 'ok', got %q", body["status"])
				}
			},
		},
		{
			name: "ready endpoint with all passing checkers returns 200 OK and status ready",
			setup: func() (*health.Handler, string) {
				h := health.NewHandler()
				h.Register("db", okChecker{})
				return h, "/health/ready"
			},
			wantStatus: http.StatusOK,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				var body map[string]string
				if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if body["status"] != "ready" {
					t.Errorf("expected 'ready', got %q", body["status"])
				}
			},
		},
		{
			name: "ready endpoint with failing checker returns 503 Service Unavailable",
			setup: func() (*health.Handler, string) {
				h := health.NewHandler()
				h.Register("db", okChecker{})
				h.Register("redis", failChecker{msg: "connection refused"})
				return h, "/health/ready"
			},
			wantStatus: http.StatusServiceUnavailable,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				var body map[string]interface{}
				_ = json.NewDecoder(w.Body).Decode(&body)
				if body["status"] != "not ready" {
					t.Errorf("expected status 'not ready', got %v", body["status"])
				}
			},
		},
		{
			name: "ready endpoint without registered checkers returns 200 OK",
			setup: func() (*health.Handler, string) {
				return health.NewHandler(), "/health/ready"
			},
			wantStatus: http.StatusOK,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				var body map[string]string
				_ = json.NewDecoder(w.Body).Decode(&body)
				if body["status"] != "ready" {
					t.Errorf("expected status ready, got %v", body["status"])
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, path := tc.setup()
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, path, nil)

			if path == "/health/live" {
				h.LiveHandler().ServeHTTP(w, r)
			} else {
				h.ReadyHandler().ServeHTTP(w, r)
			}

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
			if tc.verify != nil {
				tc.verify(t, w)
			}
		})
	}
}

func TestHealth_HandlerTimeouts_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		liveTimeout  time.Duration
		readyTimeout time.Duration
	}{
		{
			name:         "custom timeouts configured on new handler",
			liveTimeout:  2 * time.Second,
			readyTimeout: 5 * time.Second,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := health.NewHandlerWithTimeouts(tc.liveTimeout, tc.readyTimeout)
			if h == nil {
				t.Fatal("expected non-nil handler")
			}
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, "/health/live", nil)
			h.LiveHandler().ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", w.Code)
			}
		})
	}
}
