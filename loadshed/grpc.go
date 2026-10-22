package loadshed

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GRPCPriorityExtractor extracts a Priority level from gRPC incoming metadata or full method.
type GRPCPriorityExtractor func(ctx context.Context, fullMethod string) Priority

// DefaultGRPCPriorityExtractor inspects metadata headers ("x-priority") and method name.
func DefaultGRPCPriorityExtractor(ctx context.Context, fullMethod string) Priority {
	if strings.Contains(strings.ToLower(fullMethod), "health") {
		return PriorityCritical
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return PriorityStandard
	}

	vals := md.Get("x-priority")
	if len(vals) == 0 {
		return PriorityStandard
	}

	switch strings.ToLower(vals[0]) {
	case "critical", "high":
		return PriorityCritical
	case "low", "batch", "background":
		return PriorityLow
	default:
		return PriorityStandard
	}
}

// GRPCOptions holds configuration for gRPC interceptors.
type GRPCOptions struct {
	Extractor GRPCPriorityExtractor
	ShedCode  codes.Code
}

// GRPCOption configures gRPC interceptor options.
type GRPCOption func(*GRPCOptions)

// WithGRPCPriorityExtractor sets a custom priority extractor for gRPC calls.
func WithGRPCPriorityExtractor(fn GRPCPriorityExtractor) GRPCOption {
	return func(o *GRPCOptions) {
		if fn != nil {
			o.Extractor = fn
		}
	}
}

// WithGRPCShedCode sets the status code returned when an RPC is shed (default: codes.ResourceExhausted).
func WithGRPCShedCode(code codes.Code) GRPCOption {
	return func(o *GRPCOptions) {
		o.ShedCode = code
	}
}

// UnaryServerInterceptor returns a gRPC server interceptor that protects RPC handlers from latency collapse.
func UnaryServerInterceptor(limiter *Limiter, opts ...GRPCOption) grpc.UnaryServerInterceptor {
	cfg := GRPCOptions{
		Extractor: DefaultGRPCPriorityExtractor,
		ShedCode:  codes.ResourceExhausted,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		priority := cfg.Extractor(ctx, info.FullMethod)

		done, err := limiter.Acquire(ctx, priority)
		if err != nil {
			return nil, status.Errorf(cfg.ShedCode, "loadshed: system overloaded, rpc shed")
		}

		resp, err := handler(ctx, req)
		done(err)
		return resp, err
	}
}

// StreamServerInterceptor returns a gRPC stream interceptor that bounds concurrent stream handshakes.
func StreamServerInterceptor(limiter *Limiter, opts ...GRPCOption) grpc.StreamServerInterceptor {
	cfg := GRPCOptions{
		Extractor: DefaultGRPCPriorityExtractor,
		ShedCode:  codes.ResourceExhausted,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		priority := cfg.Extractor(ctx, info.FullMethod)

		done, err := limiter.Acquire(ctx, priority)
		if err != nil {
			return status.Errorf(cfg.ShedCode, "loadshed: system overloaded, stream shed")
		}

		err = handler(srv, ss)
		done(err)
		return err
	}
}
