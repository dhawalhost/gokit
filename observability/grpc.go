package observability

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// splitMethod splits a full gRPC method path (e.g. "/users.UserService/GetUser") into service and method names.
func splitMethod(fullMethod string) (string, string) {
	fullMethod = strings.TrimPrefix(fullMethod, "/")
	parts := strings.Split(fullMethod, "/")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "unknown", fullMethod
}

// UnaryServerMetrics returns a grpc.UnaryServerInterceptor that records Prometheus metrics for inbound RPCs.
func UnaryServerMetrics(r *Registry) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if r == nil {
			return handler(ctx, req)
		}

		service, method := splitMethod(info.FullMethod)
		start := time.Now()

		resp, err := handler(ctx, req)
		duration := time.Since(start).Seconds()

		code := status.Code(err)
		r.grpcServerTotal.WithLabelValues(service, method, code.String()).Inc()
		r.grpcServerDuration.WithLabelValues(service, method, code.String()).Observe(duration)

		return resp, err
	}
}

// StreamServerMetrics returns a grpc.StreamServerInterceptor that records Prometheus metrics for streaming RPCs.
func StreamServerMetrics(r *Registry) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if r == nil {
			return handler(srv, ss)
		}

		service, method := splitMethod(info.FullMethod)
		start := time.Now()

		err := handler(srv, ss)
		duration := time.Since(start).Seconds()

		code := status.Code(err)
		r.grpcServerTotal.WithLabelValues(service, method, code.String()).Inc()
		r.grpcServerDuration.WithLabelValues(service, method, code.String()).Observe(duration)

		return err
	}
}

// UnaryClientMetrics returns a grpc.UnaryClientInterceptor that records Prometheus metrics for outbound client RPCs.
func UnaryClientMetrics(r *Registry) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if r == nil {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		serviceName, methodName := splitMethod(method)
		start := time.Now()

		err := invoker(ctx, method, req, reply, cc, opts...)
		duration := time.Since(start).Seconds()

		code := status.Code(err)
		r.grpcClientTotal.WithLabelValues(serviceName, methodName, code.String()).Inc()
		r.grpcClientDuration.WithLabelValues(serviceName, methodName, code.String()).Observe(duration)

		return err
	}
}

// StreamClientMetrics returns a grpc.StreamClientInterceptor that records Prometheus metrics for outbound client streaming RPCs.
func StreamClientMetrics(r *Registry) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if r == nil {
			return streamer(ctx, desc, cc, method, opts...)
		}

		serviceName, methodName := splitMethod(method)
		start := time.Now()

		cs, err := streamer(ctx, desc, cc, method, opts...)
		duration := time.Since(start).Seconds()

		code := status.Code(err)
		r.grpcClientTotal.WithLabelValues(serviceName, methodName, code.String()).Inc()
		r.grpcClientDuration.WithLabelValues(serviceName, methodName, code.String()).Observe(duration)

		return cs, err
	}
}
