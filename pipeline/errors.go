package pipeline

import (
	"errors"
	"fmt"
)

var (
	// ErrPipelineClosed is returned when an operation is attempted on a closed or terminated pipeline.
	ErrPipelineClosed = errors.New("pipeline is closed")

	// ErrStagePanicked indicates that a stage worker encountered an unrecovered runtime panic.
	ErrStagePanicked = errors.New("pipeline stage worker panicked")

	// ErrBufferFull is returned when non-blocking enqueue fails due to full buffer capacity.
	ErrBufferFull = errors.New("pipeline stage buffer is full")

	// ErrDeadLetterQueueFull indicates that the dead-letter queue buffer capacity has been reached.
	ErrDeadLetterQueueFull = errors.New("dead-letter queue capacity exceeded")

	// ErrInvalidConfiguration indicates an illegal or contradictory pipeline configuration.
	ErrInvalidConfiguration = errors.New("invalid pipeline configuration")
)

// StageError represents a runtime processing error produced within an identified pipeline stage.
type StageError struct {
	StageName string
	Item      any
	Err       error
}

// Error returns a formatted error string containing stage name and root cause.
func (e *StageError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.StageName != "" {
		return fmt.Sprintf("pipeline stage %q failed: %v", e.StageName, e.Err)
	}
	return fmt.Sprintf("pipeline stage failed: %v", e.Err)
}

// Unwrap returns the underlying wrapped error.
func (e *StageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
