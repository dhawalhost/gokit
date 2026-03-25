package logger

import (
	"context"

	"go.uber.org/zap"
)

type contextKey struct{}

// FromContext retrieves the logger stored in ctx, falling back to the global logger.
func FromContext(ctx context.Context) *zap.Logger {
	if l, ok := ctx.Value(contextKey{}).(*zap.Logger); ok && l != nil {
		return l
	}
	return Global()
}

// WithContext returns a new context that carries the given logger.
func WithContext(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}
