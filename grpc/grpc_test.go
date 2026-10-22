package grpc_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/circuitbreaker"
	gokiterrors "github.com/dhawalhost/gokit/errors"
	gokitgrpc "github.com/dhawalhost/gokit/grpc"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// Simple JSON codec for testing RPC payloads over bufconn without protoc-generated code.
type jsonCodec struct{}

func (jsonCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (jsonCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (jsonCodec) Name() string {
	return "json"
}

func init() {
	encoding.RegisterCodec(jsonCodec{})
}

// Test message structs
type testRequest struct {
	Message     string `json:"message"`
	ShouldPanic bool   `json:"should_panic"`
	ErrorCode   int    `json:"error_code"`
	Invalid     bool   `json:"invalid"`
	ValidateCtx bool   `json:"validate_ctx"`
	ValidateAll bool   `json:"validate_all"`
}

func (r *testRequest) Validate() error {
	if r.Invalid && !r.ValidateCtx && !r.ValidateAll {
		return errors.New("request marked invalid")
	}
	return nil
}

type testRequestCtx struct {
	*testRequest
}

func (r *testRequestCtx) Validate(ctx context.Context) error {
	if r.Invalid {
		return errors.New("request ctx marked invalid")
	}
	return nil
}

type testRequestAll struct {
	Message string `json:"message"`
	Invalid bool   `json:"invalid"`
}

func (r *testRequestAll) ValidateAll() error {
	if r.Invalid {
		return errors.New("request all marked invalid")
	}
	return nil
}

type testResponse struct {
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// Error Mapping Unit Tests
// ---------------------------------------------------------------------------

func TestErrors_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		inputErr     error
		expectedCode codes.Code
		expectedHTTP int
	}{
		{
			name:         "nil error returns OK",
			inputErr:     nil,
			expectedCode: codes.OK,
			expectedHTTP: 0,
		},
		{
			name:         "existing gRPC status error preserved",
			inputErr:     status.Error(codes.AlreadyExists, "entity exists"),
			expectedCode: codes.AlreadyExists,
			expectedHTTP: http.StatusConflict,
		},
		{
			name:         "context.Canceled maps to Canceled",
			inputErr:     context.Canceled,
			expectedCode: codes.Canceled,
			expectedHTTP: 499,
		},
		{
			name:         "context.DeadlineExceeded maps to DeadlineExceeded",
			inputErr:     context.DeadlineExceeded,
			expectedCode: codes.DeadlineExceeded,
			expectedHTTP: http.StatusGatewayTimeout,
		},
		{
			name:         "BadRequest maps to InvalidArgument",
			inputErr:     gokiterrors.BadRequest("BAD_REQ", "invalid field"),
			expectedCode: codes.InvalidArgument,
			expectedHTTP: http.StatusBadRequest,
		},
		{
			name:         "Unauthorized maps to Unauthenticated",
			inputErr:     gokiterrors.Unauthorized("NO_AUTH", "missing token"),
			expectedCode: codes.Unauthenticated,
			expectedHTTP: http.StatusUnauthorized,
		},
		{
			name:         "Forbidden maps to PermissionDenied",
			inputErr:     gokiterrors.Forbidden("FORBIDDEN", "access denied"),
			expectedCode: codes.PermissionDenied,
			expectedHTTP: http.StatusForbidden,
		},
		{
			name:         "NotFound maps to NotFound",
			inputErr:     gokiterrors.NotFound("NOT_FOUND", "user not found"),
			expectedCode: codes.NotFound,
			expectedHTTP: http.StatusNotFound,
		},
		{
			name:         "Conflict maps to AlreadyExists",
			inputErr:     gokiterrors.Conflict("CONFLICT", "slug already taken"),
			expectedCode: codes.AlreadyExists,
			expectedHTTP: http.StatusConflict,
		},
		{
			name:         "UnprocessableEntity maps to InvalidArgument",
			inputErr:     gokiterrors.UnprocessableEntity("UNPROCESSABLE", "bad format"),
			expectedCode: codes.InvalidArgument,
			expectedHTTP: http.StatusBadRequest,
		},
		{
			name:         "TooManyRequests maps to ResourceExhausted",
			inputErr:     gokiterrors.TooManyRequests("RATE_LIMIT", "too many requests"),
			expectedCode: codes.ResourceExhausted,
			expectedHTTP: http.StatusTooManyRequests,
		},
		{
			name:         "ServiceUnavailable maps to Unavailable",
			inputErr:     gokiterrors.ServiceUnavailable("down for maintenance"),
			expectedCode: codes.Unavailable,
			expectedHTTP: http.StatusServiceUnavailable,
		},
		{
			name:         "Internal error maps to Internal",
			inputErr:     gokiterrors.Internal("database crashed"),
			expectedCode: codes.Internal,
			expectedHTTP: http.StatusInternalServerError,
		},
		{
			name:         "Generic error maps to Internal",
			inputErr:     errors.New("random disk failure"),
			expectedCode: codes.Internal,
			expectedHTTP: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := gokitgrpc.ToStatus(tc.inputErr)
			if st.Code() != tc.expectedCode {
				t.Errorf("ToStatus code = %v, want %v", st.Code(), tc.expectedCode)
			}

			grpcErr := gokitgrpc.ToGRPCError(tc.inputErr)
			if tc.inputErr == nil && grpcErr != nil {
				t.Errorf("expected nil ToGRPCError, got %v", grpcErr)
			}
			if tc.inputErr != nil && grpcErr == nil {
				t.Errorf("expected non-nil ToGRPCError")
			}

			if tc.inputErr != nil {
				appErr := gokitgrpc.FromStatus(st)
				if appErr == nil {
					t.Fatalf("FromStatus returned nil for non-nil error")
				}
				if appErr.HTTPStatus != tc.expectedHTTP {
					t.Errorf("FromStatus HTTPStatus = %d, want %d", appErr.HTTPStatus, tc.expectedHTTP)
				}

				fromErr := gokitgrpc.FromGRPCError(grpcErr)
				if fromErr == nil {
					t.Errorf("FromGRPCError returned nil for non-nil error")
				}
			}
		})
	}
}

func TestErrors_AllGRPCCodesCoverage(t *testing.T) {
	codeList := []codes.Code{
		codes.InvalidArgument,
		codes.Unauthenticated,
		codes.PermissionDenied,
		codes.NotFound,
		codes.AlreadyExists,
		codes.ResourceExhausted,
		codes.FailedPrecondition,
		codes.Aborted,
		codes.OutOfRange,
		codes.Unimplemented,
		codes.Internal,
		codes.Unavailable,
		codes.DataLoss,
		codes.DeadlineExceeded,
		codes.Canceled,
		codes.Unknown,
	}

	for _, code := range codeList {
		st := status.New(code, "test-message")
		ae := gokitgrpc.FromStatus(st)
		if ae == nil {
			t.Errorf("FromStatus returned nil for code %v", code)
		}
	}

	// Nil cases
	if gokitgrpc.FromStatus(nil) != nil {
		t.Error("expected nil from nil status")
	}
	if gokitgrpc.FromStatus(status.New(codes.OK, "")) != nil {
		t.Error("expected nil from OK status")
	}
	if gokitgrpc.FromGRPCError(nil) != nil {
		t.Error("expected nil from nil error")
	}
	// Non-status error wrapping
	wrapped := gokitgrpc.FromGRPCError(errors.New("plain non-grpc error"))
	if wrapped == nil {
		t.Error("expected non-nil wrapped error")
	}
}

// ---------------------------------------------------------------------------
// Server and Client Integration Tests via bufconn
// ---------------------------------------------------------------------------

const bufSize = 1024 * 1024

type testServiceInterface interface {
	Echo(ctx context.Context, req *testRequest) (*testResponse, error)
}

type serviceMock struct {
	echoFn   func(ctx context.Context, req *testRequest) (*testResponse, error)
	streamFn func(srv any, ss grpc.ServerStream) error
}

func (m *serviceMock) Echo(ctx context.Context, req *testRequest) (*testResponse, error) {
	if m.echoFn != nil {
		return m.echoFn(ctx, req)
	}
	return nil, nil
}

func setupTestEnvironment(t *testing.T, srvCfg gokitgrpc.ServerConfig, clientCfg gokitgrpc.ClientConfig, mock *serviceMock) (*grpc.ClientConn, func()) {
	lis := bufconn.Listen(bufSize)

	srv := gokitgrpc.NewServer(srvCfg)

	serviceDesc := grpc.ServiceDesc{
		ServiceName: "test.TestService",
		HandlerType: (*testServiceInterface)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "Echo",
				Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
					in := new(testRequest)
					if err := dec(in); err != nil {
						return nil, err
					}
					var reqToValidate any = in
					if in.ValidateCtx {
						reqToValidate = &testRequestCtx{testRequest: in}
					} else if in.ValidateAll {
						reqToValidate = &testRequestAll{Message: in.Message, Invalid: in.Invalid}
					}
					if interceptor == nil {
						return mock.echoFn(ctx, in)
					}
					info := &grpc.UnaryServerInfo{
						Server:     srv,
						FullMethod: "/test.TestService/Echo",
					}
					handler := func(c context.Context, _ any) (any, error) {
						return mock.echoFn(c, in)
					}
					return interceptor(ctx, reqToValidate, info, handler)
				},
			},
		},
		Streams: []grpc.StreamDesc{
			{
				StreamName: "EchoStream",
				Handler: func(srv any, ss grpc.ServerStream) error {
					return mock.streamFn(srv, ss)
				},
				ServerStreams: true,
				ClientStreams: true,
			},
		},
	}
	srv.RegisterService(&serviceDesc, mock)

	go func() {
		_ = srv.Serve(lis)
	}()

	clientCfg.Target = "passthrough://bufnet"
	clientCfg.Insecure = true

	conn, err := gokitgrpc.Dial(context.Background(), clientCfg,
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype("json")),
	)
	if err != nil {
		srv.Stop()
		t.Fatalf("Dial error: %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = lis.Close()
	}

	return conn, cleanup
}

func TestServer_UnaryInterceptors_TableDriven(t *testing.T) {
	log := zaptest.NewLogger(t)

	tests := []struct {
		name        string
		req         testRequest
		authHeader  string
		setupMock   func() *serviceMock
		assertResp  func(t *testing.T, resp *testResponse, header metadata.MD, err error)
		useAuth     bool
		userRole    string
		expectError bool
	}{
		{
			name: "Normal request succeeds with RequestID and Tracing",
			req:  testRequest{Message: "hello gRPC"},
			setupMock: func() *serviceMock {
				return &serviceMock{
					echoFn: func(ctx context.Context, req *testRequest) (*testResponse, error) {
						reqID := gokitgrpc.RequestIDFromContext(ctx)
						if reqID == "" {
							t.Error("expected non-empty request ID in server context")
						}
						return &testResponse{Message: "echo: " + req.Message}, nil
					},
				}
			},
			assertResp: func(t *testing.T, resp *testResponse, header metadata.MD, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if resp.Message != "echo: hello gRPC" {
					t.Errorf("expected echo message, got %s", resp.Message)
				}
				ids := header.Get("x-request-id")
				if len(ids) == 0 || ids[0] == "" {
					t.Errorf("expected x-request-id response header")
				}
			},
		},
		{
			name: "Panic recovery returns codes.Internal",
			req:  testRequest{Message: "boom", ShouldPanic: true},
			setupMock: func() *serviceMock {
				return &serviceMock{
					echoFn: func(_ context.Context, req *testRequest) (*testResponse, error) {
						if req.ShouldPanic {
							panic("unexpected runtime crash")
						}
						return &testResponse{}, nil
					},
				}
			},
			assertResp: func(t *testing.T, _ *testResponse, _ metadata.MD, err error) {
				if err == nil {
					t.Fatal("expected error from panic, got nil")
				}
				st := status.Convert(err)
				if st.Code() != codes.Internal {
					t.Errorf("expected codes.Internal, got %v", st.Code())
				}
				if !strings.Contains(st.Message(), "panic recovered") {
					t.Errorf("expected panic recovered in message, got %s", st.Message())
				}
			},
			expectError: true,
		},
		{
			name: "Validation interceptor rejects invalid request",
			req:  testRequest{Message: "bad", Invalid: true},
			setupMock: func() *serviceMock {
				return &serviceMock{
					echoFn: func(_ context.Context, _ *testRequest) (*testResponse, error) {
						return &testResponse{Message: "should not reach here"}, nil
					},
				}
			},
			assertResp: func(t *testing.T, _ *testResponse, _ metadata.MD, err error) {
				if err == nil {
					t.Fatal("expected validation error, got nil")
				}
				st := status.Convert(err)
				if st.Code() != codes.InvalidArgument {
					t.Errorf("expected codes.InvalidArgument, got %v", st.Code())
				}
			},
			expectError: true,
		},
		{
			name:       "Auth interceptor accepts valid bearer token",
			req:        testRequest{Message: "secret data"},
			authHeader: "Bearer valid-token-123",
			useAuth:    true,
			setupMock: func() *serviceMock {
				return &serviceMock{
					echoFn: func(ctx context.Context, req *testRequest) (*testResponse, error) {
						role := ctx.Value("role")
						if role != "admin" {
							t.Errorf("expected role admin in context, got %v", role)
						}
						return &testResponse{Message: "authed: " + req.Message}, nil
					},
				}
			},
			assertResp: func(t *testing.T, resp *testResponse, _ metadata.MD, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if resp.Message != "authed: secret data" {
					t.Errorf("unexpected response message: %s", resp.Message)
				}
			},
		},
		{
			name:       "Auth interceptor rejects missing or bad token",
			req:        testRequest{Message: "secret data"},
			authHeader: "InvalidScheme 123",
			useAuth:    true,
			setupMock: func() *serviceMock {
				return &serviceMock{
					echoFn: func(_ context.Context, _ *testRequest) (*testResponse, error) {
						return &testResponse{}, nil
					},
				}
			},
			assertResp: func(t *testing.T, _ *testResponse, _ metadata.MD, err error) {
				if err == nil {
					t.Fatal("expected unauthenticated error, got nil")
				}
				st := status.Convert(err)
				if st.Code() != codes.Unauthenticated {
					t.Errorf("expected codes.Unauthenticated, got %v", st.Code())
				}
			},
			expectError: true,
		},
		{
			name:       "Auth interceptor rejects empty bearer token",
			req:        testRequest{Message: "secret data"},
			authHeader: "Bearer   ",
			useAuth:    true,
			setupMock: func() *serviceMock {
				return &serviceMock{
					echoFn: func(_ context.Context, _ *testRequest) (*testResponse, error) {
						return &testResponse{}, nil
					},
				}
			},
			assertResp: func(t *testing.T, _ *testResponse, _ metadata.MD, err error) {
				if err == nil {
					t.Fatal("expected unauthenticated error, got nil")
				}
				st := status.Convert(err)
				if st.Code() != codes.Unauthenticated {
					t.Errorf("expected codes.Unauthenticated, got %v", st.Code())
				}
			},
			expectError: true,
		},
		{
			name: "Validation interceptor rejects invalid request with Validate(ctx)",
			req:  testRequest{Message: "bad", Invalid: true, ValidateCtx: true},
			setupMock: func() *serviceMock {
				return &serviceMock{
					echoFn: func(_ context.Context, _ *testRequest) (*testResponse, error) {
						return &testResponse{}, nil
					},
				}
			},
			assertResp: func(t *testing.T, _ *testResponse, _ metadata.MD, err error) {
				if err == nil {
					t.Fatal("expected validation error, got nil")
				}
				st := status.Convert(err)
				if st.Code() != codes.InvalidArgument {
					t.Errorf("expected InvalidArgument, got %v", st.Code())
				}
			},
			expectError: true,
		},
		{
			name: "Validation interceptor rejects invalid request with ValidateAll()",
			req:  testRequest{Message: "bad", Invalid: true, ValidateAll: true},
			setupMock: func() *serviceMock {
				return &serviceMock{
					echoFn: func(_ context.Context, _ *testRequest) (*testResponse, error) {
						return &testResponse{}, nil
					},
				}
			},
			assertResp: func(t *testing.T, _ *testResponse, _ metadata.MD, err error) {
				if err == nil {
					t.Fatal("expected validation error, got nil")
				}
				st := status.Convert(err)
				if st.Code() != codes.InvalidArgument {
					t.Errorf("expected InvalidArgument, got %v", st.Code())
				}
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := tc.setupMock()

			unaryInterceptors := gokitgrpc.DefaultUnaryServerInterceptors(log, "test-tracer")
			if tc.useAuth {
				authInterceptor := gokitgrpc.UnaryAuth(func(ctx context.Context, token string) (context.Context, error) {
					if token == "valid-token-123" {
						return context.WithValue(ctx, "role", "admin"), nil
					}
					return nil, gokiterrors.Unauthorized("INVALID_TOKEN", "bad token")
				})
				unaryInterceptors = append([]grpc.UnaryServerInterceptor{authInterceptor}, unaryInterceptors...)
			}

			srvCfg := gokitgrpc.ServerConfig{
				EnableReflection:  true,
				UnaryInterceptors: unaryInterceptors,
			}
			clientCfg := gokitgrpc.ClientConfig{
				UnaryInterceptors: gokitgrpc.DefaultUnaryClientInterceptors(log, "test-tracer"),
			}

			conn, cleanup := setupTestEnvironment(t, srvCfg, clientCfg, mock)
			defer cleanup()

			ctx := context.Background()
			if tc.authHeader != "" {
				ctx = metadata.AppendToOutgoingContext(ctx, "authorization", tc.authHeader)
			}

			var header metadata.MD
			var resp testResponse
			err := conn.Invoke(ctx, "/test.TestService/Echo", &tc.req, &resp, grpc.Header(&header))
			tc.assertResp(t, &resp, header, err)
		})
	}
}

func TestServer_StreamInterceptors_TableDriven(t *testing.T) {
	log := zaptest.NewLogger(t)

	tests := []struct {
		name       string
		authHeader string
		shouldFail bool
		run        func(t *testing.T, ss grpc.ClientStream)
	}{
		{
			name:       "Stream logging, tracing, and request ID succeed",
			authHeader: "Bearer valid-token-123",
			run: func(t *testing.T, stream grpc.ClientStream) {
				req := testRequest{Message: "stream msg"}
				if err := stream.SendMsg(&req); err != nil {
					t.Fatalf("SendMsg error: %v", err)
				}
				var resp testResponse
				if err := stream.RecvMsg(&resp); err != nil {
					t.Fatalf("RecvMsg error: %v", err)
				}
				if resp.Message != "stream echo: stream msg" {
					t.Errorf("unexpected response: %s", resp.Message)
				}
			},
		},
		{
			name:       "Stream auth rejects missing header",
			authHeader: "",
			shouldFail: true,
			run: func(t *testing.T, stream grpc.ClientStream) {
				req := testRequest{Message: "stream msg"}
				_ = stream.SendMsg(&req)
				var resp testResponse
				err := stream.RecvMsg(&resp)
				if err == nil {
					t.Fatal("expected stream error for missing auth header")
				}
				st := status.Convert(err)
				if st.Code() != codes.Unauthenticated {
					t.Errorf("expected Unauthenticated, got %v", st.Code())
				}
			},
		},
		{
			name:       "Stream recovery catches panic and returns codes.Internal",
			authHeader: "Bearer valid-token-123",
			shouldFail: true,
			run: func(t *testing.T, stream grpc.ClientStream) {
				req := testRequest{Message: "stream panic", ShouldPanic: true}
				_ = stream.SendMsg(&req)
				var resp testResponse
				err := stream.RecvMsg(&resp)
				if err == nil {
					t.Fatal("expected stream error for panic, got nil")
				}
				st := status.Convert(err)
				if st.Code() != codes.Internal {
					t.Errorf("expected codes.Internal, got %v", st.Code())
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &serviceMock{
				streamFn: func(_ any, ss grpc.ServerStream) error {
					ctx := ss.Context()
					reqID := gokitgrpc.RequestIDFromContext(ctx)
					if reqID == "" {
						t.Error("expected request ID in stream context")
					}
					var req testRequest
					if err := ss.RecvMsg(&req); err != nil {
						return err
					}
					if req.ShouldPanic {
						panic("stream runtime crash")
					}
					return ss.SendMsg(&testResponse{Message: "stream echo: " + req.Message})
				},
			}

			streamInterceptors := []grpc.StreamServerInterceptor{
				gokitgrpc.StreamRecovery(log),
				gokitgrpc.StreamRequestID(),
				gokitgrpc.StreamAuth(func(ctx context.Context, token string) (context.Context, error) {
					if token == "valid-token-123" {
						return ctx, nil
					}
					return nil, status.Error(codes.Unauthenticated, "invalid token")
				}),
				gokitgrpc.StreamTracing("test-tracer"),
				gokitgrpc.StreamLogging(log),
			}

			srvCfg := gokitgrpc.ServerConfig{
				StreamInterceptors: streamInterceptors,
			}
			clientCfg := gokitgrpc.ClientConfig{}

			conn, cleanup := setupTestEnvironment(t, srvCfg, clientCfg, mock)
			defer cleanup()

			ctx := context.Background()
			if tc.authHeader != "" {
				ctx = metadata.AppendToOutgoingContext(ctx, "authorization", tc.authHeader)
			}

			streamDesc := &grpc.StreamDesc{
				StreamName:    "EchoStream",
				ServerStreams: true,
				ClientStreams: true,
			}
			stream, err := conn.NewStream(ctx, streamDesc, "/test.TestService/EchoStream")
			if err != nil {
				t.Fatalf("NewStream error: %v", err)
			}

			tc.run(t, stream)
		})
	}
}

func TestClient_Retry_And_CircuitBreaker_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		attempts  int
		setupMock func(attempts *int32) *serviceMock
		cb        *circuitbreaker.CircuitBreaker
		retry     *gokitgrpc.RetryConfig
		expected  func(t *testing.T, attempts int32, err error)
	}{
		{
			name: "Retry recovers from transient codes.Unavailable after 2 failures",
			setupMock: func(attempts *int32) *serviceMock {
				return &serviceMock{
					echoFn: func(_ context.Context, req *testRequest) (*testResponse, error) {
						count := atomic.AddInt32(attempts, 1)
						if count < 3 {
							return nil, status.Error(codes.Unavailable, "temporary outage")
						}
						return &testResponse{Message: "success on attempt 3"}, nil
					},
				}
			},
			retry: &gokitgrpc.RetryConfig{
				MaxAttempts:    4,
				InitialBackoff: 5 * time.Millisecond,
				MaxBackoff:     20 * time.Millisecond,
				RetryableCodes: []codes.Code{codes.Unavailable},
			},
			expected: func(t *testing.T, attempts int32, err error) {
				if err != nil {
					t.Fatalf("expected retry success, got error: %v", err)
				}
				if attempts != 3 {
					t.Errorf("expected exactly 3 attempts, got %d", attempts)
				}
			},
		},
		{
			name: "Non-retryable error (codes.NotFound) fails immediately without retry",
			setupMock: func(attempts *int32) *serviceMock {
				return &serviceMock{
					echoFn: func(_ context.Context, _ *testRequest) (*testResponse, error) {
						atomic.AddInt32(attempts, 1)
						return nil, status.Error(codes.NotFound, "entity not found")
					},
				}
			},
			retry: &gokitgrpc.RetryConfig{
				MaxAttempts:    5,
				InitialBackoff: 5 * time.Millisecond,
				RetryableCodes: []codes.Code{codes.Unavailable},
			},
			expected: func(t *testing.T, attempts int32, err error) {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				st := status.Convert(err)
				if st.Code() != codes.NotFound {
					t.Errorf("expected NotFound, got %v", st.Code())
				}
				if attempts != 1 {
					t.Errorf("expected exactly 1 attempt, got %d", attempts)
				}
			},
		},
		{
			name: "Circuit breaker trips after consecutive failures",
			setupMock: func(attempts *int32) *serviceMock {
				return &serviceMock{
					echoFn: func(_ context.Context, _ *testRequest) (*testResponse, error) {
						atomic.AddInt32(attempts, 1)
						return nil, status.Error(codes.Unavailable, "server unavailable")
					},
				}
			},
			cb: circuitbreaker.New(circuitbreaker.Config{
				Name:         "test-cb",
				MaxFailures:  2,
				ResetTimeout: 5 * time.Second,
			}),
			expected: func(t *testing.T, attempts int32, _ error) {
				// The test runner will invoke 3 calls; the 3rd should be rejected by circuit breaker
				if attempts != 2 {
					t.Errorf("expected exactly 2 backend executions before trip, got %d", attempts)
				}
			},
		},
		{
			name: "Retry aborted immediately when context is cancelled",
			setupMock: func(attempts *int32) *serviceMock {
				return &serviceMock{
					echoFn: func(_ context.Context, _ *testRequest) (*testResponse, error) {
						atomic.AddInt32(attempts, 1)
						return nil, status.Error(codes.Unavailable, "server offline")
					},
				}
			},
			retry: &gokitgrpc.RetryConfig{
				MaxAttempts:    5,
				InitialBackoff: 100 * time.Millisecond,
			},
			expected: func(t *testing.T, _ int32, err error) {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("expected context.Canceled, got %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var attempts int32
			mock := tc.setupMock(&attempts)

			clientCfg := gokitgrpc.ClientConfig{
				Retry:          tc.retry,
				CircuitBreaker: tc.cb,
			}

			conn, cleanup := setupTestEnvironment(t, gokitgrpc.ServerConfig{}, clientCfg, mock)
			defer cleanup()

			ctx := context.Background()
			var resp testResponse

			if tc.name == "Retry aborted immediately when context is cancelled" {
				cancelCtx, cancel := context.WithCancel(ctx)
				time.AfterFunc(15*time.Millisecond, cancel)
				err := conn.Invoke(cancelCtx, "/test.TestService/Echo", &testRequest{}, &resp)
				tc.expected(t, atomic.LoadInt32(&attempts), err)
				return
			}

			if tc.cb != nil {
				// Trigger calls to trip the breaker
				_ = conn.Invoke(ctx, "/test.TestService/Echo", &testRequest{}, &resp)
				_ = conn.Invoke(ctx, "/test.TestService/Echo", &testRequest{}, &resp)
				err := conn.Invoke(ctx, "/test.TestService/Echo", &testRequest{}, &resp)
				if err == nil || !strings.Contains(err.Error(), "circuit is open") {
					t.Errorf("expected circuit breaker open error on 3rd call, got %v", err)
				}
				tc.expected(t, atomic.LoadInt32(&attempts), err)
			} else {
				err := conn.Invoke(ctx, "/test.TestService/Echo", &testRequest{}, &resp)
				tc.expected(t, atomic.LoadInt32(&attempts), err)
			}
		})
	}
}

func TestClient_DialValidation(t *testing.T) {
	ctx := context.Background()
	_, err := gokitgrpc.Dial(ctx, gokitgrpc.ClientConfig{Target: ""})
	if err == nil {
		t.Error("expected error for empty target, got nil")
	}
}

func TestServer_DefaultsAndHelpers(t *testing.T) {
	log := zaptest.NewLogger(t)
	s := gokitgrpc.NewServer(gokitgrpc.ServerConfig{
		MaxRecvMsgSize: -1,
		MaxSendMsgSize: -1,
	})
	if s == nil {
		t.Fatal("expected non-nil server")
	}

	streamChains := gokitgrpc.DefaultStreamServerInterceptors(log, "trace")
	if len(streamChains) != 4 {
		t.Errorf("expected 4 stream interceptors, got %d", len(streamChains))
	}

	// Dial with TLSConfig & Keepalive
	conn, err := gokitgrpc.Dial(context.Background(), gokitgrpc.ClientConfig{
		Target:    "example.com:443",
		TLSConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402
		Keepalive: &keepalive.ClientParameters{
			Time: 10 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	_ = conn.Close()
}
