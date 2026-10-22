package grpc

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc/metadata"
)

func init() {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}

// metadataCarrier adapts gRPC metadata.MD to OpenTelemetry propagation.TextMapCarrier.
type metadataCarrier metadata.MD

// Get returns the first value associated with the passed key.
func (c metadataCarrier) Get(key string) string {
	values := metadata.MD(c).Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Set stores the key-value pair.
func (c metadataCarrier) Set(key, val string) {
	metadata.MD(c).Set(key, val)
}

// Keys lists the keys stored in this carrier.
func (c metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// extractTraceContext extracts OpenTelemetry trace context from incoming gRPC metadata.
func extractTraceContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	propagator := otel.GetTextMapPropagator()
	return propagator.Extract(ctx, metadataCarrier(md))
}

// injectTraceContext injects OpenTelemetry trace context into outgoing gRPC metadata.
func injectTraceContext(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	} else {
		md = md.Copy()
	}
	propagator := otel.GetTextMapPropagator()
	propagator.Inject(ctx, metadataCarrier(md))
	return metadata.NewOutgoingContext(ctx, md)
}
