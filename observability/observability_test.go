package observability_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dhawalhost/gokit/config"
	"github.com/dhawalhost/gokit/observability"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
)

var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestObservability_Metrics_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "prometheus metrics registry allows histogram registration",
			run: func(t *testing.T) {
				reg := prometheus.NewRegistry()
				duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
					Namespace: "test",
					Name:      "http_request_duration_seconds",
					Help:      "Histogram of HTTP request latencies.",
					Buckets:   prometheus.DefBuckets,
				}, []string{"method", "path", "status"})

				if err := reg.Register(duration); err != nil {
					t.Fatalf("failed to register histogram: %v", err)
				}
			},
		},
		{
			name: "MetricsHandler responds with 200 OK",
			run: func(t *testing.T) {
				observability.InitMetrics("testobs")
				h := observability.MetricsHandler()
				if h == nil {
					t.Fatal("expected non-nil handler")
				}
				w := httptest.NewRecorder()
				r, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
				h.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Errorf("expected 200, got %d", w.Code)
				}
			},
		},
		{
			name: "Metrics middleware records request and passes through",
			run: func(t *testing.T) {
				observability.InitMetrics("testmw")
				mw := observability.Metrics()
				w := httptest.NewRecorder()
				r, _ := http.NewRequest(http.MethodGet, "/test", nil)
				mw(noopHandler).ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Errorf("expected 200, got %d", w.Code)
				}
			},
		},
		{
			name: "Metrics middleware extracts Chi RoutePattern",
			run: func(t *testing.T) {
				observability.InitMetrics("testchi")
				r := chi.NewRouter()
				r.Use(observability.Metrics())
				r.Get("/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				})

				w := httptest.NewRecorder()
				req, _ := http.NewRequest(http.MethodGet, "/users/123", nil)
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Errorf("expected 200, got %d", w.Code)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestObservability_Tracing_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.Handler
		wantStatus int
	}{
		{
			name:       "tracing middleware wraps successful handler",
			handler:    noopHandler,
			wantStatus: http.StatusOK,
		},
		{
			name: "tracing middleware records 5xx status code",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mw := observability.Tracing("test-service")
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, "/trace", nil)
			mw(tc.handler).ServeHTTP(w, r)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
		})
	}

	// Test InitTracer disabled
	shutdown, err := observability.InitTracer(context.Background(), config.TelemetryConfig{Enabled: false})
	if err != nil {
		t.Fatalf("InitTracer disabled: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	// Test InitTracer enabled
	shutdownEnabled, err := observability.InitTracer(context.Background(), config.TelemetryConfig{
		Enabled:      true,
		ServiceName:  "test-obs-svc",
		OTLPEndpoint: "localhost:4318",
	})
	if err != nil {
		t.Fatalf("InitTracer enabled: %v", err)
	}
	if err := shutdownEnabled(context.Background()); err != nil {
		t.Fatalf("shutdownEnabled: %v", err)
	}
}
