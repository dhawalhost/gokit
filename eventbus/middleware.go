package eventbus

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Middleware is a function that wraps a HandlerFunc.
type Middleware func(next HandlerFunc) HandlerFunc

// Chain combines multiple middlewares into a single Middleware applied from outer to inner.
func Chain(middlewares ...Middleware) Middleware {
	return func(final HandlerFunc) HandlerFunc {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}

// RecoveryMiddleware catches panics in event handlers and converts them into errors.
func RecoveryMiddleware() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, msg *Message) (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("eventbus: panic in handler for topic %q: %v\nstack: %s", msg.Topic, r, string(debug.Stack()))
				}
			}()
			return next(ctx, msg)
		}
	}
}

// RetryMiddleware retries handler execution up to maxRetries with backoff if an error occurs.
func RetryMiddleware(maxRetries int, backoff time.Duration) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, msg *Message) error {
			var err error
			for attempt := 0; attempt <= maxRetries; attempt++ {
				if attempt > 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(backoff):
					}
				}

				err = next(ctx, msg)
				if err == nil {
					return nil
				}
			}
			return fmt.Errorf("eventbus: failed after %d retries: %w", maxRetries, err)
		}
	}
}

// DeadLetterMiddleware routes messages that fail handler execution to a Dead Letter Queue (DLQ).
func DeadLetterMiddleware(dlqPublisher Publisher, dlqTopic string) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, msg *Message) error {
			err := next(ctx, msg)
			if err != nil && dlqPublisher != nil {
				dlqMsg := NewMessage(dlqTopic, msg.Payload).
					WithID(msg.ID).
					WithKey(msg.Key).
					WithHeaders(msg.Headers).
					WithHeader("x-death-reason", err.Error()).
					WithHeader("x-original-topic", msg.Topic).
					WithHeader("x-death-timestamp", time.Now().UTC().Format(time.RFC3339))

				_ = dlqPublisher.Publish(ctx, dlqMsg)
			}
			return err
		}
	}
}

// HeaderCarrier adapts message headers map for OpenTelemetry propagator.
type HeaderCarrier map[string]string

// Get returns the value associated with the passed key.
func (c HeaderCarrier) Get(key string) string {
	return c[key]
}

// Set stores the key-value pair.
func (c HeaderCarrier) Set(key, value string) {
	c[key] = value
}

// Keys lists the keys stored in this carrier.
func (c HeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// TracingMiddleware extracts OpenTelemetry trace context from message headers and starts a consumer span.
func TracingMiddleware(tracer trace.Tracer) Middleware {
	if tracer == nil {
		tracer = otel.GetTracerProvider().Tracer("gokit/eventbus")
	}
	propagator := otel.GetTextMapPropagator()

	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, msg *Message) error {
			// Extract trace context from message headers
			carrier := HeaderCarrier(msg.Headers)
			ctx = propagator.Extract(ctx, carrier)

			ctx, span := tracer.Start(ctx, fmt.Sprintf("eventbus.consume %s", msg.Topic),
				trace.WithSpanKind(trace.SpanKindConsumer),
			)
			defer span.End()

			err := next(ctx, msg)
			if err != nil {
				span.RecordError(err)
			}
			return err
		}
	}
}

// InjectTracing injects current span context from context.Context into message headers.
func InjectTracing(ctx context.Context, msg *Message) {
	if msg.Headers == nil {
		msg.Headers = make(map[string]string)
	}
	propagator := otel.GetTextMapPropagator()
	propagator.Inject(ctx, HeaderCarrier(msg.Headers))
}
