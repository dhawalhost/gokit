package observability_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dhawalhost/gokit/observability"
	"github.com/prometheus/client_golang/prometheus"
)

var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestInitMetricsRegisters(t *testing.T) {
	// Use a fresh registry to avoid duplicate metric registration conflicts.
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
	// InitMetrics uses the default Prometheus registry. Call with a unique name
	// so that parallel test runs don't collide.
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
