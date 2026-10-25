package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/dhawalhost/gokit/config"
)

// TracerOption configures TracerProvider initialization.
type TracerOption func(*tracerConfig)

type tracerConfig struct {
	sampler    sdktrace.Sampler
	propagator propagation.TextMapPropagator
}

// WithSampler configures the trace sampling strategy (e.g. sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1))).
func WithSampler(sampler sdktrace.Sampler) TracerOption {
	return func(c *tracerConfig) {
		if sampler != nil {
			c.sampler = sampler
		}
	}
}

// WithPropagator configures a custom TextMapPropagator.
func WithPropagator(prop propagation.TextMapPropagator) TracerOption {
	return func(c *tracerConfig) {
		if prop != nil {
			c.propagator = prop
		}
	}
}

// InitTracer sets up an OTLP HTTP trace exporter and installs a global TracerProvider
// along with W3C TraceContext and Baggage propagators.
// The returned function shuts down the provider gracefully.
func InitTracer(ctx context.Context, cfg config.TelemetryConfig, opts ...TracerOption) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return func(_ context.Context) error { return nil }, nil
	}

	tc := tracerConfig{
		sampler: sdktrace.AlwaysSample(),
		propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	}
	for _, opt := range opts {
		opt(&tc)
	}

	otel.SetTextMapPropagator(tc.propagator)

	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: otlp exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceNameKey.String(cfg.ServiceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(tc.sampler),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}
