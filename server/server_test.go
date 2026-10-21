package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/server"
)

func TestNewDefaults(t *testing.T) {
	s := server.New()
	if s == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestNewWithOptions(t *testing.T) {
	tests := []struct {
		name string
		opts []server.Option
	}{
		{
			"all timeout options",
			[]server.Option{
				server.WithAddr(":9991"),
				server.WithReadTimeout(5 * time.Second),
				server.WithWriteTimeout(5 * time.Second),
				server.WithIdleTimeout(30 * time.Second),
				server.WithShutdownTimeout(5 * time.Second),
			},
		},
		{
			"TLS option",
			[]server.Option{
				server.WithTLS("cert.pem", "key.pem"),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := server.New(tc.opts...)
			if s == nil {
				t.Fatal("expected non-nil server")
			}
		})
	}
}

func TestMountAndServe(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		requestPath  string
		expectedCode int
		expectedBody string
	}{
		{"hit route", "/ping", "/ping", http.StatusOK, "pong"},
		{"miss route", "/ping", "/other", http.StatusNotFound, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := server.New()
			s.Mount(tc.path, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("pong"))
			}))
			req := httptest.NewRequest(http.MethodGet, tc.requestPath, nil)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			if w.Code != tc.expectedCode {
				t.Errorf("expected %d, got %d", tc.expectedCode, w.Code)
			}
			if tc.expectedBody != "" && w.Body.String() != tc.expectedBody {
				t.Errorf("expected body %q, got %q", tc.expectedBody, w.Body.String())
			}
		})
	}
}

func TestUseMiddleware(t *testing.T) {
	var callCount int
	countMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			next.ServeHTTP(w, r)
		})
	}

	s := server.New()
	s.Use(countMW, countMW)
	s.Mount("/test", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if callCount != 2 {
		t.Errorf("expected middleware called 2 times, got %d", callCount)
	}
}

func TestShutdownWithoutRun(t *testing.T) {
	s := server.New()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestShutdownAfterRun(t *testing.T) {
	s := server.New(
		server.WithAddr("127.0.0.1:19877"),
		server.WithShutdownTimeout(500*time.Millisecond),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(30 * time.Millisecond)

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after Shutdown")
	}
}

func TestRunCancelContext(t *testing.T) {
	s := server.New(
		server.WithAddr("127.0.0.1:19878"),
		server.WithShutdownTimeout(500*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error after ctx cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestRunInvalidAddrReturnsError(t *testing.T) {
	s := server.New(server.WithAddr("127.0.0.1:99999"))
	err := s.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestRunTLSMissingCert(t *testing.T) {
	s := server.New(
		server.WithAddr("127.0.0.1:19880"),
		server.WithTLS("/nonexistent/cert.pem", "/nonexistent/key.pem"),
	)
	err := s.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for missing TLS certificate files")
	}
}
