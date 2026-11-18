package workflow

import (
	"context"
	"time"
)

// State represents the current status of a workflow instance.
type State string

// Event represents a trigger causing a transition between states.
type Event string

// EventContext provides context data to guards, actions, and lifecycle hooks during an event transition.
type EventContext struct {
	InstanceID string
	FromState  State
	ToState    State
	Event      Event
	Payload    any
	Metadata   map[string]any
	Version    int64
	Timestamp  time.Time
}

// Guard evaluates whether a transition is permitted to execute given the context.
// Returns true to allow the transition, false to reject with ErrGuardFailed.
type Guard func(ctx context.Context, e *EventContext) bool

// Action executes custom business logic during a transition or lifecycle event.
type Action func(ctx context.Context, e *EventContext) error

// HistoryEntry records a single state transition in the audit log of an instance.
type HistoryEntry struct {
	FromState State         `json:"from_state"`
	ToState   State         `json:"to_state"`
	Event     Event         `json:"event"`
	Timestamp time.Time     `json:"timestamp"`
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
}

// Instance represents a stateful workflow execution.
type Instance struct {
	ID             string         `json:"id"`
	DefinitionName string         `json:"definition_name"`
	CurrentState   State          `json:"current_state"`
	Version        int64          `json:"version"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	History        []HistoryEntry `json:"history,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// Clone creates a deep copy of the Instance.
func (in *Instance) Clone() *Instance {
	if in == nil {
		return nil
	}

	clone := *in

	if in.Metadata != nil {
		clone.Metadata = make(map[string]any, len(in.Metadata))
		for k, v := range in.Metadata {
			clone.Metadata[k] = v
		}
	}

	if in.History != nil {
		clone.History = make([]HistoryEntry, len(in.History))
		copy(clone.History, in.History)
	}

	return &clone
}
