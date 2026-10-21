package client_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/circuitbreaker"
	"github.com/dhawalhost/gokit/client"
	"github.com/dhawalhost/gokit/middleware"
)

func TestClient_Methods_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "GET request returns response body and status 200",
			run: func(t *testing.T) {
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Errorf("expected GET, got %s", r.Method)
					}
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("hello world"))
				}))
				defer ts.Close()

				c := client.New(client.WithLogger(zap.NewNop()))
				resp, err := c.Get(context.Background(), ts.URL)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected status 200, got %d", resp.StatusCode)
				}
				body, _ := io.ReadAll(resp.Body)
				if string(body) != "hello world" {
					t.Errorf("expected 'hello world', got %q", string(body))
				}
			},
		},
		{
			name: "POST JSON serializes payload and decodes response",
			run: func(t *testing.T) {
				type userReq struct {
					Name string `json:"name"`
				}
				type userResp struct {
					ID   int    `json:"id"`
					Name string `json:"name"`
				}

				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						t.Errorf("expected POST, got %s", r.Method)
					}
					if r.Header.Get("Content-Type") != "application/json" {
						t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
					}

					var req userReq
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Errorf("failed to decode request body: %v", err)
					}

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(userResp{ID: 42, Name: req.Name})
				}))
				defer ts.Close()

				c := client.New(
					client.WithBaseURL(ts.URL),
					client.WithLogger(zap.NewNop()),
				)

				var result userResp
				resp, err := c.PostJSON(context.Background(), "/users", userReq{Name: "Alice"}, &result)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusCreated {
					t.Errorf("expected status 201, got %d", resp.StatusCode)
				}
				if result.ID != 42 || result.Name != "Alice" {
					t.Errorf("unexpected response: %+v", result)
				}
			},
		},
		{
			name: "PUT and DELETE methods execute and return expected status codes",
			run: func(t *testing.T) {
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.Method {
					case http.MethodPut:
						w.WriteHeader(http.StatusOK)
					case http.MethodDelete:
						w.WriteHeader(http.StatusNoContent)
					default:
						t.Errorf("unexpected method: %s", r.Method)
					}
				}))
				defer ts.Close()

				c := client.New(client.WithLogger(zap.NewNop()))

				putResp, err := c.Put(context.Background(), ts.URL, "text/plain", nil)
				if err != nil {
					t.Fatalf("PUT failed: %v", err)
				}
				putResp.Body.Close()
				if putResp.StatusCode != http.StatusOK {
					t.Errorf("PUT expected 200, got %d", putResp.StatusCode)
				}

				delResp, err := c.Delete(context.Background(), ts.URL)
				if err != nil {
					t.Fatalf("DELETE failed: %v", err)
				}
				delResp.Body.Close()
				if delResp.StatusCode != http.StatusNoContent {
					t.Errorf("DELETE expected 204, got %d", delResp.StatusCode)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestClient_Retry_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "retry succeeds after transient 503 failures",
			run: func(t *testing.T) {
				var attempts int32
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					count := atomic.AddInt32(&attempts, 1)
					if count < 3 {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = w.Write([]byte("temporarily down"))
						return
					}
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("ok now"))
				}))
				defer ts.Close()

				c := client.New(
					client.WithMaxRetries(3),
					client.WithRetryPolicy(client.RetryPolicy{
						MaxRetries:           3,
						InitialBackoff:       10 * time.Millisecond,
						MaxBackoff:           50 * time.Millisecond,
						BackoffFactor:        2.0,
						Jitter:               false,
						RetryableStatusCodes: []int{http.StatusServiceUnavailable},
					}),
					client.WithLogger(zap.NewNop()),
				)

				resp, err := c.Get(context.Background(), ts.URL)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected 200, got %d", resp.StatusCode)
				}
				if atomic.LoadInt32(&attempts) != 3 {
					t.Errorf("expected 3 attempts, got %d", atomic.LoadInt32(&attempts))
				}
			},
		},
		{
			name: "body replay resends identical request payload on retries",
			run: func(t *testing.T) {
				var attempts int32
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					if string(body) != `{"data":"payload"}` {
						t.Errorf("attempt %d received corrupted body: %s", atomic.LoadInt32(&attempts), string(body))
					}

					count := atomic.AddInt32(&attempts, 1)
					if count < 2 {
						w.WriteHeader(http.StatusGatewayTimeout)
						return
					}
					w.WriteHeader(http.StatusOK)
				}))
				defer ts.Close()

				c := client.New(
					client.WithRetryPolicy(client.RetryPolicy{
						MaxRetries:           2,
						InitialBackoff:       10 * time.Millisecond,
						RetryableStatusCodes: []int{http.StatusGatewayTimeout},
					}),
					client.WithLogger(zap.NewNop()),
				)

				type testPayload struct {
					Data string `json:"data"`
				}

				resp, err := c.PostJSON(context.Background(), ts.URL, testPayload{Data: "payload"}, nil)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected 200, got %d", resp.StatusCode)
				}
				if atomic.LoadInt32(&attempts) != 2 {
					t.Errorf("expected 2 attempts, got %d", atomic.LoadInt32(&attempts))
				}
			},
		},
		{
			name: "non-retryable 400 Bad Request fails immediately without retry",
			run: func(t *testing.T) {
				var attempts int32
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					atomic.AddInt32(&attempts, 1)
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte("invalid input"))
				}))
				defer ts.Close()

				c := client.New(
					client.WithMaxRetries(3),
					client.WithLogger(zap.NewNop()),
				)

				resp, err := c.Get(context.Background(), ts.URL)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusBadRequest {
					t.Errorf("expected 400, got %d", resp.StatusCode)
				}
				if atomic.LoadInt32(&attempts) != 1 {
					t.Errorf("expected 1 attempt, got %d", atomic.LoadInt32(&attempts))
				}
			},
		},
		{
			name: "honors Retry-After header duration before retrying",
			run: func(t *testing.T) {
				var attempts int32
				start := time.Now()

				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					count := atomic.AddInt32(&attempts, 1)
					if count == 1 {
						w.Header().Set("Retry-After", "1")
						w.WriteHeader(http.StatusTooManyRequests)
						return
					}
					w.WriteHeader(http.StatusOK)
				}))
				defer ts.Close()

				c := client.New(
					client.WithRetryPolicy(client.RetryPolicy{
						MaxRetries:           2,
						InitialBackoff:       10 * time.Millisecond,
						MaxBackoff:           2 * time.Second,
						RetryableStatusCodes: []int{http.StatusTooManyRequests},
					}),
					client.WithLogger(zap.NewNop()),
				)

				resp, err := c.Get(context.Background(), ts.URL)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer resp.Body.Close()

				elapsed := time.Since(start)
				if elapsed < 900*time.Millisecond {
					t.Errorf("expected Retry-After wait of ~1s, elapsed %v", elapsed)
				}
				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected 200, got %d", resp.StatusCode)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestClient_ResilienceAndContext_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "repeated failures trip circuit breaker and fail fast",
			run: func(t *testing.T) {
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				}))
				defer ts.Close()

				cb := circuitbreaker.New(circuitbreaker.Config{
					Name:         "test-service",
					MaxFailures:  2,
					ResetTimeout: 1 * time.Second,
				})

				c := client.New(
					client.WithCircuitBreaker(cb),
					client.WithMaxRetries(0),
					client.WithLogger(zap.NewNop()),
				)

				// Attempt 1: fails, failures=1
				resp1, _ := c.Get(context.Background(), ts.URL)
				if resp1 != nil {
					resp1.Body.Close()
				}

				// Attempt 2: fails, failures=2 -> opens circuit breaker
				resp2, _ := c.Get(context.Background(), ts.URL)
				if resp2 != nil {
					resp2.Body.Close()
				}

				if cb.State() != circuitbreaker.StateOpen {
					t.Fatalf("expected circuit breaker to be open, got %v", cb.State())
				}

				// Attempt 3: should fail fast with circuitbreaker error
				_, err := c.Get(context.Background(), ts.URL)
				if err == nil {
					t.Fatal("expected circuit breaker error, got nil")
				}
			},
		},
		{
			name: "context deadline cancellation aborts in-flight request",
			run: func(t *testing.T) {
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					time.Sleep(200 * time.Millisecond)
					w.WriteHeader(http.StatusOK)
				}))
				defer ts.Close()

				c := client.New(client.WithLogger(zap.NewNop()))

				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()

				_, err := c.Get(ctx, ts.URL)
				if err == nil {
					t.Fatal("expected context deadline error, got nil")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestClient_HeaderPropagation_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "request ID propagated from context via X-Request-ID",
			run: func(t *testing.T) {
				var receivedRequestID string
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					receivedRequestID = r.Header.Get("X-Request-ID")
					w.WriteHeader(http.StatusOK)
				}))
				defer ts.Close()

				c := client.New(
					client.WithRequestIDPropagation(true),
					client.WithLogger(zap.NewNop()),
				)

				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("X-Request-ID", "req-xyz-12345")

				var innerCtx context.Context
				handler := middleware.RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					innerCtx = r.Context()
				}))
				handler.ServeHTTP(httptest.NewRecorder(), req)

				resp, err := c.Get(innerCtx, ts.URL)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer resp.Body.Close()

				if receivedRequestID != "req-xyz-12345" {
					t.Errorf("expected X-Request-ID 'req-xyz-12345', got %q", receivedRequestID)
				}
			},
		},
		{
			name: "traceparent propagated from OpenTelemetry trace context",
			run: func(t *testing.T) {
				var receivedTraceParent string
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					receivedTraceParent = r.Header.Get("traceparent")
					w.WriteHeader(http.StatusOK)
				}))
				defer ts.Close()

				tp := sdktrace.NewTracerProvider()
				otel.SetTracerProvider(tp)
				otel.SetTextMapPropagator(propagation.TraceContext{})

				tracer := tp.Tracer("test")
				ctx, span := tracer.Start(context.Background(), "test-span")
				defer span.End()

				c := client.New(
					client.WithTracePropagation(true),
					client.WithLogger(zap.NewNop()),
				)

				resp, err := c.Get(ctx, ts.URL)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer resp.Body.Close()

				if receivedTraceParent == "" {
					t.Error("expected traceparent header to be propagated, got empty")
				}
			},
		},
		{
			name: "default headers injected into outbound request",
			run: func(t *testing.T) {
				var receivedHeader string
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					receivedHeader = r.Header.Get("X-App-Client")
					w.WriteHeader(http.StatusOK)
				}))
				defer ts.Close()

				c := client.New(
					client.WithDefaultHeader("X-App-Client", "gokit-outbound/1.0"),
					client.WithLogger(zap.NewNop()),
				)

				resp, err := c.Get(context.Background(), ts.URL)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer resp.Body.Close()

				if receivedHeader != "gokit-outbound/1.0" {
					t.Errorf("expected header 'gokit-outbound/1.0', got %q", receivedHeader)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
