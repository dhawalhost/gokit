// Package observability provides Prometheus metrics initialisation and a metrics HTTP handler.
package observability

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HTTPRequestDuration tracks the latency of HTTP requests.
var HTTPRequestDuration *prometheus.HistogramVec

// HTTPRequestsInFlight tracks the number of HTTP requests currently being served.
var HTTPRequestsInFlight prometheus.Gauge

// HTTPRequestsTotal counts the total number of HTTP requests.
var HTTPRequestsTotal *prometheus.CounterVec

var (
	defaultRegistryLock sync.Mutex
	defaultRegistry     *Registry
)

// DefaultRegistry returns the singleton Registry initialized by InitMetrics.
func DefaultRegistry() *Registry {
	defaultRegistryLock.Lock()
	defer defaultRegistryLock.Unlock()
	return defaultRegistry
}

// InitMetrics registers Prometheus metrics for the given service name.
// It is safe to call across unit tests and initializes both the package-level variables
// and the isolated DefaultRegistry().
func InitMetrics(serviceName string) {
	defaultRegistryLock.Lock()
	defer defaultRegistryLock.Unlock()

	r := NewRegistry(serviceName)
	defaultRegistry = r
	HTTPRequestDuration = r.httpDuration
	HTTPRequestsInFlight = r.httpInFlight
	HTTPRequestsTotal = r.httpRequestsTotal

	// Register with global Prometheus registry safely (ignoring duplicate registration in tests)
	_ = prometheus.Register(HTTPRequestDuration)
	_ = prometheus.Register(HTTPRequestsInFlight)
	_ = prometheus.Register(HTTPRequestsTotal)
}

// MetricsHandler returns an HTTP handler that exposes Prometheus metrics.
func MetricsHandler() http.Handler {
	defaultRegistryLock.Lock()
	r := defaultRegistry
	defaultRegistryLock.Unlock()

	if r != nil {
		return r.HTTPHandler()
	}
	return promhttp.Handler()
}
