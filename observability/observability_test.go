package observability_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/config"
	"github.com/dhawalhost/gokit/observability"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestObservability_Metrics_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "prometheus metrics registry allows histogram registration",
			run: func(t *testing.T) {
				reg := prometheus.NewRegistry()
				duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
					Namespace: "test",
					Name:      "http_request_duration_seconds",
					Help:      "Histogram of HTTP request latencies.",
					Buckets:   prometheus.DefBuckets,
				}, []string{"method", "path", "status"})

				if err := reg.Register(duration); err != nil {
					t.Fatalf("failed to register histogram: %v", err)
				}
			},
		},
		{
			name: "MetricsHandler responds with 200 OK",
			run: func(t *testing.T) {
				observability.InitMetrics("testobs")
				h := observability.MetricsHandler()
				if h == nil {
					t.Fatal("expected non-nil handler")
				}
				w := httptest.NewRecorder()
				r, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
				h.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Errorf("expected 200, got %d", w.Code)
				}
			},
		},
		{
			name: "Metrics middleware records request and passes through",
			run: func(t *testing.T) {
				observability.InitMetrics("testmw")
				mw := observability.Metrics()
				w := httptest.NewRecorder()
				r, _ := http.NewRequest(http.MethodGet, "/test", nil)
				mw(noopHandler).ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Errorf("expected 200, got %d", w.Code)
				}
			},
		},
		{
			name: "Metrics middleware extracts Chi RoutePattern",
			run: func(t *testing.T) {
				observability.InitMetrics("testchi")
				r := chi.NewRouter()
				r.Use(observability.Metrics())
				r.Get("/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				})

				w := httptest.NewRecorder()
				req, _ := http.NewRequest(http.MethodGet, "/users/123", nil)
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Errorf("expected 200, got %d", w.Code)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestObservability_Tracing_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.Handler
		wantStatus int
	}{
		{
			name:       "tracing middleware wraps successful handler",
			handler:    noopHandler,
			wantStatus: http.StatusOK,
		},
		{
			name: "tracing middleware records 5xx status code",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mw := observability.Tracing("test-service")
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, "/trace", nil)
			mw(tc.handler).ServeHTTP(w, r)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
		})
	}

	// Test InitTracer disabled
	shutdown, err := observability.InitTracer(context.Background(), config.TelemetryConfig{Enabled: false})
	if err != nil {
		t.Fatalf("InitTracer disabled: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	// Test InitTracer enabled with options
	shutdownEnabled, err := observability.InitTracer(context.Background(), config.TelemetryConfig{
		Enabled:      true,
		ServiceName:  "test-obs-svc",
		OTLPEndpoint: "localhost:4318",
	},
		observability.WithSampler(sdktrace.AlwaysSample()),
		observability.WithPropagator(nil),
	)
	if err != nil {
		t.Fatalf("InitTracer enabled: %v", err)
	}
	if err := shutdownEnabled(context.Background()); err != nil {
		t.Fatalf("shutdownEnabled: %v", err)
	}
}

func TestObservability_Registry(t *testing.T) {
	reg := observability.NewRegistry("testapp",
		observability.WithGoRuntimeMetrics(),
		observability.WithProcessMetrics(),
		observability.WithLatencyBuckets([]float64{0.1, 0.5, 1.0}),
	)

	if reg.PrometheusRegistry() == nil {
		t.Fatal("expected non-nil PrometheusRegistry")
	}

	// Test pre-wired metric accessors
	if reg.HTTPDuration() == nil || reg.HTTPInFlight() == nil || reg.HTTPRequestsTotal() == nil {
		t.Error("expected non-nil HTTP metrics")
	}
	if reg.GRPCServerDuration() == nil || reg.GRPCServerTotal() == nil {
		t.Error("expected non-nil gRPC server metrics")
	}
	if reg.GRPCClientDuration() == nil || reg.GRPCClientTotal() == nil {
		t.Error("expected non-nil gRPC client metrics")
	}

	// Custom metric registration
	counter := reg.MustNewCounter("items_processed_total", "help", "type")
	counter.WithLabelValues("orders").Inc()

	gauge := reg.MustNewGauge("active_workers", "help", "pool")
	gauge.WithLabelValues("default").Set(42)

	hist := reg.MustNewHistogram("db_query_duration_seconds", "help", []float64{0.01, 0.1}, "operation")
	hist.WithLabelValues("select").Observe(0.05)

	// Error branch on duplicate metric name
	_, err := reg.NewCounter("items_processed_total", "duplicate", "type")
	if err == nil {
		t.Error("expected error registering duplicate counter")
	}

	// Test DefaultRegistry getter
	if d := observability.DefaultRegistry(); d == nil {
		t.Error("expected non-nil default registry after InitMetrics")
	}

	// Panic on MustNewGauge duplicate
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic on duplicate gauge")
			}
		}()
		_ = reg.MustNewGauge("active_workers", "dup", "pool")
	}()

	// Panic on MustNewHistogram duplicate
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic on duplicate histogram")
			}
		}()
		_ = reg.MustNewHistogram("db_query_duration_seconds", "dup", nil, "operation")
	}()

	// Panic on MustNewCounter duplicate
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on MustNewCounter duplicate")
		}
	}()
	_ = reg.MustNewCounter("items_processed_total", "duplicate", "type")
}

func TestObservability_HTTPMetrics_And_Handler(t *testing.T) {
	reg := observability.NewRegistry("customsvc")

	// HTTP Metrics Middleware
	mw := observability.HTTPMetrics(reg)
	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/orders", nil)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
	}

	// HTTP Metrics with nil registry is safe no-op
	nilMW := observability.HTTPMetrics(nil)
	recNil := httptest.NewRecorder()
	nilMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(recNil, req)
	if recNil.Code != http.StatusOK {
		t.Errorf("expected 200 from nilMW, got %d", recNil.Code)
	}

	// Handler without auth
	h := reg.HTTPHandler()
	recMetrics := httptest.NewRecorder()
	reqMetrics, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(recMetrics, reqMetrics)

	if recMetrics.Code != http.StatusOK {
		t.Errorf("expected 200 from metrics handler, got %d", recMetrics.Code)
	}
	body := recMetrics.Body.String()
	if !strings.Contains(body, "customsvc_http_requests_total") {
		t.Errorf("expected body to contain customsvc_http_requests_total, got: %s", body)
	}

	// Handler with Basic Auth
	authH := reg.HTTPHandler(observability.WithBasicAuth("admin", "secret"))

	// Unauthorized
	recAuth := httptest.NewRecorder()
	reqAuth, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	authH.ServeHTTP(recAuth, reqAuth)
	if recAuth.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", recAuth.Code)
	}

	// Authorized
	recAuthSuccess := httptest.NewRecorder()
	reqAuthSuccess, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	reqAuthSuccess.SetBasicAuth("admin", "secret")
	authH.ServeHTTP(recAuthSuccess, reqAuthSuccess)
	if recAuthSuccess.Code != http.StatusOK {
		t.Errorf("expected 200 with valid auth, got %d", recAuthSuccess.Code)
	}
}

func TestObservability_GRPC_Interceptors(t *testing.T) {
	reg := observability.NewRegistry("grpcapp")

	// Unary Server Interceptor
	unaryServer := observability.UnaryServerMetrics(reg)
	info := &grpc.UnaryServerInfo{FullMethod: "/users.UserService/GetUser"}

	_, err := unaryServer(context.Background(), "req", info, func(ctx context.Context, req any) (any, error) {
		return "resp", nil
	})
	if err != nil {
		t.Fatalf("unexpected unary server error: %v", err)
	}

	// Unary Server with error
	_, err = unaryServer(context.Background(), "req", info, func(ctx context.Context, req any) (any, error) {
		return nil, status.Error(codes.NotFound, "user not found")
	})
	if err == nil {
		t.Fatal("expected error from handler")
	}

	// Stream Server Interceptor
	streamServer := observability.StreamServerMetrics(reg)
	streamInfo := &grpc.StreamServerInfo{FullMethod: "/chat.ChatService/StreamChat"}
	err = streamServer(nil, nil, streamInfo, func(srv any, stream grpc.ServerStream) error {
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected stream server error: %v", err)
	}

	// Unary Client Interceptor
	unaryClient := observability.UnaryClientMetrics(reg)
	err = unaryClient(context.Background(), "/payment.PaymentService/Charge", "req", "reply", nil, func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected unary client error: %v", err)
	}

	// Stream Client Interceptor
	streamClient := observability.StreamClientMetrics(reg)
	_, err = streamClient(context.Background(), nil, nil, "/payment.PaymentService/StreamAudit", func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected stream client error: %v", err)
	}

	// Test nil registry interceptors (pass through safely)
	uServerNil := observability.UnaryServerMetrics(nil)
	_, _ = uServerNil(context.Background(), "req", info, func(ctx context.Context, req any) (any, error) { return "ok", nil })

	sServerNil := observability.StreamServerMetrics(nil)
	_ = sServerNil(nil, nil, streamInfo, func(srv any, stream grpc.ServerStream) error { return nil })

	uClientNil := observability.UnaryClientMetrics(nil)
	_ = uClientNil(context.Background(), "Ping", "req", "reply", nil, func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return nil
	})

	sClientNil := observability.StreamClientMetrics(nil)
	_, _ = sClientNil(context.Background(), nil, nil, "Ping", func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return nil, nil
	})
}

func TestObservability_HTTPClient_Tracing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Received", "yes")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	defer ts.Close()

	client := observability.NewInstrumentedHTTPClient(5 * time.Second)
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("failed client get: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "pong" {
		t.Errorf("expected pong, got %s", string(body))
	}

	// Wrap nil client with custom tracer name
	c := observability.WrapHTTPClient(nil, "custom-trace")
	if c == nil {
		t.Fatal("expected non-nil wrapped client")
	}

	// 404 response
	ts404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts404.Close()
	resp404, err := client.Get(ts404.URL)
	if err == nil {
		_ = resp404.Body.Close()
	}

	// Transport error
	badClient := observability.NewInstrumentedHTTPClient(5 * time.Second)
	_, _ = badClient.Get("http://127.0.0.1:0")
}

func TestObservability_SpanHelpers(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(trace.NewNoopTracerProvider())

	ctx := context.Background()

	ctx, span := observability.StartSpan(ctx, "test-tracer", "operation-abc")
	defer span.End()

	observability.SetAttributes(ctx, attribute.String("env", "staging"), attribute.Int("attempt", 1))
	observability.AddEvent(ctx, "step_completed", attribute.String("step", "auth"))
	observability.RecordError(ctx, errors.New("minor warning"))

	traceID := observability.TraceID(ctx)
	spanID := observability.SpanID(ctx)

	if traceID == "" || spanID == "" {
		t.Errorf("expected non-empty trace and span IDs, got trace: %q, span: %q", traceID, spanID)
	}

	// Verify SpanFromContext
	extractedSpan := observability.SpanFromContext(ctx)
	if extractedSpan == nil {
		t.Error("expected non-nil extracted span")
	}

	// Verify RecordError with nil does nothing
	observability.RecordError(ctx, nil)

	// Verify empty trace/span ID on plain background context
	if id := observability.TraceID(context.Background()); id != "" {
		t.Errorf("expected empty trace ID on plain context, got %q", id)
	}
	if id := observability.SpanID(context.Background()); id != "" {
		t.Errorf("expected empty span ID on plain context, got %q", id)
	}
}
