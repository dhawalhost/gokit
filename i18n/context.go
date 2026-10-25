package i18n

import (
	"context"
)

type contextKey struct{}

var localizerKey = contextKey{}

// WithLocalizer stores the given Localizer inside ctx.
func WithLocalizer(ctx context.Context, l *Localizer) context.Context {
	return context.WithValue(ctx, localizerKey, l)
}

// FromContext extracts the Localizer from ctx. Returns nil if none is set.
func FromContext(ctx context.Context) *Localizer {
	if ctx == nil {
		return nil
	}
	val := ctx.Value(localizerKey)
	if l, ok := val.(*Localizer); ok {
		return l
	}
	return nil
}

// T translates key using the Localizer attached to ctx, or returns key if none is present.
func T(ctx context.Context, key string, args ...any) string {
	l := FromContext(ctx)
	if l != nil {
		return l.T(key, args...)
	}
	return key
}

// Plural translates a plural message using the Localizer attached to ctx, or returns key if none is present.
func Plural(ctx context.Context, key string, count int, args ...any) string {
	l := FromContext(ctx)
	if l != nil {
		return l.Plural(key, count, args...)
	}
	return key
}
