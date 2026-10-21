package middleware

import (
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Logger returns a middleware that logs each request using the provided zap.Logger.
// Requests to paths beginning with /health are skipped.
func Logger(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip health paths.
			if strings.HasPrefix(r.URL.Path, "/health") {
				next.ServeHTTP(w, r)
				return
			}

			rec := &StatusRecorder{ResponseWriter: w, Status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r)
			log.Info("request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rec.Status),
				zap.Duration("latency", time.Since(start)),
				zap.String("request_id", RequestIDFromContext(r.Context())),
				zap.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}
