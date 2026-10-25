package batcher_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/batcher"
)

func TestBatcher_BasicAndSizeTrigger(t *testing.T) {
	var mu sync.Mutex
	var batches [][]int

	b := batcher.New(func(ctx context.Context, batch []int) error {
		mu.Lock()
		defer mu.Unlock()
		copyBatch := make([]int, len(batch))
		copy(copyBatch, batch)
		batches = append(batches, copyBatch)
		return nil
	},
		batcher.WithMaxBatchSize(3),
		batcher.WithFlushInterval(100*time.Millisecond),
		batcher.WithQueueSize(10),
	)
	defer b.Stop()

	ctx := context.Background()
	for i := 1; i <= 6; i++ {
		if err := b.Submit(ctx, i); err != nil {
			t.Fatalf("failed to submit %d: %v", i, err)
		}
	}

	// Wait briefly for the 2 batches of size 3 to be dispatched
	time.Sleep(30 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 2 {
		t.Fatalf("expected 2 batches of size 3, got %d", len(batches))
	}
	if len(batches[0]) != 3 || len(batches[1]) != 3 {
		t.Errorf("expected batches of size 3, got: %+v", batches)
	}

	stats := b.Stats()
	if stats.Submitted != 6 || stats.Flushed != 6 || stats.Batches != 2 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}

func TestBatcher_IntervalTriggerAndManualFlush(t *testing.T) {
	var mu sync.Mutex
	var received []string

	b := batcher.New(func(ctx context.Context, batch []string) error {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, batch...)
		return nil
	},
		batcher.WithMaxBatchSize(100),
		batcher.WithFlushInterval(20*time.Millisecond),
	)

	ctx := context.Background()
	_ = b.Submit(ctx, "a")
	_ = b.Submit(ctx, "b")

	// Flush manually
	if err := b.Flush(ctx); err != nil {
		t.Fatalf("manual flush failed: %v", err)
	}

	mu.Lock()
	if len(received) != 2 {
		t.Errorf("expected 2 items after manual flush, got %d", len(received))
	}
	mu.Unlock()

	// Another item flushed by interval ticker
	_ = b.Submit(ctx, "c")
	time.Sleep(40 * time.Millisecond)

	mu.Lock()
	if len(received) != 3 {
		t.Errorf("expected 3 items after interval tick, got %d", len(received))
	}
	mu.Unlock()

	b.Stop()
}

func TestBatcher_BackpressureStrategies(t *testing.T) {
	t.Run("BackpressureReject", func(t *testing.T) {
		blockCh := make(chan struct{})
		b := batcher.New(func(ctx context.Context, batch []int) error {
			<-blockCh
			return nil
		},
			batcher.WithMaxBatchSize(100),
			batcher.WithQueueSize(2),
			batcher.WithFlushInterval(1*time.Hour),
			batcher.WithBackpressure(batcher.BackpressureReject),
		)
		defer func() {
			close(blockCh)
			b.Stop()
		}()

		ctx := context.Background()
		// Fill queue
		_ = b.Submit(ctx, 1)
		_ = b.Submit(ctx, 2)

		// Next submit should be rejected with ErrQueueFull
		err := b.Submit(ctx, 3)
		if !errors.Is(err, batcher.ErrQueueFull) {
			t.Errorf("expected ErrQueueFull, got: %v", err)
		}
		if b.Stats().Dropped == 0 {
			t.Errorf("expected dropped count > 0")
		}
	})

	t.Run("BackpressureDropOldest", func(t *testing.T) {
		blockCh := make(chan struct{})
		b := batcher.New(func(ctx context.Context, batch []int) error {
			<-blockCh
			return nil
		},
			batcher.WithMaxBatchSize(100),
			batcher.WithQueueSize(2),
			batcher.WithFlushInterval(1*time.Hour),
			batcher.WithBackpressure(batcher.BackpressureDropOldest),
		)
		defer func() {
			close(blockCh)
			b.Stop()
		}()

		ctx := context.Background()
		_ = b.Submit(ctx, 1)
		_ = b.Submit(ctx, 2)
		// Should succeed by dropping oldest
		err := b.Submit(ctx, 3)
		if err != nil {
			t.Errorf("expected success with DropOldest, got: %v", err)
		}
		if b.Stats().Dropped == 0 {
			t.Errorf("expected dropped count > 0")
		}
	})
}

func TestBatcher_ContextCancellationAndClosed(t *testing.T) {
	blockCh := make(chan struct{})
	b := batcher.New(func(ctx context.Context, batch []int) error {
		<-blockCh
		return nil
	},
		batcher.WithMaxBatchSize(10),
		batcher.WithQueueSize(1),
		batcher.WithFlushInterval(1*time.Hour),
		batcher.WithBackpressure(batcher.BackpressureBlock),
	)

	ctx, cancel := context.WithCancel(context.Background())
	_ = b.Submit(ctx, 1) // fills queue

	// Cancel context during submit
	cancel()
	err := b.Submit(ctx, 2)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	// Cancel context on Flush
	if err := b.Flush(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Flush, got: %v", err)
	}

	close(blockCh)
	b.Stop()

	// Calling submit on stopped batcher
	if err := b.Submit(context.Background(), 3); !errors.Is(err, batcher.ErrBatcherClosed) {
		t.Errorf("expected ErrBatcherClosed, got: %v", err)
	}
	if err := b.Flush(context.Background()); !errors.Is(err, batcher.ErrBatcherClosed) {
		t.Errorf("expected ErrBatcherClosed on Flush, got: %v", err)
	}
}

func TestBatcher_RetryAndPanicRecovery(t *testing.T) {
	t.Run("retries on error", func(t *testing.T) {
		var attempts atomic.Int64
		errCustom := errors.New("temporary failure")

		b := batcher.New(func(ctx context.Context, batch []int) error {
			if attempts.Add(1) < 3 {
				return errCustom
			}
			return nil
		},
			batcher.WithMaxBatchSize(1),
			batcher.WithRetries(3, 5*time.Millisecond),
		)
		defer b.Stop()

		_ = b.Submit(context.Background(), 42)
		time.Sleep(50 * time.Millisecond)

		if attempts.Load() != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts.Load())
		}
		if b.Stats().Errors != 0 {
			t.Errorf("expected 0 errors after successful retry, got %d", b.Stats().Errors)
		}
	})

	t.Run("panic recovery in batch func", func(t *testing.T) {
		b := batcher.New(func(ctx context.Context, batch []int) error {
			panic("unexpected failure in worker")
		},
			batcher.WithMaxBatchSize(1),
		)
		defer b.Stop()

		_ = b.Submit(context.Background(), 1)
		time.Sleep(30 * time.Millisecond)

		if b.Stats().Errors != 1 {
			t.Errorf("expected 1 error recorded from panic, got %d", b.Stats().Errors)
		}
	})
}

func TestBatcher_HighConcurrency(t *testing.T) {
	var totalReceived atomic.Int64

	b := batcher.New(func(ctx context.Context, batch []int) error {
		totalReceived.Add(int64(len(batch)))
		return nil
	},
		batcher.WithMaxBatchSize(25),
		batcher.WithWorkers(4),
		batcher.WithFlushInterval(15*time.Millisecond),
		batcher.WithQueueSize(500),
	)

	const goroutines = 20
	const itemsPerGoroutine = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < itemsPerGoroutine; i++ {
				_ = b.Submit(context.Background(), id*1000+i)
			}
		}(g)
	}

	wg.Wait()
	b.Stop()

	if totalReceived.Load() != int64(goroutines*itemsPerGoroutine) {
		t.Errorf("expected %d items received, got %d", goroutines*itemsPerGoroutine, totalReceived.Load())
	}
}

func TestDataLoader_CoalescingAndCache(t *testing.T) {
	var batchCalls atomic.Int64

	dl := batcher.NewDataLoader(func(ctx context.Context, keys []string) (map[string]int, error) {
		batchCalls.Add(1)
		res := make(map[string]int)
		for _, k := range keys {
			res[k] = len(k)
		}
		return res, nil
	},
		batcher.WithDataLoaderBatchSize(10),
		batcher.WithDataLoaderWait(15*time.Millisecond),
		batcher.WithDataLoaderCache(true),
	)
	defer dl.Close()

	ctx := context.Background()

	// Concurrent loads for multiple keys
	keys := []string{"apple", "banana", "cherry", "apple", "banana"}
	var wg sync.WaitGroup
	results := make([]int, len(keys))

	wg.Add(len(keys))
	for i, k := range keys {
		idx := i
		key := k
		go func() {
			defer wg.Done()
			val, err := dl.Load(ctx, key)
			if err != nil {
				t.Errorf("unexpected error loading %s: %v", key, err)
			}
			results[idx] = val
		}()
	}

	wg.Wait()

	if batchCalls.Load() != 1 {
		t.Errorf("expected exactly 1 batch execution for coalesced calls, got %d", batchCalls.Load())
	}

	if results[0] != 5 || results[1] != 6 || results[2] != 6 {
		t.Errorf("unexpected results: %+v", results)
	}

	// Subsequent load should hit cache without increasing batch calls
	val, err := dl.Load(ctx, "apple")
	if err != nil || val != 5 {
		t.Errorf("expected 5 from cache, got %d (%v)", val, err)
	}
	if batchCalls.Load() != 1 {
		t.Errorf("expected cache hit to not increase batch calls, got %d", batchCalls.Load())
	}

	// Invalidate single key
	dl.Clear("apple")
	val, err = dl.Load(ctx, "apple")
	if err != nil || val != 5 {
		t.Errorf("expected 5 after reload, got %d (%v)", val, err)
	}
	if batchCalls.Load() != 2 {
		t.Errorf("expected 2 batch calls after cache clear, got %d", batchCalls.Load())
	}

	// ClearAll and Prime
	dl.ClearAll()
	dl.Prime("grape", 99)
	val, _ = dl.Load(ctx, "grape")
	if val != 99 {
		t.Errorf("expected primed value 99, got %d", val)
	}
}

func TestDataLoader_ErrorsAndPanics(t *testing.T) {
	t.Run("missing key in result map", func(t *testing.T) {
		dl := batcher.NewDataLoader(func(ctx context.Context, keys []string) (map[string]int, error) {
			return map[string]int{"found": 1}, nil
		},
			batcher.WithDataLoaderWait(5*time.Millisecond),
		)
		defer dl.Close()

		_, err := dl.Load(context.Background(), "missing")
		if !errors.Is(err, batcher.ErrKeyNotFound) {
			t.Errorf("expected ErrKeyNotFound, got: %v", err)
		}
	})

	t.Run("batch function error distributes to all callers", func(t *testing.T) {
		errDB := errors.New("db down")
		dl := batcher.NewDataLoader(func(ctx context.Context, keys []int) (map[int]string, error) {
			return nil, errDB
		},
			batcher.WithDataLoaderWait(5*time.Millisecond),
		)
		defer dl.Close()

		vals, errs := dl.LoadMany(context.Background(), []int{1, 2, 3})
		if len(vals) != 3 || len(errs) != 3 {
			t.Fatalf("expected 3 items")
		}
		for _, err := range errs {
			if !errors.Is(err, errDB) {
				t.Errorf("expected errDB, got: %v", err)
			}
		}
	})

	t.Run("panic in dataloader function", func(t *testing.T) {
		dl := batcher.NewDataLoader(func(ctx context.Context, keys []int) (map[int]string, error) {
			panic("boom")
		},
			batcher.WithDataLoaderWait(5*time.Millisecond),
		)
		defer dl.Close()

		_, err := dl.Load(context.Background(), 1)
		if err == nil {
			t.Errorf("expected error from panic, got nil")
		}
	})
}

func TestNilConstructorPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil batcher fn")
		}
	}()
	_ = batcher.New[int](nil)
}

func TestDataLoaderNilConstructorPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil dataloader fn")
		}
	}()
	_ = batcher.NewDataLoader[int, int](nil)
}

func TestDataLoader_EdgeCases(t *testing.T) {
	dl := batcher.NewDataLoader(func(ctx context.Context, keys []int) (map[int]int, error) {
		time.Sleep(20 * time.Millisecond)
		res := make(map[int]int)
		for _, k := range keys {
			res[k] = k * 10
		}
		return res, nil
	},
		batcher.WithDataLoaderWait(10*time.Millisecond),
		batcher.WithDataLoaderCache(false),
	)

	// Context cancellation
	cancCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := dl.Load(cancCtx, 1)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	// Normal load with cache disabled
	val, err := dl.Load(context.Background(), 2)
	if err != nil || val != 20 {
		t.Errorf("expected 20, got %d (%v)", val, err)
	}

	dl.Close()

	// Calling Load on closed DataLoader
	_, err = dl.Load(context.Background(), 2)
	if !errors.Is(err, batcher.ErrBatcherClosed) {
		t.Errorf("expected ErrBatcherClosed, got: %v", err)
	}
}
