package jobqueue_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/jobqueue"
)

func TestBatcher_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "flushes when batch size threshold is reached",
			run: func(t *testing.T) {
				var mu sync.Mutex
				var receivedBatches [][]string

				batcher := jobqueue.NewBatcher(jobqueue.BatcherConfig[string]{
					BatchSize:  3,
					Linger:     1 * time.Second, // Long linger so size triggers first
					BufferSize: 10,
					FlushFunc: func(ctx context.Context, batch []string) error {
						mu.Lock()
						defer mu.Unlock()
						receivedBatches = append(receivedBatches, batch)
						return nil
					},
				})
				defer func() { _ = batcher.Close() }()

				ctx := context.Background()
				_ = batcher.Push(ctx, "a")
				_ = batcher.Push(ctx, "b")
				_ = batcher.Push(ctx, "c")

				// Wait briefly for workerLoop to process batch
				time.Sleep(50 * time.Millisecond)

				mu.Lock()
				defer mu.Unlock()
				if len(receivedBatches) != 1 {
					t.Fatalf("expected 1 batch, got %d", len(receivedBatches))
				}
				if len(receivedBatches[0]) != 3 {
					t.Fatalf("expected batch size 3, got %d", len(receivedBatches[0]))
				}
			},
		},
		{
			name: "flushes when linger timer expires for partial batch",
			run: func(t *testing.T) {
				var mu sync.Mutex
				var receivedBatches [][]int

				batcher := jobqueue.NewBatcher(jobqueue.BatcherConfig[int]{
					BatchSize:  10,
					Linger:     30 * time.Millisecond,
					BufferSize: 10,
					FlushFunc: func(ctx context.Context, batch []int) error {
						mu.Lock()
						defer mu.Unlock()
						receivedBatches = append(receivedBatches, batch)
						return nil
					},
				})
				defer func() { _ = batcher.Close() }()

				ctx := context.Background()
				_ = batcher.Push(ctx, 1)
				_ = batcher.Push(ctx, 2)

				// Wait for linger to trigger
				time.Sleep(60 * time.Millisecond)

				mu.Lock()
				defer mu.Unlock()
				if len(receivedBatches) != 1 {
					t.Fatalf("expected 1 batch via linger, got %d", len(receivedBatches))
				}
				if len(receivedBatches[0]) != 2 {
					t.Fatalf("expected 2 items, got %d", len(receivedBatches[0]))
				}
			},
		},
		{
			name: "manual Flush forces immediate emission",
			run: func(t *testing.T) {
				var mu sync.Mutex
				var flushedCount int

				batcher := jobqueue.NewBatcher(jobqueue.BatcherConfig[int]{
					BatchSize:  100,
					Linger:     10 * time.Second,
					BufferSize: 10,
					FlushFunc: func(ctx context.Context, batch []int) error {
						mu.Lock()
						defer mu.Unlock()
						flushedCount += len(batch)
						return nil
					},
				})
				defer func() { _ = batcher.Close() }()

				ctx := context.Background()
				_ = batcher.Push(ctx, 100)
				_ = batcher.Push(ctx, 200)

				err := batcher.Flush(ctx)
				if err != nil {
					t.Fatalf("Flush returned error: %v", err)
				}

				mu.Lock()
				defer mu.Unlock()
				if flushedCount != 2 {
					t.Fatalf("expected 2 flushed items, got %d", flushedCount)
				}
			},
		},
		{
			name: "non-blocking mode returns ErrQueueFull when buffer is full",
			run: func(t *testing.T) {
				var droppedCount int
				batcher := jobqueue.NewBatcher(jobqueue.BatcherConfig[int]{
					BatchSize:   100,
					Linger:      10 * time.Second,
					BufferSize:  2,
					BlockOnFull: false,
					OnDrop: func(item int) {
						droppedCount++
					},
				})
				defer func() { _ = batcher.Close() }()

				ctx := context.Background()
				_ = batcher.Push(ctx, 1)
				_ = batcher.Push(ctx, 2)

				// Buffer is 2; 3rd push should fail with ErrQueueFull
				err := batcher.Push(ctx, 3)
				if err != jobqueue.ErrQueueFull {
					t.Fatalf("expected ErrQueueFull, got %v", err)
				}

				stats := batcher.Stats()
				if stats.ItemsDropped != 1 {
					t.Fatalf("expected 1 dropped item in stats, got %d", stats.ItemsDropped)
				}
			},
		},
		{
			name: "graceful Close flushes all buffered items",
			run: func(t *testing.T) {
				var mu sync.Mutex
				var totalFlushed int

				batcher := jobqueue.NewBatcher(jobqueue.BatcherConfig[string]{
					BatchSize:  50,
					Linger:     10 * time.Second,
					BufferSize: 100,
					FlushFunc: func(ctx context.Context, batch []string) error {
						mu.Lock()
						defer mu.Unlock()
						totalFlushed += len(batch)
						return nil
					},
				})

				ctx := context.Background()
				for i := 0; i < 25; i++ {
					_ = batcher.Push(ctx, "msg")
				}

				// Close should flush the 25 items before returning
				err := batcher.Close()
				if err != nil {
					t.Fatalf("Close returned error: %v", err)
				}

				mu.Lock()
				defer mu.Unlock()
				if totalFlushed != 25 {
					t.Fatalf("expected 25 flushed items on Close, got %d", totalFlushed)
				}

				// Push after close should return ErrBatcherClosed
				err = batcher.Push(ctx, "after-close")
				if err != jobqueue.ErrBatcherClosed {
					t.Fatalf("expected ErrBatcherClosed, got %v", err)
				}
			},
		},
		{
			name: "blocking Push returns error when context is canceled while buffer full",
			run: func(t *testing.T) {
				batcher := jobqueue.NewBatcher(jobqueue.BatcherConfig[int]{
					BatchSize:   100,
					Linger:      10 * time.Second,
					BufferSize:  1,
					BlockOnFull: true,
				})
				defer func() { _ = batcher.Close() }()

				ctx, cancel := context.WithCancel(context.Background())
				_ = batcher.Push(ctx, 1) // fills buffer of 1

				cancel() // cancel context before second push
				err := batcher.Push(ctx, 2)
				if err != context.Canceled {
					t.Errorf("expected context.Canceled, got %v", err)
				}
			},
		},
		{
			name: "Batcher zero config defaults, double close, and Flush on closed",
			run: func(t *testing.T) {
				// Zero config applies defaults
				batcher := jobqueue.NewBatcher(jobqueue.BatcherConfig[int]{})
				if err := batcher.Close(); err != nil {
					t.Fatalf("first Close error: %v", err)
				}
				// Second Close is no-op
				if err := batcher.Close(); err != nil {
					t.Errorf("second Close error: %v", err)
				}

				// Flush on closed batcher
				if err := batcher.Flush(context.Background()); err != jobqueue.ErrBatcherClosed {
					t.Errorf("expected ErrBatcherClosed, got %v", err)
				}

				// Flush with canceled context on running batcher
				running := jobqueue.NewBatcher(jobqueue.BatcherConfig[int]{})
				defer func() { _ = running.Close() }()

				cancCtx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := running.Flush(cancCtx); err != context.Canceled {
					t.Errorf("expected context.Canceled on Flush, got %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
