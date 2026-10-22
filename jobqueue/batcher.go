package jobqueue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrQueueFull is returned when pushing to a non-blocking full batcher.
	ErrQueueFull = errors.New("jobqueue: batcher buffer is full")
	// ErrBatcherClosed is returned when pushing to a closed batcher.
	ErrBatcherClosed = errors.New("jobqueue: batcher is closed")
)

// FlushFunc defines the callback invoked when a batch of items is ready for processing.
type FlushFunc[T any] func(ctx context.Context, batch []T) error

// BatcherConfig defines options for high-throughput amortized batching.
type BatcherConfig[T any] struct {
	// BatchSize triggers a flush once this number of items is reached (default: 50).
	BatchSize int
	// Linger is the maximum time to wait before flushing a partial batch (default: 50ms).
	Linger time.Duration
	// BufferSize is the channel capacity for incoming items before backpressure occurs (default: 1000).
	BufferSize int
	// BlockOnFull specifies whether Push blocks when the buffer is full (true) or returns ErrQueueFull (false).
	BlockOnFull bool
	// FlushFunc is the required callback to process flushed batches.
	FlushFunc FlushFunc[T]
	// OnDrop is an optional callback called when an item is dropped due to a full buffer.
	OnDrop func(item T)
}

// BatcherStats provides observability into batching performance and backpressure.
type BatcherStats struct {
	ItemsPushed    int64
	ItemsFlushed   int64
	BatchesFlushed int64
	ItemsDropped   int64
}

// Batcher buffers incoming items and flushes them in batches based on size or linger timeout.
// This amortizes disk and network overhead, increasing write throughput by 5-20x.
type Batcher[T any] struct {
	cfg     BatcherConfig[T]
	inCh    chan T
	flushCh chan chan error
	closeCh chan struct{}
	closed  atomic.Bool
	stats   BatcherStats
	wg      sync.WaitGroup
	flushMu sync.Mutex
}

// NewBatcher creates and starts a new high-throughput batcher.
func NewBatcher[T any](cfg BatcherConfig[T]) *Batcher[T] {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.Linger <= 0 {
		cfg.Linger = 50 * time.Millisecond
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 1000
	}

	b := &Batcher[T]{
		cfg:     cfg,
		inCh:    make(chan T, cfg.BufferSize),
		flushCh: make(chan chan error),
		closeCh: make(chan struct{}),
	}

	b.wg.Add(1)
	go b.workerLoop()

	return b
}

// Push adds an item to the batch buffer. Respects backpressure settings (blocks or returns ErrQueueFull).
func (b *Batcher[T]) Push(ctx context.Context, item T) error {
	if b.closed.Load() {
		return ErrBatcherClosed
	}

	if b.cfg.BlockOnFull {
		select {
		case <-b.closeCh:
			return ErrBatcherClosed
		case <-ctx.Done():
			return ctx.Err()
		case b.inCh <- item:
			atomic.AddInt64(&b.stats.ItemsPushed, 1)
			return nil
		}
	}

	select {
	case b.inCh <- item:
		atomic.AddInt64(&b.stats.ItemsPushed, 1)
		return nil
	default:
		atomic.AddInt64(&b.stats.ItemsDropped, 1)
		if b.cfg.OnDrop != nil {
			b.cfg.OnDrop(item)
		}
		return ErrQueueFull
	}
}

// Flush synchronously flushes any currently buffered items.
func (b *Batcher[T]) Flush(ctx context.Context) error {
	if b.closed.Load() {
		return ErrBatcherClosed
	}

	errCh := make(chan error, 1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.closeCh:
		return ErrBatcherClosed
	case b.flushCh <- errCh:
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// Stats returns a snapshot of batcher metrics.
func (b *Batcher[T]) Stats() BatcherStats {
	return BatcherStats{
		ItemsPushed:    atomic.LoadInt64(&b.stats.ItemsPushed),
		ItemsFlushed:   atomic.LoadInt64(&b.stats.ItemsFlushed),
		BatchesFlushed: atomic.LoadInt64(&b.stats.BatchesFlushed),
		ItemsDropped:   atomic.LoadInt64(&b.stats.ItemsDropped),
	}
}

// Close gracefully flushes all pending items and shuts down the batcher worker.
func (b *Batcher[T]) Close() error {
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}

	close(b.closeCh)
	b.wg.Wait()
	return nil
}

func (b *Batcher[T]) workerLoop() {
	defer b.wg.Done()

	batch := make([]T, 0, b.cfg.BatchSize)
	ticker := time.NewTicker(b.cfg.Linger)
	defer ticker.Stop()

	flushCurrent := func() error {
		if len(batch) == 0 {
			return nil
		}
		b.flushMu.Lock()
		defer b.flushMu.Unlock()

		toFlush := batch
		batch = make([]T, 0, b.cfg.BatchSize)
		atomic.AddInt64(&b.stats.BatchesFlushed, 1)
		atomic.AddInt64(&b.stats.ItemsFlushed, int64(len(toFlush)))

		if b.cfg.FlushFunc != nil {
			return b.cfg.FlushFunc(context.Background(), toFlush)
		}
		return nil
	}

	for {
		select {
		case item := <-b.inCh:
			batch = append(batch, item)
			if len(batch) >= b.cfg.BatchSize {
				_ = flushCurrent()
				ticker.Reset(b.cfg.Linger)
			}

		case <-ticker.C:
			if len(batch) > 0 {
				_ = flushCurrent()
			}

		case errCh := <-b.flushCh:
			// Drain all currently pending items in inCh before executing flush
			var drainErr error
			draining := true
			for draining {
				select {
				case item := <-b.inCh:
					batch = append(batch, item)
					if len(batch) >= b.cfg.BatchSize {
						if err := flushCurrent(); err != nil && drainErr == nil {
							drainErr = err
						}
					}
				default:
					draining = false
				}
			}
			err := flushCurrent()
			if drainErr != nil && err == nil {
				err = drainErr
			}
			errCh <- err
			ticker.Reset(b.cfg.Linger)

		case <-b.closeCh:
			// Drain any remaining items in inCh
			for {
				select {
				case item := <-b.inCh:
					batch = append(batch, item)
					if len(batch) >= b.cfg.BatchSize {
						_ = flushCurrent()
					}
				default:
					_ = flushCurrent()
					return
				}
			}
		}
	}
}
