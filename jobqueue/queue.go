package jobqueue

import (
	"context"
	"time"
)

// JobHandler defines the function signature for executing a background task.
type JobHandler func(ctx context.Context, job *Job) error

// Enqueuer defines the contract for submitting jobs to a queue.
type Enqueuer interface {
	Enqueue(ctx context.Context, job *Job) error
	EnqueueIn(ctx context.Context, job *Job, delay time.Duration) error
	EnqueueAt(ctx context.Context, job *Job, runAt time.Time) error
}

// Queue unites enqueuing, handler registration, and worker lifecycle.
type Queue interface {
	Enqueuer
	RegisterHandler(jobType string, handler JobHandler)
	Start(ctx context.Context) error
	Stop() error
}

// QueueConfig holds common configuration for job queues.
type QueueConfig struct {
	Workers      int           // Number of concurrent worker goroutines (default: 4)
	PollInterval time.Duration // Interval to poll for delayed/scheduled jobs (default: 1s)
	BaseBackoff  time.Duration // Base exponential backoff for failed jobs (default: 1s)
	MaxBackoff   time.Duration // Cap on retry backoff (default: 1m)
}
