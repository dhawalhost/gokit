package saga

import (
	"context"
	"time"
)

// StepFunc defines a forward action or compensating rollback action in a Saga.
type StepFunc func(ctx context.Context) error

// Step defines a single distributed unit of work with forward execution and backward compensation.
type Step struct {
	Name       string
	Execute    StepFunc
	Compensate StepFunc
}

// StepStatus tracks the outcome of an individual Saga step.
type StepStatus string

// Saga step lifecycle statuses.
const (
	StepStatusPending     StepStatus = "pending"
	StepStatusSucceeded   StepStatus = "succeeded"
	StepStatusFailed      StepStatus = "failed"
	StepStatusCompensated StepStatus = "compensated"
	StepStatusCompFailed  StepStatus = "compensation_failed"
)

// StepResult contains execution metadata for an executed step.
type StepResult struct {
	StepName  string        `json:"step_name"`
	Status    StepStatus    `json:"status"`
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
	CompError string        `json:"compensation_error,omitempty"`
}

// NewStep constructs a step with forward execution and optional compensation.
func NewStep(name string, execute StepFunc, compensate StepFunc) Step {
	return Step{
		Name:       name,
		Execute:    execute,
		Compensate: compensate,
	}
}
