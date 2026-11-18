package saga

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// State represents the overall lifecycle state of a Saga execution.
type State string

// Saga lifecycle states.
const (
	StatePending      State = "pending"
	StateRunning      State = "running"
	StateCompleted    State = "completed"
	StateCompensating State = "compensating"
	StateCompensated  State = "compensated"
	StateFailed       State = "failed"
)

// ExecutionResult captures the overall outcome and step logs of a Saga run.
type ExecutionResult struct {
	SagaID      string        `json:"saga_id"`
	SagaName    string        `json:"saga_name"`
	State       State         `json:"state"`
	Duration    time.Duration `json:"duration"`
	StepResults []StepResult  `json:"step_results"`
	FailedStep  string        `json:"failed_step,omitempty"`
	Error       error         `json:"error,omitempty"`
}

// Saga coordinates multi-step distributed transactions with automated reverse compensations.
type Saga struct {
	id    string
	name  string
	steps []Step
}

// New constructs a new Saga orchestrator.
func New(name string) *Saga {
	return &Saga{
		id:    uuid.NewString(),
		name:  name,
		steps: make([]Step, 0),
	}
}

// AddStep appends a forward action and its compensating rollback step.
func (s *Saga) AddStep(step Step) *Saga {
	s.steps = append(s.steps, step)
	return s
}

// Execute runs the Saga steps in sequence. If any step fails, prior successful
// steps are compensated in reverse topological order.
func (s *Saga) Execute(ctx context.Context) (*ExecutionResult, error) {
	result := &ExecutionResult{
		SagaID:      s.id,
		SagaName:    s.name,
		State:       StateRunning,
		StepResults: make([]StepResult, 0, len(s.steps)),
	}

	start := time.Now()
	var executedSteps []int

	// 1. Run forward steps
	for i, step := range s.steps {
		stepStart := time.Now()
		err := step.Execute(ctx)
		duration := time.Since(stepStart)

		if err != nil {
			result.FailedStep = step.Name
			result.Error = fmt.Errorf("saga %q: step %q failed: %w", s.name, step.Name, err)
			result.StepResults = append(result.StepResults, StepResult{
				StepName: step.Name,
				Status:   StepStatusFailed,
				Duration: duration,
				Error:    err.Error(),
			})

			// 2. Trigger reverse compensations
			s.compensate(executedSteps, result)
			result.Duration = time.Since(start)
			return result, result.Error
		}

		executedSteps = append(executedSteps, i)
		result.StepResults = append(result.StepResults, StepResult{
			StepName: step.Name,
			Status:   StepStatusSucceeded,
			Duration: duration,
		})
	}

	result.State = StateCompleted
	result.Duration = time.Since(start)
	return result, nil
}

func (s *Saga) compensate(executedIndices []int, result *ExecutionResult) {
	result.State = StateCompensating
	allCompensated := true

	// Create detached context with timeout for compensations in case original ctx is expired
	compCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Reverse compensation loop
	for i := len(executedIndices) - 1; i >= 0; i-- {
		stepIdx := executedIndices[i]
		step := s.steps[stepIdx]

		if step.Compensate == nil {
			continue
		}

		err := step.Compensate(compCtx)
		if err != nil {
			allCompensated = false
			result.StepResults[stepIdx].Status = StepStatusCompFailed
			result.StepResults[stepIdx].CompError = err.Error()
		} else {
			result.StepResults[stepIdx].Status = StepStatusCompensated
		}
	}

	if allCompensated {
		result.State = StateCompensated
	} else {
		result.State = StateFailed
	}
}
