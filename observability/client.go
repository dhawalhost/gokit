package observability

import (
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TraceRoundTripper wraps an http.RoundTripper to inject OpenTelemetry trace headers
// and record client span telemetry for outbound HTTP requests.
type TraceRoundTripper struct {
	Base       http.RoundTripper
	TracerName string
}

// RoundTrip executes the HTTP request with tracing and context propagation.
func (t *TraceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	tracerName := t.TracerName
	if tracerName == "" {
		tracerName = "gokit/http-client"
	}

	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(
		req.Context(),
		fmt.Sprintf("HTTP %s", req.Method),
		trace.WithSpanKind(trace.SpanKindClient),
	)
	defer span.End()

	span.SetAttributes(
		attribute.String("http.method", req.Method),
		attribute.String("http.url", req.URL.String()),
		attribute.String("http.host", req.URL.Host),
	)

	// Inject W3C TraceContext and Baggage headers into outbound request
	clonedReq := req.Clone(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(clonedReq.Header))

	resp, err := base.RoundTrip(clonedReq)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}

	return resp, nil
}

// WrapHTTPClient wraps the transport of an existing *http.Client to inject OpenTelemetry trace propagation.
func WrapHTTPClient(client *http.Client, tracerName ...string) *http.Client {
	if client == nil {
		client = &http.Client{}
	}

	name := "gokit/http-client"
	if len(tracerName) > 0 && tracerName[0] != "" {
		name = tracerName[0]
	}

	cloned := *client
	cloned.Transport = &TraceRoundTripper{
		Base:       client.Transport,
		TracerName: name,
	}
	return &cloned
}

// NewInstrumentedHTTPClient creates an *http.Client configured with tracing and a default timeout.
func NewInstrumentedHTTPClient(timeout time.Duration, tracerName ...string) *http.Client {
	client := &http.Client{
		Timeout: timeout,
	}
	return WrapHTTPClient(client, tracerName...)
}
