package audit

import (
	"context"
)

type actorContextKey struct{}
type auditMetadataContextKey struct{}

// WithActor stores the current Actor in the context.
func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// ActorFromContext retrieves the Actor from the context, if set.
func ActorFromContext(ctx context.Context) (Actor, bool) {
	if a, ok := ctx.Value(actorContextKey{}).(Actor); ok {
		return a, true
	}
	return Actor{}, false
}

// WithMetadata attaches arbitrary key-value metadata to the context for inclusion in audit events.
func WithMetadata(ctx context.Context, key string, val any) context.Context {
	existing, ok := ctx.Value(auditMetadataContextKey{}).(map[string]any)
	newMap := make(map[string]any, len(existing)+1)
	if ok {
		for k, v := range existing {
			newMap[k] = v
		}
	}
	newMap[key] = val
	return context.WithValue(ctx, auditMetadataContextKey{}, newMap)
}

// MetadataFromContext retrieves any metadata stored in the context for audit logging.
func MetadataFromContext(ctx context.Context) map[string]any {
	if m, ok := ctx.Value(auditMetadataContextKey{}).(map[string]any); ok {
		res := make(map[string]any, len(m))
		for k, v := range m {
			res[k] = v
		}
		return res
	}
	return nil
}
