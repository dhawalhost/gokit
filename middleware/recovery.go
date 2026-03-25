package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"

	"go.uber.org/zap"
)

// Recovery returns a middleware that catches panics, logs the stack trace, and
// returns a JSON 500 response.
func Recovery(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rvr := recover(); rvr != nil {
					log.Error("panic recovered",
						zap.String("panic", fmt.Sprintf("%v", rvr)),
						zap.String("stack", string(debug.Stack())),
						zap.String("request_id", RequestIDFromContext(r.Context())),
					)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"code":    "INTERNAL_ERROR",
						"message": "an unexpected error occurred",
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
