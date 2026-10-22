package grpc

import (
	"context"
	"net"
	"runtime/debug"
	"strings"
	"time"

	"github.com/dhawalhost/gokit/middleware"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	trace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

// ServerConfig configures a gRPC server.
type ServerConfig struct {
	// MaxRecvMsgSize is the maximum message size in bytes the server can receive (default: 16MB).
	MaxRecvMsgSize int
	// MaxSendMsgSize is the maximum message size in bytes the server can send (default: 16MB).
	MaxSendMsgSize int
	// KeepaliveParams configures connection keepalive ping parameters.
	KeepaliveParams *keepalive.ServerParameters
	// KeepalivePolicy configures enforcement policy for client pings.
	KeepalivePolicy *keepalive.EnforcementPolicy
	// EnableReflection registers gRPC server reflection if true (useful for grpcurl / Postman).
	EnableReflection bool
	// UnaryInterceptors is a list of unary interceptors to chain.
	UnaryInterceptors []grpc.UnaryServerInterceptor
	// StreamInterceptors is a list of stream interceptors to chain.
	StreamInterceptors []grpc.StreamServerInterceptor
}

// NewServer creates a new configured gRPC server with sensible production defaults.
func NewServer(cfg ServerConfig, extraOpts ...grpc.ServerOption) *grpc.Server {
	opts := make([]grpc.ServerOption, 0, len(extraOpts)+6)

	maxRecv := cfg.MaxRecvMsgSize
	if maxRecv <= 0 {
		maxRecv = 16 * 1024 * 1024 // 16MB
	}
	opts = append(opts, grpc.MaxRecvMsgSize(maxRecv))

	maxSend := cfg.MaxSendMsgSize
	if maxSend <= 0 {
		maxSend = 16 * 1024 * 1024 // 16MB
	}
	opts = append(opts, grpc.MaxSendMsgSize(maxSend))

	if cfg.KeepaliveParams != nil {
		opts = append(opts, grpc.KeepaliveParams(*cfg.KeepaliveParams))
	} else {
		opts = append(opts, grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     15 * time.Minute,
			MaxConnectionAge:      30 * time.Minute,
			MaxConnectionAgeGrace: 5 * time.Second,
			Time:                  2 * time.Hour,
			Timeout:               20 * time.Second,
		}))
	}

	if cfg.KeepalivePolicy != nil {
		opts = append(opts, grpc.KeepaliveEnforcementPolicy(*cfg.KeepalivePolicy))
	} else {
		opts = append(opts, grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}))
	}

	if len(cfg.UnaryInterceptors) > 0 {
		opts = append(opts, grpc.ChainUnaryInterceptor(cfg.UnaryInterceptors...))
	}

	if len(cfg.StreamInterceptors) > 0 {
		opts = append(opts, grpc.ChainStreamInterceptor(cfg.StreamInterceptors...))
	}

	opts = append(opts, extraOpts...)

	srv := grpc.NewServer(opts...)

	if cfg.EnableReflection {
		reflection.Register(srv)
	}

	return srv
}

// wrappedServerStream wraps grpc.ServerStream with a modified context.Context.
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

// WrapServerStream wraps a grpc.ServerStream with a new context.
func WrapServerStream(ss grpc.ServerStream, ctx context.Context) grpc.ServerStream {
	return &wrappedServerStream{
		ServerStream: ss,
		ctx:          ctx,
	}
}

// UnaryRecovery catches panics in unary RPCs, logs the stack trace, and returns codes.Internal.
func UnaryRecovery(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				stack := string(debug.Stack())
				if log != nil {
					log.Error("grpc unary panic recovered",
						zap.String("method", info.FullMethod),
						zap.Any("panic", r),
						zap.String("stack", stack),
					)
				}
				err = status.Errorf(grpccodes.Internal, "panic recovered: %v", r)
			}
		}()
		return handler(ctx, req)
	}
}

// StreamRecovery catches panics in streaming RPCs, logs the stack trace, and returns codes.Internal.
func StreamRecovery(log *zap.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				stack := string(debug.Stack())
				if log != nil {
					log.Error("grpc stream panic recovered",
						zap.String("method", info.FullMethod),
						zap.Any("panic", r),
						zap.String("stack", stack),
					)
				}
				err = status.Errorf(grpccodes.Internal, "panic recovered: %v", r)
			}
		}()
		return handler(srv, ss)
	}
}

// UnaryRequestID extracts X-Request-ID from incoming metadata or generates a new UUID.
// It stores the request ID in the context and echoes it in outgoing header metadata.
func UnaryRequestID() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		reqID := extractOrGenerateRequestID(ctx)
		ctx = setRequestIDInContext(ctx, reqID)
		_ = grpc.SetHeader(ctx, metadata.Pairs("x-request-id", reqID))
		return handler(ctx, req)
	}
}

// StreamRequestID extracts X-Request-ID from incoming metadata or generates a new UUID for streaming RPCs.
func StreamRequestID() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		reqID := extractOrGenerateRequestID(ctx)
		ctx = setRequestIDInContext(ctx, reqID)
		_ = grpc.SetHeader(ctx, metadata.Pairs("x-request-id", reqID))
		return handler(srv, WrapServerStream(ss, ctx))
	}
}

// UnaryLogging logs completed unary RPC invocations with execution duration and status code.
func UnaryLogging(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if log == nil {
			return handler(ctx, req)
		}

		start := time.Now()
		resp, err := handler(ctx, req)
		duration := time.Since(start)

		st := ToStatus(err)
		code := st.Code()
		peerIP := extractPeerIP(ctx)
		reqID := middleware.RequestIDFromContext(ctx)

		fields := []zap.Field{
			zap.String("grpc.method", info.FullMethod),
			zap.String("grpc.code", code.String()),
			zap.Duration("duration_ms", duration),
			zap.String("peer.ip", peerIP),
		}
		if reqID != "" {
			fields = append(fields, zap.String("request_id", reqID))
		}
		if err != nil && code != grpccodes.OK {
			fields = append(fields, zap.Error(err))
			log.Warn("grpc unary call completed with error", fields...)
		} else {
			log.Info("grpc unary call completed", fields...)
		}

		return resp, err
	}
}

// StreamLogging logs stream RPC completion and duration.
func StreamLogging(log *zap.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if log == nil {
			return handler(srv, ss)
		}

		start := time.Now()
		ctx := ss.Context()
		err := handler(srv, ss)
		duration := time.Since(start)

		st := ToStatus(err)
		code := st.Code()
		peerIP := extractPeerIP(ctx)
		reqID := middleware.RequestIDFromContext(ctx)

		fields := []zap.Field{
			zap.String("grpc.method", info.FullMethod),
			zap.String("grpc.code", code.String()),
			zap.Duration("duration_ms", duration),
			zap.String("peer.ip", peerIP),
		}
		if reqID != "" {
			fields = append(fields, zap.String("request_id", reqID))
		}
		if err != nil && code != grpccodes.OK {
			fields = append(fields, zap.Error(err))
			log.Warn("grpc stream completed with error", fields...)
		} else {
			log.Info("grpc stream completed", fields...)
		}

		return err
	}
}

// UnaryTracing extracts OpenTelemetry context from incoming metadata and creates a server span.
func UnaryTracing(tracerName string) grpc.UnaryServerInterceptor {
	if tracerName == "" {
		tracerName = "gokit/grpc"
	}
	tr := otel.GetTracerProvider().Tracer(tracerName)

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = extractTraceContext(ctx)
		service, method := splitMethodName(info.FullMethod)

		ctx, span := tr.Start(ctx, info.FullMethod,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.RPCSystemKey.String("grpc"),
				semconv.RPCServiceKey.String(service),
				semconv.RPCMethodKey.String(method),
			),
		)
		defer span.End()

		resp, err := handler(ctx, req)
		st := ToStatus(err)
		if err != nil && st.Code() != grpccodes.OK {
			span.RecordError(err)
			span.SetStatus(codes.Error, st.Message())
			span.SetAttributes(attribute.String("rpc.grpc.status_code", st.Code().String()))
		} else {
			span.SetStatus(codes.Ok, "")
		}

		return resp, err
	}
}

// StreamTracing extracts OpenTelemetry context and traces gRPC streams.
func StreamTracing(tracerName string) grpc.StreamServerInterceptor {
	if tracerName == "" {
		tracerName = "gokit/grpc"
	}
	tr := otel.GetTracerProvider().Tracer(tracerName)

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := extractTraceContext(ss.Context())
		service, method := splitMethodName(info.FullMethod)

		ctx, span := tr.Start(ctx, info.FullMethod,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.RPCSystemKey.String("grpc"),
				semconv.RPCServiceKey.String(service),
				semconv.RPCMethodKey.String(method),
			),
		)
		defer span.End()

		err := handler(srv, WrapServerStream(ss, ctx))
		st := ToStatus(err)
		if err != nil && st.Code() != grpccodes.OK {
			span.RecordError(err)
			span.SetStatus(codes.Error, st.Message())
			span.SetAttributes(attribute.String("rpc.grpc.status_code", st.Code().String()))
		} else {
			span.SetStatus(codes.Ok, "")
		}

		return err
	}
}

// Authenticator validates an authentication token and returns an enriched context.
type Authenticator func(ctx context.Context, token string) (context.Context, error)

// UnaryAuth extracts a bearer token from metadata and validates it using the provided Authenticator.
func UnaryAuth(authenticator Authenticator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		token, err := extractBearerToken(ctx)
		if err != nil {
			return nil, err
		}

		authedCtx, err := authenticator(ctx, token)
		if err != nil {
			return nil, ToGRPCError(err)
		}

		return handler(authedCtx, req)
	}
}

// StreamAuth validates the token on stream initialization and propagates the authenticated context.
func StreamAuth(authenticator Authenticator) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		token, err := extractBearerToken(ctx)
		if err != nil {
			return err
		}

		authedCtx, err := authenticator(ctx, token)
		if err != nil {
			return ToGRPCError(err)
		}

		return handler(srv, WrapServerStream(ss, authedCtx))
	}
}

// UnaryValidation validates the incoming request if it implements validation interfaces.
func UnaryValidation() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if v, ok := req.(interface{ Validate() error }); ok {
			if err := v.Validate(); err != nil {
				return nil, status.Errorf(grpccodes.InvalidArgument, "validation error: %v", err)
			}
		} else if v, ok := req.(interface{ Validate(context.Context) error }); ok {
			if err := v.Validate(ctx); err != nil {
				return nil, status.Errorf(grpccodes.InvalidArgument, "validation error: %v", err)
			}
		} else if v, ok := req.(interface{ ValidateAll() error }); ok {
			if err := v.ValidateAll(); err != nil {
				return nil, status.Errorf(grpccodes.InvalidArgument, "validation error: %v", err)
			}
		}

		return handler(ctx, req)
	}
}

// DefaultUnaryServerInterceptors returns the standard recommended chain of unary server interceptors:
// Recovery -> RequestID -> Tracing -> Logging -> Validation.
func DefaultUnaryServerInterceptors(log *zap.Logger, tracerName string) []grpc.UnaryServerInterceptor {
	return []grpc.UnaryServerInterceptor{
		UnaryRecovery(log),
		UnaryRequestID(),
		UnaryTracing(tracerName),
		UnaryLogging(log),
		UnaryValidation(),
	}
}

// DefaultStreamServerInterceptors returns the standard recommended chain of stream server interceptors:
// Recovery -> RequestID -> Tracing -> Logging.
func DefaultStreamServerInterceptors(log *zap.Logger, tracerName string) []grpc.StreamServerInterceptor {
	return []grpc.StreamServerInterceptor{
		StreamRecovery(log),
		StreamRequestID(),
		StreamTracing(tracerName),
		StreamLogging(log),
	}
}

// Helper functions

type requestIDCtxKey struct{}

func extractOrGenerateRequestID(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		vals := md.Get("x-request-id")
		if len(vals) > 0 && strings.TrimSpace(vals[0]) != "" {
			return strings.TrimSpace(vals[0])
		}
	}
	return uuid.New().String()
}

func setRequestIDInContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDCtxKey{}, id)
}

// RequestIDFromContext extracts the request ID stored by the gRPC interceptors.
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDCtxKey{}).(string); ok && id != "" {
		return id
	}
	return middleware.RequestIDFromContext(ctx)
}

func extractPeerIP(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		if host, _, err := net.SplitHostPort(p.Addr.String()); err == nil {
			return host
		}
		return p.Addr.String()
	}
	return "unknown"
}

func splitMethodName(fullMethod string) (string, string) {
	full := strings.TrimPrefix(fullMethod, "/")
	parts := strings.SplitN(full, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return full, ""
}

func extractBearerToken(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(grpccodes.Unauthenticated, "missing metadata")
	}

	auths := md.Get("authorization")
	if len(auths) == 0 {
		return "", status.Error(grpccodes.Unauthenticated, "missing authorization header")
	}

	raw := strings.TrimSpace(auths[0])
	if !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		return "", status.Error(grpccodes.Unauthenticated, "invalid authorization scheme, expected Bearer")
	}

	token := strings.TrimSpace(raw[7:])
	if token == "" {
		return "", status.Error(grpccodes.Unauthenticated, "empty bearer token")
	}

	return token, nil
}
