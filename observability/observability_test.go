package observability_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dhawalhost/gokit/config"
	"github.com/dhawalhost/gokit/observability"
	"github.com/prometheus/client_golang/prometheus"
)

var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestInitMetricsRegisters(t *testing.T) {
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
}

func TestMetricsHandlerResponds(t *testing.T) {
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
}

func TestMetricsMiddleware(t *testing.T) {
	observability.InitMetrics("testmw")
	mw := observability.Metrics()
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/test", nil)
	mw(noopHandler).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestTracingMiddleware(t *testing.T) {
	mw := observability.Tracing("test-service")
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/trace", nil)
	mw(noopHandler).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestInitTracerDisabled(t *testing.T) {
	shutdown, err := observability.InitTracer(context.Background(), config.TelemetryConfig{Enabled: false})
	if err != nil {
		t.Fatalf("InitTracer disabled: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestTracingMiddlewareWith5xx(t *testing.T) {
	mw := observability.Tracing("test-service")
	errHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/trace", nil)
	mw(errHandler).ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}
