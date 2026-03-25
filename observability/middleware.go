package observability

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type metricsRecorder struct {
	http.ResponseWriter
	status int
}

func (m *metricsRecorder) WriteHeader(status int) {
	m.status = status
	m.ResponseWriter.WriteHeader(status)
}

// Metrics returns a middleware that records Prometheus metrics for each request.
// InitMetrics must be called before this middleware is used.
func Metrics() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if HTTPRequestsInFlight != nil {
				HTTPRequestsInFlight.Inc()
				defer HTTPRequestsInFlight.Dec()
			}
			rec := &metricsRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r)
			elapsed := time.Since(start).Seconds()
			status := strconv.Itoa(rec.status)
			if HTTPRequestDuration != nil {
				HTTPRequestDuration.WithLabelValues(r.Method, r.URL.Path, status).Observe(elapsed)
			}
			if HTTPRequestsTotal != nil {
				HTTPRequestsTotal.WithLabelValues(r.Method, r.URL.Path, status).Inc()
			}
		})
	}
}

// Tracing returns a middleware that creates a span for each request.
func Tracing(serviceName string) func(http.Handler) http.Handler {
	tracer := otel.Tracer(serviceName)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.Start(r.Context(), fmt.Sprintf("%s %s", r.Method, r.URL.Path))
			defer span.End()
			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.path", r.URL.Path),
			)
			rec := &metricsRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))
			span.SetAttributes(attribute.Int("http.status_code", rec.status))
			if rec.status >= 500 {
				span.SetStatus(codes.Error, http.StatusText(rec.status))
			}
		})
	}
}
