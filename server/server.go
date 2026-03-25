// Package server provides an HTTP server with graceful shutdown.
package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
)

// HTTPServer wraps net/http.Server with a chi router and graceful shutdown.
type HTTPServer struct {
	mux             *chi.Mux
	srv             *http.Server
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
	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           s.mux,
		ReadHeaderTimeout: s.readTimeout,
		ReadTimeout:       s.readTimeout,
		WriteTimeout:      s.writeTimeout,
		IdleTimeout:       s.idleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		if s.certFile != "" && s.keyFile != "" {
			errCh <- s.srv.ListenAndServeTLS(s.certFile, s.keyFile)
		} else {
			errCh <- s.srv.ListenAndServe()
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
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, s.shutdownTimeout)
	defer cancel()

	if s.srv == nil {
		return nil
	}

	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server: shutdown: %w", err)
	}
	return nil
}
