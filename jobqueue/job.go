package jobqueue

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// State represents the lifecycle state of a queued background job.
type State string

// Job lifecycle states.
const (
	StatePending    State = "pending"
	StateProcessing State = "processing"
	StateCompleted  State = "completed"
	StateFailed     State = "failed"
	StateScheduled  State = "scheduled"
)

// Standard priority constants.
const (
	PriorityLow      = 0
	PriorityNormal   = 10
	PriorityHigh     = 20
	PriorityCritical = 30
)

// Job represents a background task to be processed asynchronously.
type Job struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Payload     []byte            `json:"payload"`
	Priority    int               `json:"priority"`
	State       State             `json:"state"`
	RetryCount  int               `json:"retry_count"`
	MaxRetries  int               `json:"max_retries"`
	LastError   string            `json:"last_error,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	RunAt       time.Time         `json:"run_at"`
	CreatedAt   time.Time         `json:"created_at"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
}

// NewJob creates a new Job ready for enqueueing.
func NewJob(jobType string, payload []byte) *Job {
	now := time.Now().UTC()
	return &Job{
		ID:         uuid.NewString(),
		Type:       jobType,
		Payload:    payload,
		Priority:   PriorityNormal,
		State:      StatePending,
		MaxRetries: 3,
		Headers:    make(map[string]string),
		RunAt:      now,
		CreatedAt:  now,
	}
}

// NewJSONJob serializes data into a JSON payload and returns a new Job.
func NewJSONJob(jobType string, data any) (*Job, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("jobqueue: marshal payload: %w", err)
	}
	return NewJob(jobType, bytes), nil
}

// Unmarshal deserializes the job payload into the target destination.
func (j *Job) Unmarshal(v any) error {
	if len(j.Payload) == 0 {
		return fmt.Errorf("jobqueue: empty payload")
	}
	return json.Unmarshal(j.Payload, v)
}

// WithPriority sets the job priority (higher values process first).
func (j *Job) WithPriority(priority int) *Job {
	j.Priority = priority
	return j
}

// WithMaxRetries sets the maximum retry attempts on failure.
func (j *Job) WithMaxRetries(retries int) *Job {
	j.MaxRetries = retries
	return j
}

// WithDelay schedules the job to run after the specified delay.
func (j *Job) WithDelay(delay time.Duration) *Job {
	j.RunAt = time.Now().UTC().Add(delay)
	j.State = StateScheduled
	return j
}

// WithRunAt schedules the job to execute at a specific future timestamp.
func (j *Job) WithRunAt(t time.Time) *Job {
	j.RunAt = t.UTC()
	j.State = StateScheduled
	return j
}

// WithHeader attaches metadata to the job (e.g. tenant_id, traceparent).
func (j *Job) WithHeader(key, value string) *Job {
	if j.Headers == nil {
		j.Headers = make(map[string]string)
	}
	j.Headers[key] = value
	return j
}
