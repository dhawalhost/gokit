package observability

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	mw "github.com/dhawalhost/gokit/middleware"
)

// routePattern extracts the matched route template (e.g. "/users/{id}")
// from Chi context to avoid high-cardinality Prometheus time series.
func routePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx != nil && rctx.RoutePattern() != "" {
		return rctx.RoutePattern()
	}
	return r.URL.Path
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
			rec := &mw.StatusRecorder{ResponseWriter: w, Status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r)
			elapsed := time.Since(start).Seconds()
			status := strconv.Itoa(rec.Status)
			path := routePattern(r)
			if HTTPRequestDuration != nil {
				HTTPRequestDuration.WithLabelValues(r.Method, path, status).Observe(elapsed)
			}
			if HTTPRequestsTotal != nil {
				HTTPRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
			}
		})
	}
}

// Tracing returns a middleware that creates a span for each request.
func Tracing(serviceName string) func(http.Handler) http.Handler {
	tracer := otel.Tracer(serviceName)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := routePattern(r)
			ctx, span := tracer.Start(r.Context(), fmt.Sprintf("%s %s", r.Method, path))
			defer span.End()
			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.path", path),
			)
			rec := &mw.StatusRecorder{ResponseWriter: w, Status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))
			span.SetAttributes(attribute.Int("http.status_code", rec.Status))
			if rec.Status >= 500 {
				span.SetStatus(codes.Error, http.StatusText(rec.Status))
			}
		})
	}
}
