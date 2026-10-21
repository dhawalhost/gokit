// Package server provides an HTTP server with graceful shutdown.
package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
)

// HTTPServer wraps net/http.Server with a chi router and graceful shutdown.
type HTTPServer struct {
	mux             *chi.Mux
	mu              sync.Mutex
	srv             *http.Server
	shutdownOnce    sync.Once
	shutdownErr     error
	addr            string
	readTimeout     time.Duration
	writeTimeout    time.Duration
	idleTimeout     time.Duration
	shutdownTimeout time.Duration
	certFile        string
	keyFile         string
}

// New creates a new HTTPServer with the given options.
func New(opts ...Option) *HTTPServer {
	s := &HTTPServer{
		mux:             chi.NewRouter(),
		addr:            ":8080",
		readTimeout:     30 * time.Second,
		writeTimeout:    30 * time.Second,
		idleTimeout:     120 * time.Second,
		shutdownTimeout: 30 * time.Second,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Handler returns the underlying http.Handler (the chi router).
// This is useful for testing without binding a real network port.
func (s *HTTPServer) Handler() http.Handler {
	return s.mux
}

// Mount mounts an http.Handler at the given pattern.
func (s *HTTPServer) Mount(pattern string, handler http.Handler) {
	s.mux.Mount(pattern, handler)
}

// Use appends middleware to the router's middleware stack.
func (s *HTTPServer) Use(middleware ...func(http.Handler) http.Handler) {
	s.mux.Use(middleware...)
}

// Run starts the HTTP server and blocks until ctx is cancelled or a signal
// (SIGTERM/SIGINT) is received, then gracefully shuts down.
func (s *HTTPServer) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.mux,
		ReadHeaderTimeout: s.readTimeout,
		ReadTimeout:       s.readTimeout,
		WriteTimeout:      s.writeTimeout,
		IdleTimeout:       s.idleTimeout,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	s.mu.Lock()
	s.srv = srv
	s.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		if s.certFile != "" && s.keyFile != "" {
			errCh <- srv.ListenAndServeTLS(s.certFile, s.keyFile)
		} else {
			errCh <- srv.ListenAndServe()
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server: listen: %w", err)
		}
	case <-quit:
	case <-ctx.Done():
	}

	return s.Shutdown(ctx)
}

// Shutdown gracefully shuts down the server with the configured timeout.
// If the incoming ctx is already cancelled, a fresh context with shutdownTimeout
// is used to ensure in-flight connections have time to complete.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	s.shutdownOnce.Do(func() {
		baseCtx := context.Background()
		if ctx != nil && ctx.Err() == nil {
			baseCtx = ctx
		}

		shutdownCtx, cancel := context.WithTimeout(baseCtx, s.shutdownTimeout)
		defer cancel()

		s.mu.Lock()
		srv := s.srv
		s.mu.Unlock()

		if srv == nil {
			return
		}

		if err := srv.Shutdown(shutdownCtx); err != nil {
			s.shutdownErr = fmt.Errorf("server: shutdown: %w", err)
		}
	})

	return s.shutdownErr
}
