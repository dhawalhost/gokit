// Package workflow provides a finite-state machine (FSM) and workflow orchestration engine
// with state transitions, conditional guards, lifecycle hooks, audit history, and optimistic concurrency control.
package workflow

import (
	"errors"
)

var (
	// ErrInvalidTransition is returned when an event is fired that has no valid transition from the current state.
	ErrInvalidTransition = errors.New("workflow: invalid state transition")
	// ErrGuardFailed is returned when a transition's guard condition rejects the event.
	ErrGuardFailed = errors.New("workflow: transition guard condition rejected event")
	// ErrInstanceNotFound is returned when operating on a workflow instance that does not exist in the store.
	ErrInstanceNotFound = errors.New("workflow: instance not found")
	// ErrDefinitionNotFound is returned when attempting to create an instance for an unregistered workflow definition.
	ErrDefinitionNotFound = errors.New("workflow: definition not found")
	// ErrOptimisticLockConflict is returned when another process has modified the instance concurrently.
	ErrOptimisticLockConflict = errors.New("workflow: optimistic lock conflict, version mismatch")
	// ErrInvalidDefinition is returned when building a workflow with conflicting, orphan, or missing states.
	ErrInvalidDefinition = errors.New("workflow: invalid workflow definition")
)
