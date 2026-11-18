package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// TaskFunc defines a concurrent fetch operation in an aggregation plan.
type TaskFunc func(ctx context.Context) (any, error)

type task struct {
	name     string
	fn       TaskFunc
	timeout  time.Duration
	critical bool
	fallback any
}

// TaskOption configures an individual aggregation task.
type TaskOption func(*task)

// WithTaskTimeout sets a maximum execution deadline for the task.
func WithTaskTimeout(d time.Duration) TaskOption {
	return func(t *task) {
		if d > 0 {
			t.timeout = d
		}
	}
}

// WithCritical marks whether task failure aborts the entire aggregation.
func WithCritical(critical bool) TaskOption {
	return func(t *task) {
		t.critical = critical
	}
}

// WithFallback specifies a default value if the task fails or times out.
func WithFallback(fallback any) TaskOption {
	return func(t *task) {
		t.fallback = fallback
	}
}

// AggregationResult contains the coalesced responses, errors, and latencies from parallel tasks.
type AggregationResult struct {
	Data      map[string]any           `json:"data"`
	Errors    map[string]string        `json:"errors,omitempty"`
	Durations map[string]time.Duration `json:"durations"`
	mu        sync.RWMutex
}

// Get retrieves the result of a named task.
func (r *AggregationResult) Get(name string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	val, exists := r.Data[name]
	return val, exists
}

// HasErrors returns true if any non-critical task produced an error.
func (r *AggregationResult) HasErrors() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.Errors) > 0
}

// JSON serializes the aggregated result into formatted JSON bytes.
func (r *AggregationResult) JSON() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return json.Marshal(r)
}

// GetTyped extracts and type-asserts the result of a named task.
func GetTyped[T any](r *AggregationResult, name string) (T, error) {
	var zero T
	val, ok := r.Get(name)
	if !ok {
		return zero, fmt.Errorf("gateway: task %q not found in aggregation result", name)
	}

	typed, ok := val.(T)
	if !ok {
		return zero, fmt.Errorf("gateway: task %q result type mismatch (expected %T, got %T)", name, zero, val)
	}
	return typed, nil
}

// Aggregator coordinates parallel scatter-gather task execution with per-task timeouts and fallbacks.
type Aggregator struct {
	tasks []*task
}

// NewAggregator creates an empty Aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{}
}

// Add adds a named task to the aggregation plan.
func (a *Aggregator) Add(name string, fn TaskFunc, opts ...TaskOption) *Aggregator {
	t := &task{
		name:     name,
		fn:       fn,
		critical: false,
	}
	for _, opt := range opts {
		opt(t)
	}
	a.tasks = append(a.tasks, t)
	return a
}

// Execute runs all registered tasks concurrently, respects individual timeouts, and handles fallbacks.
func (a *Aggregator) Execute(ctx context.Context) (*AggregationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	res := &AggregationResult{
		Data:      make(map[string]any),
		Errors:    make(map[string]string),
		Durations: make(map[string]time.Duration),
	}

	var wg sync.WaitGroup
	wg.Add(len(a.tasks))

	var criticalErr error
	var critMu sync.Mutex

	for _, t := range a.tasks {
		currentTask := t
		go func() {
			defer wg.Done()

			taskCtx := ctx
			var cancel context.CancelFunc
			if currentTask.timeout > 0 {
				taskCtx, cancel = context.WithTimeout(ctx, currentTask.timeout)
				defer cancel()
			}

			start := time.Now()
			val, err := safeTaskCall(taskCtx, currentTask.fn)
			duration := time.Since(start)

			res.mu.Lock()
			res.Durations[currentTask.name] = duration
			if err != nil {
				if currentTask.fallback != nil {
					res.Data[currentTask.name] = currentTask.fallback
					res.Errors[currentTask.name] = fmt.Sprintf("%v (using fallback)", err)
				} else {
					res.Errors[currentTask.name] = err.Error()
				}

				if currentTask.critical {
					critMu.Lock()
					if criticalErr == nil {
						criticalErr = fmt.Errorf("%w: %s: %v", ErrCriticalTaskFailed, currentTask.name, err)
					}
					critMu.Unlock()
				}
			} else {
				res.Data[currentTask.name] = val
			}
			res.mu.Unlock()
		}()
	}

	wg.Wait()

	if criticalErr != nil {
		return nil, criticalErr
	}

	return res, nil
}

func safeTaskCall(ctx context.Context, fn TaskFunc) (val any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("gateway: task panicked: %v", r)
		}
	}()
	return fn(ctx)
}
