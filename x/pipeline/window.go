package pipeline

import (
	"context"
	"fmt"
	"time"
)

// Window represents an aggregated temporal bucket of stream elements.
type Window[T any] struct {
	Items     []T       `json:"items"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

// WindowOptions provides configuration for stream windowing operations.
type WindowOptions[T any] struct {
	TimestampExtractor func(item T) time.Time
	EmitEmptyWindows   bool
	MaxBufferSize      int
}

// WindowOption configures WindowOptions.
type WindowOption[T any] func(*WindowOptions[T])

// WithTimestampExtractor sets a custom function to extract event timestamps from items.
func WithTimestampExtractor[T any](fn func(item T) time.Time) WindowOption[T] {
	return func(o *WindowOptions[T]) {
		o.TimestampExtractor = fn
	}
}

// WithEmitEmptyWindows specifies whether empty windows should trigger aggregation emissions.
func WithEmitEmptyWindows[T any](emit bool) WindowOption[T] {
	return func(o *WindowOptions[T]) {
		o.EmitEmptyWindows = emit
	}
}

// WithWindowBufferSize sets the maximum items allowed in a sliding window buffer.
func WithWindowBufferSize[T any](size int) WindowOption[T] {
	return func(o *WindowOptions[T]) {
		o.MaxBufferSize = size
	}
}

// TumblingWindow creates a non-overlapping, fixed-duration temporal window stage.
// It gathers elements arriving within the duration interval and emits the aggregated result.
func TumblingWindow[T, R any](
	name string,
	duration time.Duration,
	aggregator func(ctx context.Context, win Window[T]) (R, error),
	opts ...WindowOption[T],
) Stage[T, R] {
	var cfg WindowOptions[T]
	for _, opt := range opts {
		opt(&cfg)
	}

	return &stageImpl[T, R]{
		name: name,
		runFunc: func(ctx context.Context, in <-chan T, out chan<- R, sm *StageMetrics, dlq DeadLetterSink) error {
			ticker := time.NewTicker(duration)
			defer ticker.Stop()

			var currentItems []T
			winStart := time.Now().UTC()

			flushWindow := func(winEnd time.Time) error {
				if len(currentItems) == 0 && !cfg.EmitEmptyWindows {
					winStart = winEnd
					return nil
				}

				win := Window[T]{
					Items:     currentItems,
					StartTime: winStart,
					EndTime:   winEnd,
				}
				currentItems = nil
				winStart = winEnd

				start := time.Now()
				res, err := safeAggregate(ctx, win, aggregator)
				if err != nil {
					if sm != nil {
						sm.IncErrors()
					}
					if dlq != nil {
						if sm != nil {
							sm.IncDeadLetter()
						}
						_ = dlq.HandleDeadLetter(ctx, DeadLetterEntry{
							StageName: name,
							Item:      win,
							Err:       err,
							Timestamp: time.Now().UTC(),
						})
						return nil
					}
					return &StageError{StageName: name, Item: win, Err: err}
				}

				if sm != nil {
					sm.RecordDuration(time.Since(start))
					sm.IncOut()
				}

				select {
				case <-ctx.Done():
					return ctx.Err()
				case out <- res:
					return nil
				}
			}

			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
					if err := flushWindow(time.Now().UTC()); err != nil {
						return err
					}
				case item, ok := <-in:
					if !ok {
						// Stream ended: flush any remaining items
						return flushWindow(time.Now().UTC())
					}
					if sm != nil {
						sm.IncIn()
					}
					currentItems = append(currentItems, item)
				}
			}
		},
	}
}

type timestampedItem[T any] struct {
	item      T
	timestamp time.Time
}

// SlidingWindow creates an overlapping temporal window stage of length 'size', evaluated every 'slide' duration.
func SlidingWindow[T, R any](
	name string,
	size time.Duration,
	slide time.Duration,
	aggregator func(ctx context.Context, win Window[T]) (R, error),
	opts ...WindowOption[T],
) Stage[T, R] {
	var cfg WindowOptions[T]
	for _, opt := range opts {
		opt(&cfg)
	}

	maxBuf := cfg.MaxBufferSize
	if maxBuf <= 0 {
		maxBuf = 100000
	}

	return &stageImpl[T, R]{
		name: name,
		runFunc: func(ctx context.Context, in <-chan T, out chan<- R, sm *StageMetrics, dlq DeadLetterSink) error {
			ticker := time.NewTicker(slide)
			defer ticker.Stop()

			var buffer []timestampedItem[T]

			evaluateWindow := func(now time.Time) error {
				cutoff := now.Add(-size)

				// Prune expired items
				validIdx := 0
				for validIdx < len(buffer) && buffer[validIdx].timestamp.Before(cutoff) {
					validIdx++
				}
				if validIdx > 0 {
					buffer = buffer[validIdx:]
				}

				if len(buffer) == 0 && !cfg.EmitEmptyWindows {
					return nil
				}

				items := make([]T, len(buffer))
				for i, ti := range buffer {
					items[i] = ti.item
				}

				win := Window[T]{
					Items:     items,
					StartTime: cutoff,
					EndTime:   now,
				}

				start := time.Now()
				res, err := safeAggregate(ctx, win, aggregator)
				if err != nil {
					if sm != nil {
						sm.IncErrors()
					}
					if dlq != nil {
						if sm != nil {
							sm.IncDeadLetter()
						}
						_ = dlq.HandleDeadLetter(ctx, DeadLetterEntry{
							StageName: name,
							Item:      win,
							Err:       err,
							Timestamp: time.Now().UTC(),
						})
						return nil
					}
					return &StageError{StageName: name, Item: win, Err: err}
				}

				if sm != nil {
					sm.RecordDuration(time.Since(start))
					sm.IncOut()
				}

				select {
				case <-ctx.Done():
					return ctx.Err()
				case out <- res:
					return nil
				}
			}

			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
					if err := evaluateWindow(time.Now().UTC()); err != nil {
						return err
					}
				case item, ok := <-in:
					if !ok {
						// Stream closed: final evaluation
						return evaluateWindow(time.Now().UTC())
					}
					if sm != nil {
						sm.IncIn()
					}

					ts := time.Now().UTC()
					if cfg.TimestampExtractor != nil {
						ts = cfg.TimestampExtractor(item)
					}

					if len(buffer) >= maxBuf {
						// Drop oldest to prevent unbounded buffer growth
						buffer = buffer[1:]
					}
					buffer = append(buffer, timestampedItem[T]{item: item, timestamp: ts})
				}
			}
		},
	}
}

func safeAggregate[T, R any](
	ctx context.Context,
	win Window[T],
	aggregator func(ctx context.Context, win Window[T]) (R, error),
) (result R, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrStagePanicked, r)
		}
	}()
	return aggregator(ctx, win)
}
