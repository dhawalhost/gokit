package jobqueue

import (
	"container/heap"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// jobPriorityQueue implements heap.Interface based on job Priority and CreatedAt.
type jobPriorityQueue []*Job

func (pq jobPriorityQueue) Len() int { return len(pq) }

func (pq jobPriorityQueue) Less(i, j int) bool {
	// Higher priority goes first
	if pq[i].Priority != pq[j].Priority {
		return pq[i].Priority > pq[j].Priority
	}
	// Earlier created goes first
	return pq[i].CreatedAt.Before(pq[j].CreatedAt)
}

func (pq jobPriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
}

func (pq *jobPriorityQueue) Push(x any) {
	item := x.(*Job)
	*pq = append(*pq, item)
}

func (pq *jobPriorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*pq = old[0 : n-1]
	return item
}

// MemoryQueue provides an in-memory priority background job queue with worker pools.
type MemoryQueue struct {
	cfg      QueueConfig
	handlers map[string]JobHandler
	hMu      sync.RWMutex
	readyPQ  jobPriorityQueue
	delayed  []*Job
	pqMu     sync.Mutex
	notifyCh chan struct{}
	closeCh  chan struct{}
	wg       sync.WaitGroup
	started  atomic.Bool
	closed   atomic.Bool
}

// NewMemoryQueue constructs a new in-memory JobQueue.
func NewMemoryQueue(cfg QueueConfig) *MemoryQueue {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 100 * time.Millisecond
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 100 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 10 * time.Second
	}

	q := &MemoryQueue{
		cfg:      cfg,
		handlers: make(map[string]JobHandler),
		readyPQ:  make(jobPriorityQueue, 0),
		delayed:  make([]*Job, 0),
		notifyCh: make(chan struct{}, 1),
		closeCh:  make(chan struct{}),
	}
	heap.Init(&q.readyPQ)
	return q
}

// RegisterHandler binds a handler to a job type.
func (q *MemoryQueue) RegisterHandler(jobType string, handler JobHandler) {
	q.hMu.Lock()
	defer q.hMu.Unlock()
	q.handlers[jobType] = handler
}

// Enqueue submits a job for immediate execution.
func (q *MemoryQueue) Enqueue(ctx context.Context, job *Job) error {
	if q.closed.Load() {
		return fmt.Errorf("jobqueue: queue is closed")
	}
	if job == nil {
		return fmt.Errorf("jobqueue: nil job")
	}

	q.pqMu.Lock()
	if !job.RunAt.IsZero() && job.RunAt.After(time.Now().UTC()) {
		job.State = StateScheduled
		q.delayed = append(q.delayed, job)
	} else {
		job.State = StatePending
		heap.Push(&q.readyPQ, job)
	}
	q.pqMu.Unlock()

	q.signal()
	return nil
}

// EnqueueIn submits a job to execute after the specified delay.
func (q *MemoryQueue) EnqueueIn(ctx context.Context, job *Job, delay time.Duration) error {
	job.WithDelay(delay)
	return q.Enqueue(ctx, job)
}

// EnqueueAt submits a job to execute at a specific future timestamp.
func (q *MemoryQueue) EnqueueAt(ctx context.Context, job *Job, runAt time.Time) error {
	job.WithRunAt(runAt)
	return q.Enqueue(ctx, job)
}

func (q *MemoryQueue) signal() {
	select {
	case q.notifyCh <- struct{}{}:
	default:
	}
}

// Start launches worker goroutines and the delayed job scheduler.
func (q *MemoryQueue) Start(ctx context.Context) error {
	if !q.started.CompareAndSwap(false, true) {
		return fmt.Errorf("jobqueue: already started")
	}

	// 1. Start delayed job poller
	q.wg.Add(1)
	go q.scheduler(ctx)

	// 2. Start worker pool
	for i := 0; i < q.cfg.Workers; i++ {
		q.wg.Add(1)
		go q.worker(ctx)
	}

	return nil
}

func (q *MemoryQueue) scheduler(ctx context.Context) {
	defer q.wg.Done()
	ticker := time.NewTicker(q.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-q.closeCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			q.promoteDelayed()
		}
	}
}

func (q *MemoryQueue) promoteDelayed() {
	now := time.Now().UTC()
	q.pqMu.Lock()
	if len(q.delayed) == 0 {
		q.pqMu.Unlock()
		return
	}

	remaining := make([]*Job, 0, len(q.delayed))
	promoted := false

	for _, job := range q.delayed {
		if !job.RunAt.After(now) {
			job.State = StatePending
			heap.Push(&q.readyPQ, job)
			promoted = true
		} else {
			remaining = append(remaining, job)
		}
	}
	q.delayed = remaining
	q.pqMu.Unlock()

	if promoted {
		q.signal()
	}
}

func (q *MemoryQueue) worker(ctx context.Context) {
	defer q.wg.Done()

	for {
		select {
		case <-q.closeCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		job := q.popJob()
		if job == nil {
			select {
			case <-q.closeCh:
				return
			case <-ctx.Done():
				return
			case <-q.notifyCh:
				continue
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}

		q.processJob(ctx, job)
	}
}

func (q *MemoryQueue) popJob() *Job {
	q.pqMu.Lock()
	defer q.pqMu.Unlock()
	if q.readyPQ.Len() == 0 {
		return nil
	}
	return heap.Pop(&q.readyPQ).(*Job)
}

func (q *MemoryQueue) processJob(ctx context.Context, job *Job) {
	job.State = StateProcessing

	q.hMu.RLock()
	handler, exists := q.handlers[job.Type]
	q.hMu.RUnlock()

	if !exists {
		job.State = StateFailed
		job.LastError = fmt.Sprintf("no handler registered for job type %q", job.Type)
		return
	}

	err := handler(ctx, job)
	now := time.Now().UTC()

	if err == nil {
		job.State = StateCompleted
		job.CompletedAt = &now
		return
	}

	job.LastError = err.Error()
	job.RetryCount++

	if job.RetryCount >= job.MaxRetries {
		job.State = StateFailed
		return
	}

	// Calculate exponential backoff
	backoff := q.cfg.BaseBackoff * time.Duration(1<<uint(job.RetryCount-1))
	if backoff > q.cfg.MaxBackoff {
		backoff = q.cfg.MaxBackoff
	}

	job.RunAt = now.Add(backoff)
	job.State = StateScheduled

	q.pqMu.Lock()
	q.delayed = append(q.delayed, job)
	q.pqMu.Unlock()
}

// Stop gracefully stops workers and the scheduler.
func (q *MemoryQueue) Stop() error {
	if q.closed.CompareAndSwap(false, true) {
		close(q.closeCh)
		q.wg.Wait()
	}
	return nil
}
