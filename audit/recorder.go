package audit

import (
	"context"
	"time"

	"github.com/dhawalhost/gokit/idgen"
	"github.com/dhawalhost/gokit/middleware"
)

// Recorder coordinates audit event enrichment and emission to a Sink.
type Recorder struct {
	sink Sink
}

// NewRecorder constructs a Recorder with the given Sink.
func NewRecorder(sink Sink) *Recorder {
	return &Recorder{sink: sink}
}

// Record validates, enriches, and writes an audit event.
func (r *Recorder) Record(ctx context.Context, event *Event) error {
	if event == nil {
		return nil
	}

	// 1. Assign ID if missing (time-ordered UUIDv7)
	if event.ID == "" {
		event.ID = idgen.NewUUIDv7()
	}

	// 2. Assign Timestamp if zero
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	// 3. Enrich TenantID from context if missing
	if event.TenantID == "" {
		if tid, ok := middleware.TenantIDFromContext(ctx); ok {
			event.TenantID = tid
		}
	}

	// 4. Enrich Actor from context if actor ID is empty
	if event.Actor.ID == "" {
		if actor, ok := ActorFromContext(ctx); ok {
			event.Actor = actor
		} else if claims, ok := middleware.ClaimsFromContext(ctx); ok {
			// Extract standard subject from JWT claims
			if sub, ok := claims["sub"].(string); ok && sub != "" {
				event.Actor.ID = sub
				event.Actor.Type = "user"
			}
			if email, ok := claims["email"].(string); ok {
				event.Actor.Email = email
			}
		}
	}

	// Default actor type to "system" if still unset
	if event.Actor.Type == "" && event.Actor.ID != "" {
		event.Actor.Type = "user"
	} else if event.Actor.ID == "" {
		event.Actor.ID = "system"
		event.Actor.Type = "system"
	}

	// 5. Default status to Success if not specified
	if event.Status == "" {
		event.Status = StatusSuccess
	}

	// 6. Enrich Metadata with context items
	if event.Metadata == nil {
		event.Metadata = make(map[string]any)
	}

	// Merge context metadata
	if ctxMeta := MetadataFromContext(ctx); len(ctxMeta) > 0 {
		for k, v := range ctxMeta {
			if _, exists := event.Metadata[k]; !exists {
				event.Metadata[k] = v
			}
		}
	}

	// Add request_id from middleware if present and not already specified
	if _, exists := event.Metadata["request_id"]; !exists {
		if reqID := middleware.RequestIDFromContext(ctx); reqID != "" {
			event.Metadata["request_id"] = reqID
		}
	}

	return r.sink.Write(ctx, event)
}

// Close closes the underlying sink.
func (r *Recorder) Close() error {
	if r.sink != nil {
		return r.sink.Close()
	}
	return nil
}

// EventBuilder provides a fluent builder pattern for constructing and recording audit events.
type EventBuilder struct {
	recorder *Recorder
	event    *Event
}

// NewEvent starts building an audit event with the specified action name.
func (r *Recorder) NewEvent(action string) *EventBuilder {
	return &EventBuilder{
		recorder: r,
		event: &Event{
			Action:   action,
			Status:   StatusSuccess,
			Metadata: make(map[string]any),
		},
	}
}

// WithActor sets the full Actor object.
func (b *EventBuilder) WithActor(actor Actor) *EventBuilder {
	b.event.Actor = actor
	return b
}

// WithActorUser sets a user actor with basic attributes.
func (b *EventBuilder) WithActorUser(id, email, name, role string) *EventBuilder {
	b.event.Actor = Actor{
		ID:    id,
		Type:  "user",
		Email: email,
		Name:  name,
		Role:  role,
	}
	return b
}

// WithResource sets the target resource details.
func (b *EventBuilder) WithResource(resType, id string, name ...string) *EventBuilder {
	resName := ""
	if len(name) > 0 {
		resName = name[0]
	}
	b.event.Resource = Resource{
		Type: resType,
		ID:   id,
		Name: resName,
	}
	return b
}

// WithTenant sets the tenant ID.
func (b *EventBuilder) WithTenant(tenantID string) *EventBuilder {
	b.event.TenantID = tenantID
	return b
}

// WithStatus sets the outcome status.
func (b *EventBuilder) WithStatus(status Status) *EventBuilder {
	b.event.Status = status
	return b
}

// WithReason sets the reason or error explanation.
func (b *EventBuilder) WithReason(reason string) *EventBuilder {
	b.event.Reason = reason
	return b
}

// WithChange records a before-and-after field modification.
func (b *EventBuilder) WithChange(field string, oldVal, newVal any) *EventBuilder {
	b.event.Changes = append(b.event.Changes, FieldChange{
		Field:    field,
		OldValue: oldVal,
		NewValue: newVal,
	})
	return b
}

// WithMetadata sets an arbitrary metadata key-value pair.
func (b *EventBuilder) WithMetadata(key string, val any) *EventBuilder {
	b.event.Metadata[key] = val
	return b
}

// Record builds and records the audit event via the parent Recorder.
func (b *EventBuilder) Record(ctx context.Context) error {
	return b.recorder.Record(ctx, b.event)
}
