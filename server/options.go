package server

import "time"

// Option configures an HTTPServer.
type Option func(*HTTPServer)

// WithAddr sets the listen address (e.g. ":8080").
func WithAddr(addr string) Option {
	return func(s *HTTPServer) { s.addr = addr }
}

// WithReadTimeout sets the HTTP server read timeout.
func WithReadTimeout(d time.Duration) Option {
	return func(s *HTTPServer) { s.readTimeout = d }
}

// WithWriteTimeout sets the HTTP server write timeout.
func WithWriteTimeout(d time.Duration) Option {
	return func(s *HTTPServer) { s.writeTimeout = d }
}

// WithIdleTimeout sets the HTTP server idle timeout.
func WithIdleTimeout(d time.Duration) Option {
	return func(s *HTTPServer) { s.idleTimeout = d }
}

// WithShutdownTimeout sets the graceful shutdown timeout.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *HTTPServer) { s.shutdownTimeout = d }
}

// WithTLS configures TLS using the given certificate and key files.
func WithTLS(certFile, keyFile string) Option {
	return func(s *HTTPServer) {
		s.certFile = certFile
		s.keyFile = keyFile
	}
}
