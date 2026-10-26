package pipeline_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/pipeline"
)

func TestRecord(t *testing.T) {
	t.Parallel()

	rec := pipeline.NewRecord("payload-1")
	if rec.Value != "payload-1" {
		t.Fatalf("expected payload-1, got %v", rec.Value)
	}
	if rec.ID == "" {
		t.Fatal("expected generated ID")
	}

	custom := rec.WithID("custom-id").WithHeader("X-Trace", "abc").WithHeader("Env", "prod")
	if custom.ID != "custom-id" {
		t.Fatalf("expected custom-id, got %s", custom.ID)
	}
	if custom.Headers["X-Trace"] != "abc" || custom.Headers["Env"] != "prod" {
		t.Fatalf("unexpected headers: %v", custom.Headers)
	}

	errSample := errors.New("sample error")
	withErr := custom.WithError(errSample)
	if !errors.Is(withErr.Error, errSample) {
		t.Fatalf("expected %v, got %v", errSample, withErr.Error)
	}

	cloned := withErr.Clone()
	cloned = cloned.WithHeader("New", "Val")
	if _, ok := withErr.Headers["New"]; ok {
		t.Fatal("clone headers should be independent")
	}
}

func TestStageError(t *testing.T) {
	t.Parallel()

	var nilErr *pipeline.StageError
	if nilErr.Error() != "<nil>" {
		t.Fatalf("expected <nil>, got %s", nilErr.Error())
	}
	if nilErr.Unwrap() != nil {
		t.Fatal("expected nil unwrapped error")
	}

	rootErr := errors.New("network timeout")
	stageErr := &pipeline.StageError{
		StageName: "enricher",
		Item:      "data-1",
		Err:       rootErr,
	}
	if stageErr.Error() != `pipeline stage "enricher" failed: network timeout` {
		t.Fatalf("unexpected error message: %s", stageErr.Error())
	}
	if !errors.Is(stageErr, rootErr) {
		t.Fatal("expected stageErr to unwrap to rootErr")
	}

	noNameErr := &pipeline.StageError{Err: rootErr}
	if noNameErr.Error() != "pipeline stage failed: network timeout" {
		t.Fatalf("unexpected error message: %s", noNameErr.Error())
	}
}

func TestDLQ(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// MemoryDeadLetterQueue
	dlq := pipeline.NewMemoryDeadLetterQueue(3)
	if dlq.Len() != 0 {
		t.Fatalf("expected 0 items, got %d", dlq.Len())
	}

	for i := 1; i <= 4; i++ {
		err := dlq.HandleDeadLetter(ctx, pipeline.DeadLetterEntry{
			StageName: "test-stage",
			Item:      fmt.Sprintf("item-%d", i),
			Err:       errors.New("failed"),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Oldest item-1 should be evicted (capacity 3)
	if dlq.Len() != 3 {
		t.Fatalf("expected 3 items, got %d", dlq.Len())
	}
	entries := dlq.Entries()
	if entries[0].Item != "item-2" || entries[2].Item != "item-4" {
		t.Fatalf("unexpected entries: %v", entries)
	}

	drained := dlq.Drain()
	if len(drained) != 3 || dlq.Len() != 0 {
		t.Fatalf("expected drained 3 items, dlq empty: %d", dlq.Len())
	}

	_ = dlq.HandleDeadLetter(ctx, pipeline.DeadLetterEntry{Item: "a"})
	dlq.Clear()
	if dlq.Len() != 0 {
		t.Fatal("expected 0 after clear")
	}

	// DiscardDeadLetterSink
	discard := pipeline.DiscardDeadLetterSink{}
	if err := discard.HandleDeadLetter(ctx, pipeline.DeadLetterEntry{}); err != nil {
		t.Fatalf("expected nil from discard sink, got %v", err)
	}

	// DeadLetterFunc
	var funcCalled bool
	fnSink := pipeline.DeadLetterFunc(func(c context.Context, e pipeline.DeadLetterEntry) error {
		funcCalled = true
		return nil
	})
	_ = fnSink.HandleDeadLetter(ctx, pipeline.DeadLetterEntry{})
	if !funcCalled {
		t.Fatal("expected DeadLetterFunc to be invoked")
	}

	// ChanDeadLetterSink
	ch := make(chan pipeline.DeadLetterEntry, 1)
	chanSink := pipeline.NewChanDeadLetterSink(ch)
	_ = chanSink.HandleDeadLetter(ctx, pipeline.DeadLetterEntry{StageName: "chan-test"})
	rec := <-ch
	if rec.StageName != "chan-test" {
		t.Fatalf("expected chan-test, got %s", rec.StageName)
	}

	// ChanDeadLetterSink with cancelled context
	cancCtx, cancel := context.WithCancel(context.Background())
	cancel()
	errCanc := chanSink.HandleDeadLetter(cancCtx, pipeline.DeadLetterEntry{})
	if !errors.Is(errCanc, context.Canceled) {
		t.Fatalf("expected Canceled error, got %v", errCanc)
	}
}

func TestFanUtilities(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// FanIn
	emptyFanIn := pipeline.FanIn[int](ctx)
	if _, ok := <-emptyFanIn; ok {
		t.Fatal("expected immediate close on empty fan-in")
	}

	ch1 := make(chan int, 2)
	ch2 := make(chan int, 2)
	ch1 <- 10
	ch1 <- 20
	close(ch1)
	ch2 <- 30
	ch2 <- 40
	close(ch2)

	merged := pipeline.FanIn(ctx, ch1, ch2)
	var sum int
	for val := range merged {
		sum += val
	}
	if sum != 100 {
		t.Fatalf("expected sum 100, got %d", sum)
	}

	// FanOut
	in := make(chan string, 4)
	for _, s := range []string{"a", "b", "c", "d"} {
		in <- s
	}
	close(in)

	outs := pipeline.FanOut(ctx, in, 2, 2)
	if len(outs) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(outs))
	}

	var results []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, out := range outs {
		wg.Add(1)
		go func(c <-chan string) {
			defer wg.Done()
			for v := range c {
				mu.Lock()
				results = append(results, v)
				mu.Unlock()
			}
		}(out)
	}
	wg.Wait()
	if len(results) != 4 {
		t.Fatalf("expected 4 results across fan out, got %d", len(results))
	}

	// Broadcast
	bIn := make(chan int, 2)
	bIn <- 99
	close(bIn)

	bOuts := pipeline.Broadcast(ctx, bIn, 3, 2)
	for i, bo := range bOuts {
		val := <-bo
		if val != 99 {
			t.Fatalf("output %d expected 99, got %d", i, val)
		}
		if _, ok := <-bo; ok {
			t.Fatal("expected closed broadcast channel")
		}
	}
}

func TestStages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Map with retry
	var attempts int
	mapStage := pipeline.Map("flaky", func(c context.Context, item int) (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("temporary error")
		}
		return fmt.Sprintf("val-%d", item), nil
	}, pipeline.WithRetry(3, 5*time.Millisecond))

	inCh := make(chan int, 1)
	outCh := make(chan string, 1)
	inCh <- 42
	close(inCh)

	err := mapStage.Run(ctx, inCh, outCh)
	if err != nil {
		t.Fatalf("expected retry success, got error: %v", err)
	}
	res := <-outCh
	if res != "val-42" {
		t.Fatalf("expected val-42, got %s", res)
	}

	// Map panic recovery
	panicMap := pipeline.Map("panic-stage", func(c context.Context, item int) (int, error) {
		panic("boom")
	})
	pIn := make(chan int, 1)
	pOut := make(chan int, 1)
	pIn <- 1
	close(pIn)
	err = panicMap.Run(ctx, pIn, pOut)
	if !errors.Is(err, pipeline.ErrStagePanicked) {
		t.Fatalf("expected ErrStagePanicked, got %v", err)
	}

	// Map DLQ routing
	dlq := pipeline.NewMemoryDeadLetterQueue(10)
	failMap := pipeline.Map("fail-map", func(c context.Context, item int) (int, error) {
		return 0, errors.New("rejected")
	}, pipeline.WithStageDLQ(dlq))

	fIn := make(chan int, 1)
	fOut := make(chan int, 1)
	fIn <- 100
	close(fIn)
	err = failMap.Run(ctx, fIn, fOut)
	if err != nil {
		t.Fatalf("expected nil error when DLQ handled, got %v", err)
	}
	if dlq.Len() != 1 {
		t.Fatalf("expected 1 dead letter entry, got %d", dlq.Len())
	}

	// Filter
	filterStage := pipeline.Filter("evens", func(c context.Context, item int) bool {
		return item%2 == 0
	})
	filtIn := make(chan int, 4)
	filtOut := make(chan int, 4)
	filtIn <- 1
	filtIn <- 2
	filtIn <- 3
	filtIn <- 4
	close(filtIn)

	_ = filterStage.Run(ctx, filtIn, filtOut)
	close(filtOut)
	var evens []int
	for v := range filtOut {
		evens = append(evens, v)
	}
	if len(evens) != 2 || evens[0] != 2 || evens[1] != 4 {
		t.Fatalf("unexpected filter output: %v", evens)
	}

	// FlatMap
	flatMap := pipeline.FlatMap("duplicator", func(c context.Context, item string) ([]string, error) {
		return []string{item + "-1", item + "-2"}, nil
	})
	fmIn := make(chan string, 1)
	fmOut := make(chan string, 2)
	fmIn <- "item"
	close(fmIn)
	_ = flatMap.Run(ctx, fmIn, fmOut)
	close(fmOut)
	var fmResults []string
	for v := range fmOut {
		fmResults = append(fmResults, v)
	}
	if len(fmResults) != 2 || fmResults[0] != "item-1" || fmResults[1] != "item-2" {
		t.Fatalf("unexpected flatmap output: %v", fmResults)
	}

	// Batch and Unbatch
	batchStage := pipeline.Batch[int]("batcher", 3, 20*time.Millisecond)
	bIn := make(chan int, 5)
	bOut := make(chan []int, 2)
	for i := 1; i <= 5; i++ {
		bIn <- i
	}
	close(bIn)
	_ = batchStage.Run(ctx, bIn, bOut)
	close(bOut)

	var batches [][]int
	for b := range bOut {
		batches = append(batches, b)
	}
	if len(batches) != 2 || len(batches[0]) != 3 || len(batches[1]) != 2 {
		t.Fatalf("unexpected batching: %v", batches)
	}

	unbatchStage := pipeline.Unbatch[int]("unbatcher")
	ubIn := make(chan []int, len(batches))
	for _, b := range batches {
		ubIn <- b
	}
	close(ubIn)
	ubOut := make(chan int, 5)
	_ = unbatchStage.Run(ctx, ubIn, ubOut)
	close(ubOut)

	var unbatched []int
	for u := range ubOut {
		unbatched = append(unbatched, u)
	}
	if len(unbatched) != 5 {
		t.Fatalf("expected 5 unbatched items, got %v", unbatched)
	}

	// Parallel stage
	parallelStage := pipeline.Parallel("parallel-worker", 4, func(c context.Context, item int) (int, error) {
		return item * 2, nil
	}, pipeline.WithConcurrency(4))

	parIn := make(chan int, 10)
	parOut := make(chan int, 10)
	for i := 1; i <= 10; i++ {
		parIn <- i
	}
	close(parIn)
	_ = parallelStage.Run(ctx, parIn, parOut)
	close(parOut)

	var parSum int
	for v := range parOut {
		parSum += v
	}
	// sum(1..10) * 2 = 55 * 2 = 110
	if parSum != 110 {
		t.Fatalf("expected parallel sum 110, got %d", parSum)
	}

	// Throttle stage
	throttleStage := pipeline.Throttle[int]("throttler", 10*time.Millisecond)
	thIn := make(chan int, 2)
	thOut := make(chan int, 2)
	thIn <- 1
	thIn <- 2
	close(thIn)
	start := time.Now()
	_ = throttleStage.Run(ctx, thIn, thOut)
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("expected throttled duration >= 15ms, took %v", elapsed)
	}
}

func TestWindows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// TumblingWindow
	tumbler := pipeline.TumblingWindow("tumbler", 50*time.Millisecond, func(c context.Context, win pipeline.Window[int]) (int, error) {
		var sum int
		for _, item := range win.Items {
			sum += item
		}
		return sum, nil
	})

	tIn := make(chan int, 5)
	tOut := make(chan int, 2)
	for i := 1; i <= 5; i++ {
		tIn <- i
	}
	close(tIn)

	_ = tumbler.Run(ctx, tIn, tOut)
	close(tOut)

	var winSum int
	for s := range tOut {
		winSum += s
	}
	if winSum != 15 {
		t.Fatalf("expected window sum 15, got %d", winSum)
	}

	// SlidingWindow
	slider := pipeline.SlidingWindow("slider", 100*time.Millisecond, 30*time.Millisecond,
		func(c context.Context, win pipeline.Window[string]) (int, error) {
			return len(win.Items), nil
		},
		pipeline.WithEmitEmptyWindows[string](false),
	)

	sIn := make(chan string, 3)
	sOut := make(chan int, 5)
	sIn <- "a"
	sIn <- "b"
	sIn <- "c"
	close(sIn)

	_ = slider.Run(ctx, sIn, sOut)
	close(sOut)

	var countResults []int
	for c := range sOut {
		countResults = append(countResults, c)
	}
	if len(countResults) == 0 {
		t.Fatal("expected at least one sliding window result")
	}
}

func TestPipelineEndToEnd(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dlq := pipeline.NewMemoryDeadLetterQueue(100)

	// Build multi-stage ETL pipeline:
	// int -> (Map + 10) -> (Filter evens) -> (Parallel * 2) -> (Map string) -> string
	p := pipeline.New[int, string]("etl-pipeline",
		pipeline.WithPipelineBufferSize[int, string](32),
		pipeline.WithPipelineDLQ[int, string](dlq),
	)

	if p.Name() != "etl-pipeline" {
		t.Fatalf("expected etl-pipeline, got %s", p.Name())
	}

	pipeline.AddStage(p, pipeline.Map("add-ten", func(c context.Context, item int) (int, error) {
		return item + 10, nil
	}))

	pipeline.AddStage(p, pipeline.Filter("keep-evens", func(c context.Context, item int) bool {
		return item%2 == 0
	}))

	pipeline.AddStage(p, pipeline.Parallel("double-parallel", 4, func(c context.Context, item int) (int, error) {
		return item * 2, nil
	}))

	pipeline.AddStage(p, pipeline.Map("to-string", func(c context.Context, item int) (string, error) {
		return fmt.Sprintf("OUT-%d", item), nil
	}))

	input := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	// Add 10: 11, 12, 13, 14, 15, 16, 17, 18, 19, 20
	// Keep evens: 12, 14, 16, 18, 20 (5 items)
	// Multiply 2: 24, 28, 32, 36, 40
	// to-string: "OUT-24", ...

	results, err := p.RunSlice(ctx, input)
	if err != nil {
		t.Fatalf("unexpected pipeline failure: %v", err)
	}

	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d: %v", len(results), results)
	}

	stats := p.Stats()
	if stats.TotalIn == 0 {
		t.Fatal("expected non-zero total in stats")
	}
	if len(stats.Stages) != 4 {
		t.Fatalf("expected 4 stages in stats, got %d", len(stats.Stages))
	}

	// Sink helper
	var sinkItems []string
	var sinkMu sync.Mutex
	inCh := make(chan int, len(input))
	for _, it := range input {
		inCh <- it
	}
	close(inCh)

	err = p.Sink(ctx, inCh, func(c context.Context, item string) error {
		sinkMu.Lock()
		sinkItems = append(sinkItems, item)
		sinkMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("sink error: %v", err)
	}
	if len(sinkItems) != 5 {
		t.Fatalf("expected 5 sink items, got %d", len(sinkItems))
	}
}

func TestPipelineZeroStages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := pipeline.New[string, string]("identity-pipe")

	results, err := p.RunSlice(ctx, []string{"alpha", "beta", "gamma"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 || results[0] != "alpha" {
		t.Fatalf("unexpected zero-stage output: %v", results)
	}
}

func TestPipelineFailFast(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := pipeline.New[int, int]("failing-pipe")

	fatalErr := errors.New("unrecoverable database failure")
	pipeline.AddStage(p, pipeline.Map("fatal-stage", func(c context.Context, item int) (int, error) {
		if item == 3 {
			return 0, fatalErr
		}
		return item, nil
	}))

	_, err := p.RunSlice(ctx, []int{1, 2, 3, 4, 5})
	if !errors.Is(err, fatalErr) {
		t.Fatalf("expected fatalErr, got %v", err)
	}
}

func TestPipelineCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	p := pipeline.New[int, int]("cancel-pipe")
	pipeline.AddStage(p, pipeline.Map("slow-stage", func(c context.Context, item int) (int, error) {
		select {
		case <-c.Done():
			return 0, c.Err()
		case <-time.After(100 * time.Millisecond):
			return item, nil
		}
	}))

	in := make(chan int, 5)
	for i := 1; i <= 5; i++ {
		in <- i
	}
	close(in)

	out, _ := p.Run(ctx, in)

	// Cancel after brief moment
	time.Sleep(20 * time.Millisecond)
	cancel()

	// Drain output
	for range out {
	}
}

func TestConcurrentStress(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p := pipeline.New[int, int]("stress-pipe", pipeline.WithPipelineBufferSize[int, int](64))

	pipeline.AddStage(p, pipeline.Parallel("parallel-stage", 8, func(c context.Context, item int) (int, error) {
		return item * 3, nil
	}))

	totalItems := 500
	items := make([]int, totalItems)
	for i := 0; i < totalItems; i++ {
		items[i] = i
	}

	results, err := p.RunSlice(ctx, items)
	if err != nil {
		t.Fatalf("stress pipeline failed: %v", err)
	}
	if len(results) != totalItems {
		t.Fatalf("expected %d items, got %d", totalItems, len(results))
	}

	var sum int64
	for _, v := range results {
		atomic.AddInt64(&sum, int64(v))
	}

	// Sum(0..499) * 3 = (499 * 500 / 2) * 3 = 124750 * 3 = 374250
	expected := int64(499 * 500 / 2 * 3)
	if sum != expected {
		t.Fatalf("expected sum %d, got %d", expected, sum)
	}
}

type dummyRunner struct {
	name string
	err  error
}

func (d *dummyRunner) Name() string { return d.name }
func (d *dummyRunner) RunUntyped(ctx context.Context, in <-chan any, out chan<- any, sm *pipeline.StageMetrics, dlq pipeline.DeadLetterSink) error {
	for range in {
	}
	return d.err
}

func TestPipelineOptionsAndEdgeCases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// WithPipelineName
	p := pipeline.New[int, int]("my-pipe",
		pipeline.WithPipelineName[int, int]("renamed-pipe"),
		pipeline.WithPipelineBufferSize[int, int](8),
	)
	if p.Name() != "renamed-pipe" {
		t.Fatalf("expected renamed-pipe, got %s", p.Name())
	}

	// Direct Add() with custom runner
	p2 := pipeline.New[any, any]("custom-pipe")
	p2.Add(&dummyRunner{name: "dummy"})
	res, err := p2.RunSlice(ctx, []any{"item1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("expected empty result from dummy sink, got %v", res)
	}

	// Zero stage with type mismatch
	pZero := pipeline.New[int, string]("zero-mismatch")
	_, errZero := pZero.RunSlice(ctx, []int{123})
	if !errors.Is(errZero, pipeline.ErrInvalidConfiguration) {
		t.Fatalf("expected ErrInvalidConfiguration, got %v", errZero)
	}

	// Stage type assertion mismatch without DLQ
	pTypeFail := pipeline.New[string, int]("type-fail")
	pipeline.AddStage(pTypeFail, pipeline.Map("int-stage", func(c context.Context, item int) (int, error) {
		return item, nil
	}))
	_, errType := pTypeFail.RunSlice(ctx, []string{"not-an-int"})
	if !errors.Is(errType, pipeline.ErrInvalidConfiguration) {
		t.Fatalf("expected ErrInvalidConfiguration, got %v", errType)
	}

	// Stage type assertion mismatch with DLQ
	dlq := pipeline.NewMemoryDeadLetterQueue(5)
	pTypeDLQ := pipeline.New[string, int]("type-dlq", pipeline.WithPipelineDLQ[string, int](dlq))
	pipeline.AddStage(pTypeDLQ, pipeline.Map("int-stage-dlq", func(c context.Context, item int) (int, error) {
		return item, nil
	}))
	_, _ = pTypeDLQ.RunSlice(ctx, []string{"bad-item"})
	if dlq.Len() != 1 {
		t.Fatalf("expected 1 dead letter entry from type mismatch, got %d", dlq.Len())
	}

	// Sink returning error
	pSink := pipeline.New[int, int]("sink-err")
	pipeline.AddStage(pSink, pipeline.Map("identity", func(c context.Context, i int) (int, error) {
		return i, nil
	}))
	sinkErr := errors.New("sink disk full")
	inCh := make(chan int, 1)
	inCh <- 10
	close(inCh)
	err = pSink.Sink(ctx, inCh, func(c context.Context, item int) error {
		return sinkErr
	})
	if !errors.Is(err, sinkErr) {
		t.Fatalf("expected sinkErr, got %v", err)
	}
}

func TestStageEdgeCases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// WithBufferSize
	mapWithBuf := pipeline.Map("buf-stage", func(c context.Context, item int) (int, error) {
		return item * 2, nil
	}, pipeline.WithBufferSize(16))
	if mapWithBuf.Name() != "buf-stage" {
		t.Fatalf("expected buf-stage, got %s", mapWithBuf.Name())
	}

	// FlatMap error and panic
	fmErr := pipeline.FlatMap("fm-err", func(c context.Context, item int) ([]int, error) {
		return nil, errors.New("fm fail")
	})
	fmIn := make(chan int, 1)
	fmOut := make(chan int, 1)
	fmIn <- 1
	close(fmIn)
	err := fmErr.Run(ctx, fmIn, fmOut)
	if err == nil {
		t.Fatal("expected error from FlatMap")
	}

	fmPanic := pipeline.FlatMap("fm-panic", func(c context.Context, item int) ([]int, error) {
		panic("flatmap panic")
	})
	fmInP := make(chan int, 1)
	fmOutP := make(chan int, 1)
	fmInP <- 1
	close(fmInP)
	err = fmPanic.Run(ctx, fmInP, fmOutP)
	if !errors.Is(err, pipeline.ErrStagePanicked) {
		t.Fatalf("expected ErrStagePanicked, got %v", err)
	}

	// FlatMap with DLQ
	dlq := pipeline.NewMemoryDeadLetterQueue(5)
	fmDLQ := pipeline.FlatMap("fm-dlq", func(c context.Context, item int) ([]int, error) {
		return nil, errors.New("dlq rejected")
	}, pipeline.WithStageDLQ(dlq))
	fmInD := make(chan int, 1)
	fmOutD := make(chan int, 1)
	fmInD <- 99
	close(fmInD)
	err = fmDLQ.Run(ctx, fmInD, fmOutD)
	if err != nil {
		t.Fatalf("expected nil error with DLQ, got %v", err)
	}
	if dlq.Len() != 1 {
		t.Fatalf("expected 1 item in DLQ, got %d", dlq.Len())
	}

	// Filter panic recovery
	filterPanic := pipeline.Filter("filter-panic", func(c context.Context, item int) bool {
		panic("filter panic")
	})
	fpIn := make(chan int, 1)
	fpOut := make(chan int, 1)
	fpIn <- 5
	close(fpIn)
	_ = filterPanic.Run(ctx, fpIn, fpOut)
	close(fpOut)
	var fpCount int
	for range fpOut {
		fpCount++
	}
	if fpCount != 0 {
		t.Fatalf("expected 0 items from panicked filter, got %d", fpCount)
	}

	// Parallel panic recovery
	parPanic := pipeline.Parallel("par-panic", 2, func(c context.Context, item int) (int, error) {
		panic("worker panic")
	})
	ppIn := make(chan int, 1)
	ppOut := make(chan int, 1)
	ppIn <- 1
	close(ppIn)
	err = parPanic.Run(ctx, ppIn, ppOut)
	if !errors.Is(err, pipeline.ErrStagePanicked) {
		t.Fatalf("expected ErrStagePanicked from parallel, got %v", err)
	}

	// Parallel with DLQ
	parDLQ := pipeline.NewMemoryDeadLetterQueue(5)
	parDLQStage := pipeline.Parallel("par-dlq", 2, func(c context.Context, item int) (int, error) {
		return 0, errors.New("worker error")
	}, pipeline.WithStageDLQ(parDLQ))
	pdIn := make(chan int, 1)
	pdOut := make(chan int, 1)
	pdIn <- 42
	close(pdIn)
	err = parDLQStage.Run(ctx, pdIn, pdOut)
	if err != nil {
		t.Fatalf("expected nil error when parallel stage uses DLQ, got %v", err)
	}
	if parDLQ.Len() != 1 {
		t.Fatalf("expected 1 item in DLQ, got %d", parDLQ.Len())
	}

	// Parallel retry with backoff
	var pAttempts int
	parRetry := pipeline.Parallel("par-retry", 1, func(c context.Context, item int) (int, error) {
		pAttempts++
		if pAttempts < 2 {
			return 0, errors.New("retry me")
		}
		return item, nil
	}, pipeline.WithRetry(2, 5*time.Millisecond))
	prIn := make(chan int, 1)
	prOut := make(chan int, 1)
	prIn <- 7
	close(prIn)
	err = parRetry.Run(ctx, prIn, prOut)
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if val := <-prOut; val != 7 {
		t.Fatalf("expected 7, got %d", val)
	}

	// Cancel context during Throttle, Batch, Unbatch
	cancCtx, cancel := context.WithCancel(context.Background())
	cancel()

	thStage := pipeline.Throttle[int]("th-cancel", 100*time.Millisecond)
	chIn := make(chan int, 1)
	chOut := make(chan int, 1)
	chIn <- 1
	close(chIn)
	if errTh := thStage.Run(cancCtx, chIn, chOut); !errors.Is(errTh, context.Canceled) {
		t.Fatalf("expected Canceled error, got %v", errTh)
	}

	bStage := pipeline.Batch[int]("b-cancel", 10, 100*time.Millisecond)
	bInC := make(chan int, 1)
	bOutC := make(chan []int, 1)
	bInC <- 1
	close(bInC)
	if errB := bStage.Run(cancCtx, bInC, bOutC); !errors.Is(errB, context.Canceled) {
		t.Fatalf("expected Canceled error, got %v", errB)
	}

	uStage := pipeline.Unbatch[int]("u-cancel")
	uInC := make(chan []int, 1)
	uOutC := make(chan int, 1)
	uInC <- []int{1, 2}
	close(uInC)
	if errU := uStage.Run(cancCtx, uInC, uOutC); !errors.Is(errU, context.Canceled) {
		t.Fatalf("expected Canceled error, got %v", errU)
	}
}

func TestWindowOptionsAndEdgeCases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// TumblingWindow with error and DLQ
	dlq := pipeline.NewMemoryDeadLetterQueue(5)
	tDLQ := pipeline.TumblingWindow("t-dlq", 20*time.Millisecond,
		func(c context.Context, win pipeline.Window[int]) (int, error) {
			return 0, errors.New("tumbler failed")
		},
		pipeline.WithEmitEmptyWindows[int](false),
	)
	tIn := make(chan int, 1)
	tOut := make(chan int, 1)
	tIn <- 10
	close(tIn)
	err := tDLQ.RunWithContext(ctx, tIn, tOut, nil, dlq)
	if err != nil {
		t.Fatalf("expected nil error when DLQ is present, got %v", err)
	}
	if dlq.Len() != 1 {
		t.Fatalf("expected 1 dead letter entry, got %d", dlq.Len())
	}

	// TumblingWindow error without DLQ
	tFail := pipeline.TumblingWindow("t-fail", 20*time.Millisecond,
		func(c context.Context, win pipeline.Window[int]) (int, error) {
			return 0, errors.New("fatal tumbler")
		},
	)
	tIn2 := make(chan int, 1)
	tOut2 := make(chan int, 1)
	tIn2 <- 20
	close(tIn2)
	err2 := tFail.Run(ctx, tIn2, tOut2)
	if err2 == nil {
		t.Fatal("expected error from tumbler without DLQ")
	}

	// TumblingWindow panic
	tPanic := pipeline.TumblingWindow("t-panic", 20*time.Millisecond,
		func(c context.Context, win pipeline.Window[int]) (int, error) {
			panic("tumbler panic")
		},
	)
	tIn3 := make(chan int, 1)
	tOut3 := make(chan int, 1)
	tIn3 <- 30
	close(tIn3)
	err3 := tPanic.Run(ctx, tIn3, tOut3)
	if !errors.Is(err3, pipeline.ErrStagePanicked) {
		t.Fatalf("expected ErrStagePanicked, got %v", err3)
	}

	// SlidingWindow with custom TimestampExtractor and BufferSize
	slider := pipeline.SlidingWindow("slider-custom", 50*time.Millisecond, 20*time.Millisecond,
		func(c context.Context, win pipeline.Window[pipeline.Record[int]]) (int, error) {
			return len(win.Items), nil
		},
		pipeline.WithTimestampExtractor(func(r pipeline.Record[int]) time.Time {
			return r.Timestamp
		}),
		pipeline.WithWindowBufferSize[pipeline.Record[int]](2), // small buffer to trigger pruning/dropping
		pipeline.WithEmitEmptyWindows[pipeline.Record[int]](true),
	)
	sIn := make(chan pipeline.Record[int], 3)
	sOut := make(chan int, 5)
	sIn <- pipeline.NewRecord(1)
	sIn <- pipeline.NewRecord(2)
	sIn <- pipeline.NewRecord(3)
	close(sIn)
	_ = slider.Run(ctx, sIn, sOut)
	close(sOut)
	var sCount int
	for range sOut {
		sCount++
	}
	if sCount == 0 {
		t.Fatal("expected at least one window")
	}

	// SlidingWindow panic
	sPanic := pipeline.SlidingWindow("s-panic", 50*time.Millisecond, 20*time.Millisecond,
		func(c context.Context, win pipeline.Window[int]) (int, error) {
			panic("sliding panic")
		},
	)
	sInP := make(chan int, 1)
	sOutP := make(chan int, 1)
	sInP <- 1
	close(sInP)
	errS := sPanic.Run(ctx, sInP, sOutP)
	if !errors.Is(errS, pipeline.ErrStagePanicked) {
		t.Fatalf("expected ErrStagePanicked from sliding, got %v", errS)
	}

	// SlidingWindow with DLQ
	sDLQ := pipeline.SlidingWindow("s-dlq", 50*time.Millisecond, 20*time.Millisecond,
		func(c context.Context, win pipeline.Window[int]) (int, error) {
			return 0, errors.New("sliding error")
		},
	)
	sInD := make(chan int, 1)
	sOutD := make(chan int, 1)
	sInD <- 1
	close(sInD)
	dlqS := pipeline.NewMemoryDeadLetterQueue(5)
	errSD := sDLQ.RunWithContext(ctx, sInD, sOutD, nil, dlqS)
	if errSD != nil {
		t.Fatalf("expected nil with DLQ, got %v", errSD)
	}
	if dlqS.Len() != 1 {
		t.Fatalf("expected 1 item in DLQ, got %d", dlqS.Len())
	}
}

func TestDLQAndFanEdgeCases(t *testing.T) {
	t.Parallel()

	// Default capacity in NewMemoryDeadLetterQueue(0)
	dlqDefault := pipeline.NewMemoryDeadLetterQueue(0)
	if dlqDefault == nil {
		t.Fatal("expected non-nil dlq")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// FanOut count <= 0, bufSize < 0
	in := make(chan int, 1)
	in <- 1
	close(in)
	fo := pipeline.FanOut(ctx, in, 0, -1)
	if len(fo) != 1 {
		t.Fatalf("expected 1 output for count <= 0, got %d", len(fo))
	}
	val := <-fo[0]
	if val != 1 {
		t.Fatalf("expected 1, got %d", val)
	}

	// Broadcast count <= 0, bufSize < 0
	bIn := make(chan int, 1)
	bIn <- 5
	close(bIn)
	bc := pipeline.Broadcast(ctx, bIn, -1, -5)
	if len(bc) != 1 {
		t.Fatalf("expected 1 output for count <= 0, got %d", len(bc))
	}
	bVal := <-bc[0]
	if bVal != 5 {
		t.Fatalf("expected 5, got %d", bVal)
	}
}

func TestMoreCoverage(t *testing.T) {
	t.Parallel()

	// Defaults for Batch and Parallel
	defaultBatch := pipeline.Batch[int]("def-batch", 0, 0)
	if defaultBatch.Name() != "def-batch" {
		t.Fatalf("expected def-batch, got %s", defaultBatch.Name())
	}
	defaultPar := pipeline.Parallel("def-par", 0, func(c context.Context, i int) (int, error) {
		return i, nil
	})
	if defaultPar.Name() != "def-par" {
		t.Fatalf("expected def-par, got %s", defaultPar.Name())
	}

	// Output type mismatch in multi-stage pipeline
	ctx := context.Background()
	pMismatch := pipeline.New[int, string]("out-mismatch")
	// stage produces int, pipeline expects string
	pipeline.AddStage(pMismatch, pipeline.Map("returns-int", func(c context.Context, item int) (int, error) {
		return item, nil
	}))
	_, errMismatch := pMismatch.RunSlice(ctx, []int{10})
	if !errors.Is(errMismatch, pipeline.ErrInvalidConfiguration) {
		t.Fatalf("expected ErrInvalidConfiguration for egress type mismatch, got %v", errMismatch)
	}

	// SlidingWindow pruning of expired items
	sliderPrune := pipeline.SlidingWindow("slider-prune", 10*time.Millisecond, 5*time.Millisecond,
		func(c context.Context, win pipeline.Window[int]) (int, error) {
			return len(win.Items), nil
		},
		pipeline.WithTimestampExtractor(func(i int) time.Time {
			// Item 1 was 100ms ago (should be pruned)
			if i == 1 {
				return time.Now().Add(-100 * time.Millisecond)
			}
			return time.Now()
		}),
	)
	spIn := make(chan int, 2)
	spOut := make(chan int, 2)
	spIn <- 1
	spIn <- 2
	close(spIn)
	_ = sliderPrune.Run(ctx, spIn, spOut)
	close(spOut)
	for range spOut {
	}

	// TumblingWindow cancellation during wait
	cancCtx, cancel := context.WithCancel(context.Background())
	cancel()
	tw := pipeline.TumblingWindow("tw-cancel", 100*time.Millisecond, func(c context.Context, win pipeline.Window[int]) (int, error) {
		return 0, nil
	})
	twIn := make(chan int)
	twOut := make(chan int)
	if err := tw.Run(cancCtx, twIn, twOut); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from tw, got %v", err)
	}

	// SlidingWindow cancellation during wait
	sw := pipeline.SlidingWindow("sw-cancel", 100*time.Millisecond, 20*time.Millisecond, func(c context.Context, win pipeline.Window[int]) (int, error) {
		return 0, nil
	})
	swIn := make(chan int)
	swOut := make(chan int)
	if err := sw.Run(cancCtx, swIn, swOut); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from sw, got %v", err)
	}

	// Immediate cancellation in Map, Filter, FlatMap, FanIn
	mStage := pipeline.Map[int, int]("m-cancel", func(c context.Context, item int) (int, error) { return item, nil })
	_ = mStage.Run(cancCtx, make(chan int), make(chan int))

	fStage := pipeline.Filter[int]("f-cancel", func(c context.Context, item int) bool { return true })
	_ = fStage.Run(cancCtx, make(chan int), make(chan int))

	fmStage := pipeline.FlatMap[int, int]("fm-cancel", func(c context.Context, item int) ([]int, error) { return []int{item}, nil })
	_ = fmStage.Run(cancCtx, make(chan int), make(chan int))

	_ = pipeline.FanIn[int](cancCtx, make(chan int))
}
