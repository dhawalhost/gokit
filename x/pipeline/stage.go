package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Stage defines a transformation or filtering step in a pipeline execution graph.
type Stage[In, Out any] interface {
	Name() string
	Run(ctx context.Context, in <-chan In, out chan<- Out) error
	RunWithContext(ctx context.Context, in <-chan In, out chan<- Out, sm *StageMetrics, dlq DeadLetterSink) error
}

// StageOptions configures execution behavior for an individual stage.
type StageOptions struct {
	BufferSize   int
	Concurrency  int
	DLQ          DeadLetterSink
	RetryCount   int
	RetryBackoff time.Duration
}

// StageOption is a functional option for configuring StageOptions.
type StageOption func(*StageOptions)

// WithBufferSize sets the stage output buffer capacity.
func WithBufferSize(size int) StageOption {
	return func(o *StageOptions) {
		o.BufferSize = size
	}
}

// WithConcurrency configures parallel worker concurrency for supported stages.
func WithConcurrency(workers int) StageOption {
	return func(o *StageOptions) {
		o.Concurrency = workers
	}
}

// WithStageDLQ sets a dedicated DeadLetterSink for this specific stage.
func WithStageDLQ(dlq DeadLetterSink) StageOption {
	return func(o *StageOptions) {
		o.DLQ = dlq
	}
}

// WithRetry configures automatic retry attempts and backoff duration for failed item transformations.
func WithRetry(attempts int, backoff time.Duration) StageOption {
	return func(o *StageOptions) {
		o.RetryCount = attempts
		o.RetryBackoff = backoff
	}
}

type stageRunFunc[In, Out any] func(ctx context.Context, in <-chan In, out chan<- Out, sm *StageMetrics, dlq DeadLetterSink) error

type stageImpl[In, Out any] struct {
	name    string
	runFunc stageRunFunc[In, Out]
}

func (s *stageImpl[In, Out]) Name() string {
	return s.name
}

func (s *stageImpl[In, Out]) Run(ctx context.Context, in <-chan In, out chan<- Out) error {
	return s.runFunc(ctx, in, out, nil, nil)
}

func (s *stageImpl[In, Out]) RunWithContext(ctx context.Context, in <-chan In, out chan<- Out, sm *StageMetrics, dlq DeadLetterSink) error {
	return s.runFunc(ctx, in, out, sm, dlq)
}

// Map constructs a 1-to-1 transformation stage.
func Map[In, Out any](name string, fn func(ctx context.Context, item In) (Out, error), opts ...StageOption) Stage[In, Out] {
	var cfg StageOptions
	for _, opt := range opts {
		opt(&cfg)
	}

	return &stageImpl[In, Out]{
		name: name,
		runFunc: func(ctx context.Context, in <-chan In, out chan<- Out, sm *StageMetrics, dlq DeadLetterSink) error {
			activeDLQ := dlq
			if cfg.DLQ != nil {
				activeDLQ = cfg.DLQ
			}

			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case item, ok := <-in:
					if !ok {
						return nil
					}
					if sm != nil {
						sm.IncIn()
					}

					start := time.Now()
					var res Out
					var err error

					attempts := cfg.RetryCount + 1
					for a := 0; a < attempts; a++ {
						res, err = safeExecuteMap(ctx, item, fn)
						if err == nil {
							break
						}
						if a < attempts-1 && cfg.RetryBackoff > 0 {
							select {
							case <-ctx.Done():
								return ctx.Err()
							case <-time.After(cfg.RetryBackoff):
							}
						}
					}

					if err != nil {
						if sm != nil {
							sm.IncErrors()
						}
						if activeDLQ != nil {
							if sm != nil {
								sm.IncDeadLetter()
							}
							_ = activeDLQ.HandleDeadLetter(ctx, DeadLetterEntry{
								StageName: name,
								Item:      item,
								Err:       err,
								Timestamp: time.Now().UTC(),
							})
							continue
						}
						return &StageError{StageName: name, Item: item, Err: err}
					}

					if sm != nil {
						sm.RecordDuration(time.Since(start))
						sm.IncOut()
					}

					select {
					case <-ctx.Done():
						return ctx.Err()
					case out <- res:
					}
				}
			}
		},
	}
}

func safeExecuteMap[In, Out any](ctx context.Context, item In, fn func(context.Context, In) (Out, error)) (res Out, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrStagePanicked, r)
		}
	}()
	return fn(ctx, item)
}

// Filter creates a predicate filtering stage that selectively passes items downstream.
func Filter[T any](name string, predicate func(ctx context.Context, item T) bool) Stage[T, T] {
	return &stageImpl[T, T]{
		name: name,
		runFunc: func(ctx context.Context, in <-chan T, out chan<- T, sm *StageMetrics, _ DeadLetterSink) error {
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case item, ok := <-in:
					if !ok {
						return nil
					}
					if sm != nil {
						sm.IncIn()
					}

					start := time.Now()
					pass := safeExecuteFilter(ctx, item, predicate)
					if pass {
						if sm != nil {
							sm.RecordDuration(time.Since(start))
							sm.IncOut()
						}
						select {
						case <-ctx.Done():
							return ctx.Err()
						case out <- item:
						}
					}
				}
			}
		},
	}
}

func safeExecuteFilter[T any](ctx context.Context, item T, predicate func(context.Context, T) bool) (pass bool) {
	defer func() {
		if r := recover(); r != nil {
			pass = false
		}
	}()
	return predicate(ctx, item)
}

// FlatMap constructs a 1-to-many transformation stage where each input can produce 0 or more outputs.
func FlatMap[In, Out any](name string, fn func(ctx context.Context, item In) ([]Out, error), opts ...StageOption) Stage[In, Out] {
	var cfg StageOptions
	for _, opt := range opts {
		opt(&cfg)
	}

	return &stageImpl[In, Out]{
		name: name,
		runFunc: func(ctx context.Context, in <-chan In, out chan<- Out, sm *StageMetrics, dlq DeadLetterSink) error {
			activeDLQ := dlq
			if cfg.DLQ != nil {
				activeDLQ = cfg.DLQ
			}

			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case item, ok := <-in:
					if !ok {
						return nil
					}
					if sm != nil {
						sm.IncIn()
					}

					start := time.Now()
					results, err := safeExecuteFlatMap(ctx, item, fn)
					if err != nil {
						if sm != nil {
							sm.IncErrors()
						}
						if activeDLQ != nil {
							if sm != nil {
								sm.IncDeadLetter()
							}
							_ = activeDLQ.HandleDeadLetter(ctx, DeadLetterEntry{
								StageName: name,
								Item:      item,
								Err:       err,
								Timestamp: time.Now().UTC(),
							})
							continue
						}
						return &StageError{StageName: name, Item: item, Err: err}
					}

					if sm != nil {
						sm.RecordDuration(time.Since(start))
					}

					for _, res := range results {
						if sm != nil {
							sm.IncOut()
						}
						select {
						case <-ctx.Done():
							return ctx.Err()
						case out <- res:
						}
					}
				}
			}
		},
	}
}

func safeExecuteFlatMap[In, Out any](ctx context.Context, item In, fn func(context.Context, In) ([]Out, error)) (res []Out, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrStagePanicked, r)
		}
	}()
	return fn(ctx, item)
}

// Batch groups arriving stream items into slices of up to maxBatchSize or when maxWait elapses.
func Batch[T any](name string, maxBatchSize int, maxWait time.Duration) Stage[T, []T] {
	if maxBatchSize <= 0 {
		maxBatchSize = 100
	}
	if maxWait <= 0 {
		maxWait = 50 * time.Millisecond
	}

	return &stageImpl[T, []T]{
		name: name,
		runFunc: func(ctx context.Context, in <-chan T, out chan<- []T, sm *StageMetrics, _ DeadLetterSink) error {
			batch := make([]T, 0, maxBatchSize)
			ticker := time.NewTicker(maxWait)
			defer ticker.Stop()

			flush := func() error {
				if len(batch) == 0 {
					return nil
				}
				toEmit := batch
				batch = make([]T, 0, maxBatchSize)
				if sm != nil {
					sm.IncOut()
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case out <- toEmit:
					return nil
				}
			}

			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
					if err := flush(); err != nil {
						return err
					}
				case item, ok := <-in:
					if !ok {
						return flush()
					}
					if sm != nil {
						sm.IncIn()
					}
					batch = append(batch, item)
					if len(batch) >= maxBatchSize {
						if err := flush(); err != nil {
							return err
						}
					}
				}
			}
		},
	}
}

// Unbatch flattens slices of items ([]T) back into an individual item stream (T).
func Unbatch[T any](name string) Stage[[]T, T] {
	return &stageImpl[[]T, T]{
		name: name,
		runFunc: func(ctx context.Context, in <-chan []T, out chan<- T, sm *StageMetrics, _ DeadLetterSink) error {
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case batch, ok := <-in:
					if !ok {
						return nil
					}
					if sm != nil {
						sm.IncIn()
					}
					for _, item := range batch {
						if err := ctx.Err(); err != nil {
							return err
						}
						if sm != nil {
							sm.IncOut()
						}
						select {
						case <-ctx.Done():
							return ctx.Err()
						case out <- item:
						}
					}
				}
			}
		},
	}
}

// Parallel executes workerFn across a pool of concurrent worker goroutines.
func Parallel[In, Out any](
	name string,
	concurrency int,
	workerFn func(ctx context.Context, item In) (Out, error),
	opts ...StageOption,
) Stage[In, Out] {
	if concurrency <= 0 {
		concurrency = 4
	}

	var cfg StageOptions
	for _, opt := range opts {
		opt(&cfg)
	}

	return &stageImpl[In, Out]{
		name: name,
		runFunc: func(ctx context.Context, in <-chan In, out chan<- Out, sm *StageMetrics, dlq DeadLetterSink) error {
			activeDLQ := dlq
			if cfg.DLQ != nil {
				activeDLQ = cfg.DLQ
			}

			var wg sync.WaitGroup
			errChan := make(chan error, concurrency)

			for i := 0; i < concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						select {
						case <-ctx.Done():
							return
						case item, ok := <-in:
							if !ok {
								return
							}
							if sm != nil {
								sm.IncIn()
							}

							start := time.Now()
							var res Out
							var err error

							attempts := cfg.RetryCount + 1
							for a := 0; a < attempts; a++ {
								res, err = safeExecuteMap(ctx, item, workerFn)
								if err == nil {
									break
								}
								if a < attempts-1 && cfg.RetryBackoff > 0 {
									select {
									case <-ctx.Done():
										return
									case <-time.After(cfg.RetryBackoff):
									}
								}
							}

							if err != nil {
								if sm != nil {
									sm.IncErrors()
								}
								if activeDLQ != nil {
									if sm != nil {
										sm.IncDeadLetter()
									}
									_ = activeDLQ.HandleDeadLetter(ctx, DeadLetterEntry{
										StageName: name,
										Item:      item,
										Err:       err,
										Timestamp: time.Now().UTC(),
									})
									continue
								}
								select {
								case errChan <- &StageError{StageName: name, Item: item, Err: err}:
								default:
								}
								return
							}

							if sm != nil {
								sm.RecordDuration(time.Since(start))
								sm.IncOut()
							}

							select {
							case <-ctx.Done():
								return
							case out <- res:
							}
						}
					}
				}()
			}

			// Wait for workers to complete
			wg.Wait()

			select {
			case err := <-errChan:
				return err
			default:
				return nil
			}
		},
	}
}

// Throttle limits the rate at which items flow through the stage using a minimum interval between items.
func Throttle[T any](name string, interval time.Duration) Stage[T, T] {
	return &stageImpl[T, T]{
		name: name,
		runFunc: func(ctx context.Context, in <-chan T, out chan<- T, sm *StageMetrics, _ DeadLetterSink) error {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case item, ok := <-in:
					if !ok {
						return nil
					}
					if sm != nil {
						sm.IncIn()
					}

					// Wait for ticker interval before emitting item
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-ticker.C:
					}

					if sm != nil {
						sm.IncOut()
					}

					select {
					case <-ctx.Done():
						return ctx.Err()
					case out <- item:
					}
				}
			}
		},
	}
}
