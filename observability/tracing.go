package observability

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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
	sampler        sdktrace.Sampler
	propagator     propagation.TextMapPropagator
	insecure       bool
	serviceVersion string
	environment    string
}

// WithSampler configures the trace sampling strategy (defaults to ParentBased(TraceIDRatioBased(0.1))).
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

// WithInsecure allows plaintext HTTP transport to the OTLP collector instead of TLS.
// By default, TLS is enabled unless explicitly opted out or an http:// URL is configured.
func WithInsecure() TracerOption {
	return func(c *tracerConfig) {
		c.insecure = true
	}
}

// WithServiceVersion sets the service.version resource attribute.
func WithServiceVersion(version string) TracerOption {
	return func(c *tracerConfig) {
		if version != "" {
			c.serviceVersion = version
		}
	}
}

// WithDeploymentEnvironment sets the deployment.environment resource attribute.
func WithDeploymentEnvironment(env string) TracerOption {
	return func(c *tracerConfig) {
		if env != "" {
			c.environment = env
		}
	}
}

// InitTracer sets up an OTLP HTTP trace exporter and installs a global TracerProvider
// along with W3C TraceContext and Baggage propagators.
// By default it uses TLS transport, ParentBased(TraceIDRatioBased(0.1)) sampling,
// and standard OTEL_* environment variables.
// The returned function shuts down the provider gracefully.
func InitTracer(ctx context.Context, cfg config.TelemetryConfig, opts ...TracerOption) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return func(_ context.Context) error { return nil }, nil
	}

	tc := tracerConfig{
		sampler: sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1)),
		propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
		insecure:       cfg.Insecure,
		serviceVersion: cfg.ServiceVersion,
		environment:    cfg.Environment,
	}
	for _, opt := range opts {
		opt(&tc)
	}

	otel.SetTextMapPropagator(tc.propagator)

	var expOpts []otlptracehttp.Option
	if cfg.OTLPEndpoint != "" {
		if strings.HasPrefix(cfg.OTLPEndpoint, "http://") || strings.HasPrefix(cfg.OTLPEndpoint, "https://") {
			expOpts = append(expOpts, otlptracehttp.WithEndpointURL(cfg.OTLPEndpoint))
		} else {
			expOpts = append(expOpts, otlptracehttp.WithEndpoint(cfg.OTLPEndpoint))
		}
	}
	if tc.insecure {
		expOpts = append(expOpts, otlptracehttp.WithInsecure())
	}

	exp, err := otlptracehttp.New(ctx, expOpts...)
	if err != nil {
		return nil, fmt.Errorf("observability: otlp exporter: %w", err)
	}

	var attrs []attribute.KeyValue
	if cfg.ServiceName != "" {
		attrs = append(attrs, semconv.ServiceNameKey.String(cfg.ServiceName))
	}
	if tc.serviceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersionKey.String(tc.serviceVersion))
	}
	if tc.environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentKey.String(tc.environment))
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithHost(),
		resource.WithAttributes(attrs...),
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
