package middleware

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"time"
)

type timeoutResponseWriter struct {
	mu          sync.Mutex
	w           http.ResponseWriter
	h           http.Header
	wbuf        bytes.Buffer
	code        int
	wroteHeader bool
	timedOut    bool
}

func (tw *timeoutResponseWriter) Header() http.Header {
	return tw.h
}

func (tw *timeoutResponseWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut || tw.wroteHeader {
		return
	}
	tw.code = code
	tw.wroteHeader = true
}

func (tw *timeoutResponseWriter) Write(p []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	if !tw.wroteHeader {
		tw.code = http.StatusOK
		tw.wroteHeader = true
	}
	return tw.wbuf.Write(p)
}

// Timeout returns a middleware that cancels the request context after d.
// Handler writes are buffered under a mutex (mirroring net/http.TimeoutHandler)
// so late writes after deadline are dropped safely and panics are re-panicked
// on the serving goroutine for upstream Recovery middleware to handle.
// If the request exceeds the timeout, it writes a 503 Service Unavailable response.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			done := make(chan struct{})
			panicChan := make(chan any, 1)

			tw := &timeoutResponseWriter{
				w: w,
				h: make(http.Header),
			}

			go func() {
				defer func() {
					if p := recover(); p != nil {
						panicChan <- p
					}
				}()
				next.ServeHTTP(tw, r.WithContext(ctx))
				close(done)
			}()

			select {
			case p := <-panicChan:
				panic(p)
			case <-done:
				tw.mu.Lock()
				dst := w.Header()
				for k, vv := range tw.h {
					dst[k] = vv
				}
				if tw.wroteHeader && (tw.code != http.StatusOK || tw.wbuf.Len() == 0) {
					w.WriteHeader(tw.code)
				}
				if tw.wbuf.Len() > 0 {
					_, _ = w.Write(tw.wbuf.Bytes())
				}
				tw.mu.Unlock()
				return
			case <-ctx.Done():
				tw.mu.Lock()
				tw.timedOut = true
				tw.mu.Unlock()
				writeJSONError(w, http.StatusServiceUnavailable, `{"code":"TIMEOUT","message":"request timeout exceeded"}`)
				return
			}
		})
	}
}
