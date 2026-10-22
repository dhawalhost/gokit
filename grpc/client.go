package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/dhawalhost/gokit/circuitbreaker"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// RetryConfig configures client retry behavior for transient gRPC errors.
type RetryConfig struct {
	// MaxAttempts is the total number of attempts (including the first attempt). Default: 3.
	MaxAttempts int
	// InitialBackoff is the starting backoff duration. Default: 50ms.
	InitialBackoff time.Duration
	// MaxBackoff is the maximum backoff duration cap. Default: 500ms.
	MaxBackoff time.Duration
	// BackoffMultiplier is the factor by which backoff increases each attempt. Default: 2.0.
	BackoffMultiplier float64
	// RetryableCodes specifies which gRPC error codes trigger a retry. Default: Unavailable, ResourceExhausted.
	RetryableCodes []grpccodes.Code
}

// ClientConfig configures a gRPC client connection.
type ClientConfig struct {
	// Target is the connection target (e.g. "localhost:9090" or "dns:///service:9090").
	Target string
	// Insecure disables TLS when true.
	Insecure bool
	// TLSConfig holds optional custom TLS configuration.
	TLSConfig *tls.Config
	// Timeout sets a default per-RPC timeout if the caller's context does not have a deadline.
	Timeout time.Duration
	// MaxMsgSize sets the max message receive and send size in bytes (default: 16MB).
	MaxMsgSize int
	// Keepalive sets client connection keepalive ping options.
	Keepalive *keepalive.ClientParameters
	// Retry configures client-side retries with exponential backoff and jitter.
	Retry *RetryConfig
	// CircuitBreaker guards the client against cascading failures.
	CircuitBreaker *circuitbreaker.CircuitBreaker
	// UnaryInterceptors is a list of unary client interceptors to chain.
	UnaryInterceptors []grpc.UnaryClientInterceptor
	// StreamInterceptors is a list of stream client interceptors to chain.
	StreamInterceptors []grpc.StreamClientInterceptor
}

// Dial creates a resilient gRPC client connection with production tuning and interceptors.
func Dial(ctx context.Context, cfg ClientConfig, extraOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
	if strings.TrimSpace(cfg.Target) == "" {
		return nil, fmt.Errorf("grpc: client target cannot be empty")
	}

	opts := make([]grpc.DialOption, 0, len(extraOpts)+8)

	// Transport credentials
	if cfg.Insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else if cfg.TLSConfig != nil {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(cfg.TLSConfig)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
		})))
	}

	maxMsg := cfg.MaxMsgSize
	if maxMsg <= 0 {
		maxMsg = 16 * 1024 * 1024 // 16MB
	}
	opts = append(opts, grpc.WithDefaultCallOptions(
		grpc.MaxCallRecvMsgSize(maxMsg),
		grpc.MaxCallSendMsgSize(maxMsg),
	))

	if cfg.Keepalive != nil {
		opts = append(opts, grpc.WithKeepaliveParams(*cfg.Keepalive))
	} else {
		opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}))
	}

	// Assemble unary interceptors
	unaryInterceptors := make([]grpc.UnaryClientInterceptor, 0, len(cfg.UnaryInterceptors)+3)
	if cfg.CircuitBreaker != nil {
		unaryInterceptors = append(unaryInterceptors, UnaryClientCircuitBreaker(cfg.CircuitBreaker))
	}
	if cfg.Retry != nil {
		unaryInterceptors = append(unaryInterceptors, UnaryClientRetry(*cfg.Retry))
	}
	unaryInterceptors = append(unaryInterceptors, cfg.UnaryInterceptors...)

	if len(unaryInterceptors) > 0 {
		opts = append(opts, grpc.WithChainUnaryInterceptor(unaryInterceptors...))
	}
	if len(cfg.StreamInterceptors) > 0 {
		opts = append(opts, grpc.WithChainStreamInterceptor(cfg.StreamInterceptors...))
	}

	opts = append(opts, extraOpts...)

	conn, err := grpc.NewClient(cfg.Target, opts...)
	if err != nil {
		return nil, fmt.Errorf("grpc: dial %s: %w", cfg.Target, err)
	}

	return conn, nil
}

// UnaryClientRequestID injects the X-Request-ID into the outgoing metadata.
func UnaryClientRequestID() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		reqID := RequestIDFromContext(ctx)
		if reqID == "" {
			reqID = uuid.New().String()
		}

		ctx = metadata.AppendToOutgoingContext(ctx, "x-request-id", reqID)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnaryClientTracing traces client RPC invocations and injects trace context into outgoing metadata.
func UnaryClientTracing(tracerName string) grpc.UnaryClientInterceptor {
	if tracerName == "" {
		tracerName = "gokit/grpc"
	}
	tr := otel.GetTracerProvider().Tracer(tracerName)

	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		service, meth := splitMethodName(method)

		ctx, span := tr.Start(ctx, method,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				semconv.RPCSystemKey.String("grpc"),
				semconv.RPCServiceKey.String(service),
				semconv.RPCMethodKey.String(meth),
			),
		)
		defer span.End()

		ctx = injectTraceContext(ctx)

		err := invoker(ctx, method, req, reply, cc, opts...)
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

// UnaryClientCircuitBreaker wraps RPC invocations in a circuit breaker.
func UnaryClientCircuitBreaker(cb *circuitbreaker.CircuitBreaker) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if cb == nil {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		var invokerErr error
		err := cb.Execute(func() error {
			invokerErr = invoker(ctx, method, req, reply, cc, opts...)
			// Consider 5xx-like transient server errors as failures for the circuit breaker
			st := ToStatus(invokerErr)
			switch st.Code() {
			case grpccodes.Unavailable, grpccodes.Internal, grpccodes.DeadlineExceeded:
				return invokerErr
			default:
				// Client-side mistakes (InvalidArgument, NotFound, etc.) do NOT trip the circuit
				return nil
			}
		})

		if err != nil && invokerErr == nil {
			// Circuit breaker tripped or open
			return status.Error(grpccodes.Unavailable, err.Error())
		}

		return invokerErr
	}
}

// UnaryClientRetry executes retries with exponential backoff and jitter for transient errors.
func UnaryClientRetry(cfg RetryConfig) grpc.UnaryClientInterceptor {
	if cfg.MaxAttempts <= 1 {
		cfg.MaxAttempts = 3
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 50 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 500 * time.Millisecond
	}
	if cfg.BackoffMultiplier <= 1.0 {
		cfg.BackoffMultiplier = 2.0
	}
	if len(cfg.RetryableCodes) == 0 {
		cfg.RetryableCodes = []grpccodes.Code{grpccodes.Unavailable, grpccodes.ResourceExhausted}
	}

	retryMap := make(map[grpccodes.Code]bool, len(cfg.RetryableCodes))
	for _, c := range cfg.RetryableCodes {
		retryMap[c] = true
	}

	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var lastErr error
		backoff := cfg.InitialBackoff

		for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return err
			}

			lastErr = invoker(ctx, method, req, reply, cc, opts...)
			if lastErr == nil {
				return nil
			}

			st := ToStatus(lastErr)
			if !retryMap[st.Code()] || attempt == cfg.MaxAttempts {
				return lastErr
			}

			// Calculate exponential backoff with full jitter
			jitter := time.Duration(rand.Int63n(int64(backoff) + 1)) // #nosec G404 -- pseudo-random jitter is sufficient
			timer := time.NewTimer(jitter)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}

			backoff = time.Duration(float64(backoff) * cfg.BackoffMultiplier)
			if backoff > cfg.MaxBackoff {
				backoff = cfg.MaxBackoff
			}
		}

		return lastErr
	}
}

// UnaryClientLogging logs outbound client RPC invocations.
func UnaryClientLogging(log *zap.Logger) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if log == nil {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		start := time.Now()
		err := invoker(ctx, method, req, reply, cc, opts...)
		duration := time.Since(start)

		st := ToStatus(err)
		code := st.Code()
		reqID := RequestIDFromContext(ctx)

		fields := []zap.Field{
			zap.String("grpc.method", method),
			zap.String("grpc.target", cc.Target()),
			zap.String("grpc.code", code.String()),
			zap.Duration("duration_ms", duration),
		}
		if reqID != "" {
			fields = append(fields, zap.String("request_id", reqID))
		}
		if err != nil && code != grpccodes.OK {
			fields = append(fields, zap.Error(err))
			log.Warn("grpc client call completed with error", fields...)
		} else {
			log.Info("grpc client call completed", fields...)
		}

		return err
	}
}

// DefaultUnaryClientInterceptors returns the standard recommended chain of unary client interceptors:
// RequestID -> Tracing -> Logging.
func DefaultUnaryClientInterceptors(log *zap.Logger, tracerName string) []grpc.UnaryClientInterceptor {
	return []grpc.UnaryClientInterceptor{
		UnaryClientRequestID(),
		UnaryClientTracing(tracerName),
		UnaryClientLogging(log),
	}
}
