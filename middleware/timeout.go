package middleware

import (
	"context"
	"net/http"
	"time"
)

// Timeout returns a middleware that cancels the request context after d.
// If the request exceeds the timeout, it writes a 504 Gateway Timeout response.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			// Create a channel to signal completion
			done := make(chan struct{})

			// Wrap response writer to track if headers were sent
			rw := &timeoutResponseWriter{
				ResponseWriter: w,
				done:           done,
			}

			go func() {
				next.ServeHTTP(rw, r.WithContext(ctx))
				close(done)
			}()

			select {
			case <-done:
				// Request completed normally
				return
			case <-ctx.Done():
				// Timeout occurred — write 504 if headers not yet sent.
				if !rw.written {
					writeJSONError(w, http.StatusGatewayTimeout, `{"code":"TIMEOUT","message":"request timeout exceeded"}`)
				}
				return
			}
		})
	}
}
