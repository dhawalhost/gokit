package batcher

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// BatchFunc processes a batch of accumulated items.
type BatchFunc[T any] func(ctx context.Context, batch []T) error

// Stats captures operational metrics for a Batcher.
type Stats struct {
	Submitted int64 `json:"submitted"`
	Flushed   int64 `json:"flushed"`
	Batches   int64 `json:"batches"`
	Dropped   int64 `json:"dropped"`
	Errors    int64 `json:"errors"`
}

type batchEnvelope[T any] struct {
	items []T
	done  chan struct{}
}

// Batcher buffers individual items and flushes them in batches based on size or time.
type Batcher[T any] struct {
	cfg       Config
	fn        BatchFunc[T]
	queue     chan T
	batches   chan batchEnvelope[T]
	flushReq  chan chan struct{}
	stopCh    chan struct{}
	stopped   atomic.Bool
	closeOnce sync.Once
	accumWg   sync.WaitGroup
	workersWg sync.WaitGroup

	submitted atomic.Int64
	flushed   atomic.Int64
	batchesN  atomic.Int64
	dropped   atomic.Int64
	errorsN   atomic.Int64
}

// New creates and starts a new Batcher with the provided batch processing function and options.
func New[T any](fn BatchFunc[T], opts ...Option) *Batcher[T] {
	if fn == nil {
		panic("batcher: batch function cannot be nil")
	}

	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	b := &Batcher[T]{
		cfg:      cfg,
		fn:       fn,
		queue:    make(chan T, cfg.QueueSize),
		batches:  make(chan batchEnvelope[T], cfg.Workers*2),
		flushReq: make(chan chan struct{}),
		stopCh:   make(chan struct{}),
	}

	// Start worker pool
	b.workersWg.Add(cfg.Workers)
	for i := 0; i < cfg.Workers; i++ {
		go b.workerLoop()
	}

	// Start batch accumulator
	b.accumWg.Add(1)
	go b.accumulatorLoop()

	return b
}

// Submit enqueues an item according to the configured backpressure strategy.
func (b *Batcher[T]) Submit(ctx context.Context, item T) error {
	if b.stopped.Load() {
		return ErrBatcherClosed
	}

	switch b.cfg.Backpressure {
	case BackpressureReject:
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.stopCh:
			return ErrBatcherClosed
		case b.queue <- item:
			b.submitted.Add(1)
			return nil
		default:
			b.dropped.Add(1)
			return ErrQueueFull
		}

	case BackpressureDropOldest:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-b.stopCh:
				return ErrBatcherClosed
			case b.queue <- item:
				b.submitted.Add(1)
				return nil
			default:
				select {
				case <-b.queue:
					b.dropped.Add(1)
				default:
				}
			}
		}

	case BackpressureBlock:
		fallthrough
	default:
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.stopCh:
			return ErrBatcherClosed
		case b.queue <- item:
			b.submitted.Add(1)
			return nil
		}
	}
}

// Flush triggers an immediate dispatch of any currently buffered items and blocks until they are processed.
func (b *Batcher[T]) Flush(ctx context.Context) error {
	if b.stopped.Load() {
		return ErrBatcherClosed
	}

	done := make(chan struct{})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.stopCh:
		return ErrBatcherClosed
	case b.flushReq <- done:
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// Shutdown gracefully flushes all queued items and terminates all worker goroutines.
func (b *Batcher[T]) Shutdown(ctx context.Context) error {
	b.closeOnce.Do(func() {
		b.stopped.Store(true)
		close(b.stopCh)
		close(b.queue)
	})

	done := make(chan struct{})
	go func() {
		b.accumWg.Wait()
		b.workersWg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// Stop cleanly drains all items and stops the batcher.
func (b *Batcher[T]) Stop() {
	_ = b.Shutdown(context.Background())
}

// Stats returns a snapshot of operational metrics.
func (b *Batcher[T]) Stats() Stats {
	return Stats{
		Submitted: b.submitted.Load(),
		Flushed:   b.flushed.Load(),
		Batches:   b.batchesN.Load(),
		Dropped:   b.dropped.Load(),
		Errors:    b.errorsN.Load(),
	}
}

func (b *Batcher[T]) accumulatorLoop() {
	defer func() {
		close(b.batches)
		b.accumWg.Done()
	}()

	batch := make([]T, 0, b.cfg.MaxBatchSize)
	ticker := time.NewTicker(b.cfg.FlushInterval)
	defer ticker.Stop()

	flushCurrent := func(done chan struct{}) {
		if len(batch) > 0 {
			toDispatch := batch
			batch = make([]T, 0, b.cfg.MaxBatchSize)
			b.batches <- batchEnvelope[T]{items: toDispatch, done: done}
		} else if done != nil {
			close(done)
		}
	}

	for {
		select {
		case item, ok := <-b.queue:
			if !ok {
				flushCurrent(nil)
				return
			}
			batch = append(batch, item)
			if len(batch) >= b.cfg.MaxBatchSize {
				flushCurrent(nil)
				ticker.Reset(b.cfg.FlushInterval)
			}

		case <-ticker.C:
			flushCurrent(nil)

		case done := <-b.flushReq:
			for {
				select {
				case item, ok := <-b.queue:
					if ok {
						batch = append(batch, item)
						continue
					}
				default:
				}
				break
			}
			flushCurrent(done)
		}
	}
}

func (b *Batcher[T]) workerLoop() {
	defer b.workersWg.Done()

	for env := range b.batches {
		b.dispatchBatch(env.items)
		if env.done != nil {
			close(env.done)
		}
	}
}

func (b *Batcher[T]) dispatchBatch(batch []T) {
	ctx := context.Background()
	count := int64(len(batch))

	var err error
	maxAttempts := 1 + b.cfg.RetryCount

	for attempt := 0; attempt < maxAttempts; attempt++ {
		err = b.safeCall(ctx, batch)
		if err == nil {
			b.flushed.Add(count)
			b.batchesN.Add(1)
			return
		}

		if attempt < b.cfg.RetryCount {
			time.Sleep(b.cfg.RetryBackoff)
		}
	}

	b.errorsN.Add(1)
}

func (b *Batcher[T]) safeCall(ctx context.Context, batch []T) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("batcher: batch function panicked: %v", r)
		}
	}()
	return b.fn(ctx, batch)
}
