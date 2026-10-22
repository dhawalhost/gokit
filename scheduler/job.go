package scheduler

import (
	"context"
	"sync"
	"time"
)

// Job represents an executable unit of work managed by the scheduler.
type Job interface {
	Run(ctx context.Context) error
}

// JobFunc adapts an ordinary function to the Job interface.
type JobFunc func(ctx context.Context) error

// Run executes the underlying function.
func (f JobFunc) Run(ctx context.Context) error {
	return f(ctx)
}

// JobState describes the current lifecycle state of a scheduled job.
type JobState string

const (
	// StateIdle indicates the job is awaiting its next scheduled execution.
	StateIdle JobState = "idle"
	// StateRunning indicates an execution is currently in progress.
	StateRunning JobState = "running"
	// StateSkipped indicates the job skipped execution due to overlap or lock acquisition.
	StateSkipped JobState = "skipped"
	// StateSuccess indicates the last run completed successfully.
	StateSuccess JobState = "success"
	// StateFailed indicates the last run produced an unhandled error or panic.
	StateFailed JobState = "failed"
)

// OverlapPolicy defines how the scheduler handles new execution ticks when a previous run is still active.
type OverlapPolicy int

const (
	// OverlapSkip drops the subsequent execution tick if the job is already running.
	OverlapSkip OverlapPolicy = iota
	// OverlapAllow permits concurrent execution instances of the same job.
	OverlapAllow
)

// JobSnapshot captures a point-in-time immutable view of a scheduled job.
type JobSnapshot struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Spec         string            `json:"spec"`
	State        JobState          `json:"state"`
	NextRun      time.Time         `json:"next_run"`
	PrevRun      time.Time         `json:"prev_run"`
	RunCount     int64             `json:"run_count"`
	SuccessCount int64             `json:"success_count"`
	FailureCount int64             `json:"failure_count"`
	SkippedCount int64             `json:"skipped_count"`
	LastDuration time.Duration     `json:"last_duration"`
	LastError    string            `json:"last_error,omitempty"`
	LockKey      string            `json:"lock_key,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// JobConfig holds configuration options for a scheduled job.
type JobConfig struct {
	Name          string
	Timeout       time.Duration
	LockKey       string
	LockTTL       time.Duration
	OverlapPolicy OverlapPolicy
	MaxRetries    int
	RetryBackoff  time.Duration
	Metadata      map[string]string
}

// JobOption applies a configuration parameter to a JobConfig.
type JobOption func(*JobConfig)

// WithJobName assigns a descriptive human-readable name to the job.
func WithJobName(name string) JobOption {
	return func(c *JobConfig) {
		c.Name = name
	}
}

// WithJobTimeout sets a maximum execution duration for each job run.
func WithJobTimeout(d time.Duration) JobOption {
	return func(c *JobConfig) {
		c.Timeout = d
	}
}

// WithJobLock sets a distributed lock key and TTL for the job.
// When configured, the scheduler acquires this lock via distlock before running the job,
// ensuring only one replica executes it in a multi-pod cluster.
func WithJobLock(key string, ttl time.Duration) JobOption {
	return func(c *JobConfig) {
		c.LockKey = key
		c.LockTTL = ttl
	}
}

// WithJobOverlapPolicy configures how overlapping executions are handled.
func WithJobOverlapPolicy(policy OverlapPolicy) JobOption {
	return func(c *JobConfig) {
		c.OverlapPolicy = policy
	}
}

// WithJobRetries configures retry attempts and backoff on job failure.
func WithJobRetries(maxRetries int, backoff time.Duration) JobOption {
	return func(c *JobConfig) {
		c.MaxRetries = maxRetries
		c.RetryBackoff = backoff
	}
}

// WithJobMetadata associates arbitrary key-value metadata tags with the job.
func WithJobMetadata(metadata map[string]string) JobOption {
	return func(c *JobConfig) {
		if c.Metadata == nil {
			c.Metadata = make(map[string]string, len(metadata))
		}
		for k, v := range metadata {
			c.Metadata[k] = v
		}
	}
}

// jobEntry wraps a Job with its schedule, metrics, and execution state.
type jobEntry struct {
	mu           sync.RWMutex
	id           string
	spec         string
	schedule     Schedule
	job          Job
	cfg          JobConfig
	state        JobState
	nextRun      time.Time
	prevRun      time.Time
	runCount     int64
	successCount int64
	failureCount int64
	skippedCount int64
	lastDuration time.Duration
	lastError    error
	running      bool
}

// Snapshot returns a thread-safe copy of the job entry's current status.
func (e *jobEntry) Snapshot() JobSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshotLocked()
}

// snapshotLocked returns a copy assuming e.mu is already held.
func (e *jobEntry) snapshotLocked() JobSnapshot {
	lastErrStr := ""
	if e.lastError != nil {
		lastErrStr = e.lastError.Error()
	}

	metaCopy := make(map[string]string, len(e.cfg.Metadata))
	for k, v := range e.cfg.Metadata {
		metaCopy[k] = v
	}

	return JobSnapshot{
		ID:           e.id,
		Name:         e.cfg.Name,
		Spec:         e.spec,
		State:        e.state,
		NextRun:      e.nextRun,
		PrevRun:      e.prevRun,
		RunCount:     e.runCount,
		SuccessCount: e.successCount,
		FailureCount: e.failureCount,
		SkippedCount: e.skippedCount,
		LastDuration: e.lastDuration,
		LastError:    lastErrStr,
		LockKey:      e.cfg.LockKey,
		Metadata:     metaCopy,
	}
}
