package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// StartSpan starts a new OpenTelemetry span with the given tracer name and span name.
func StartSpan(ctx context.Context, tracerName, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, spanName, opts...)
}

// SpanFromContext returns the current OpenTelemetry span from the context.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// RecordError records an error on the current span in context and sets the status to Error.
func RecordError(ctx context.Context, err error, opts ...trace.EventOption) {
	if err == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.RecordError(err, opts...)
		span.SetStatus(codes.Error, err.Error())
	}
}

// AddEvent adds an event with attributes to the current span in context.
func AddEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.AddEvent(name, trace.WithAttributes(attrs...))
	}
}

// SetAttributes sets one or more attributes on the current span in context.
func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(attrs...)
	}
}

// TraceID extracts the hexadecimal TraceID from the span context, or returns an empty string.
func TraceID(ctx context.Context) string {
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		return spanCtx.TraceID().String()
	}
	return ""
}

// SpanID extracts the hexadecimal SpanID from the span context, or returns an empty string.
func SpanID(ctx context.Context) string {
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		return spanCtx.SpanID().String()
	}
	return ""
}
