// Package observability provides Prometheus metrics initialisation and a metrics HTTP handler.
package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HTTPRequestDuration tracks the latency of HTTP requests.
var HTTPRequestDuration *prometheus.HistogramVec

// HTTPRequestsInFlight tracks the number of HTTP requests currently being served.
var HTTPRequestsInFlight prometheus.Gauge

// HTTPRequestsTotal counts the total number of HTTP requests.
var HTTPRequestsTotal *prometheus.CounterVec

// InitMetrics registers Prometheus metrics for the given service name.
// It must be called once before the metrics middleware or handler is used.
func InitMetrics(serviceName string) {
	HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: serviceName,
		Name:      "http_request_duration_seconds",
		Help:      "Histogram of HTTP request latencies.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "path", "status"})

	HTTPRequestsInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: serviceName,
		Name:      "http_requests_in_flight",
		Help:      "Number of HTTP requests currently being served.",
	})

	HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: serviceName,
		Name:      "http_requests_total",
		Help:      "Total number of HTTP requests.",
	}, []string{"method", "path", "status"})

	prometheus.MustRegister(HTTPRequestDuration, HTTPRequestsInFlight, HTTPRequestsTotal)
}

// MetricsHandler returns an HTTP handler that exposes Prometheus metrics.
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}
