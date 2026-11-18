package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Pipeline manages an end-to-end streaming execution graph composed of sequential stages.
type Pipeline[In, Out any] struct {
	mu         sync.RWMutex
	name       string
	stages     []StageRunner
	dlq        DeadLetterSink
	bufferSize int
	collector  *metricsCollector
	startTime  time.Time
}

// Option configures Pipeline instances.
type Option[In, Out any] func(*Pipeline[In, Out])

// WithPipelineName sets a descriptive name for the pipeline.
func WithPipelineName[In, Out any](name string) Option[In, Out] {
	return func(p *Pipeline[In, Out]) {
		p.name = name
	}
}

// WithPipelineBufferSize sets the capacity for intermediate channel buffers between stages.
func WithPipelineBufferSize[In, Out any](size int) Option[In, Out] {
	return func(p *Pipeline[In, Out]) {
		p.bufferSize = size
	}
}

// WithPipelineDLQ sets the default fallback DeadLetterSink for all pipeline stages.
func WithPipelineDLQ[In, Out any](dlq DeadLetterSink) Option[In, Out] {
	return func(p *Pipeline[In, Out]) {
		p.dlq = dlq
	}
}

// New constructs a new Pipeline instance.
func New[In, Out any](name string, opts ...Option[In, Out]) *Pipeline[In, Out] {
	p := &Pipeline[In, Out]{
		name:       name,
		stages:     make([]StageRunner, 0),
		bufferSize: 16,
		collector:  newMetricsCollector(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// StageRunner is the interface for pipeline stage execution across dynamic generic types.
type StageRunner interface {
	Name() string
	RunUntyped(ctx context.Context, in <-chan any, out chan<- any, sm *StageMetrics, dlq DeadLetterSink) error
}

type typedStageAdapter[In, Out any] struct {
	stage Stage[In, Out]
}

func (a *typedStageAdapter[In, Out]) Name() string {
	return a.stage.Name()
}

func (a *typedStageAdapter[In, Out]) RunUntyped(
	ctx context.Context,
	in <-chan any,
	out chan<- any,
	sm *StageMetrics,
	dlq DeadLetterSink,
) error {
	stageCtx, stageCancel := context.WithCancel(ctx)
	defer stageCancel()

	typedIn := make(chan In)
	typedOut := make(chan Out)

	var wg sync.WaitGroup
	errChan := make(chan error, 3)

	// Ingress adapter: any -> In
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(typedIn)
		for {
			select {
			case <-stageCtx.Done():
				return
			case raw, ok := <-in:
				if !ok {
					return
				}
				typedVal, ok := raw.(In)
				if !ok {
					typeErr := fmt.Errorf("%w: cannot convert %T to stage input %T", ErrInvalidConfiguration, raw, *new(In))
					if sm != nil {
						sm.IncErrors()
					}
					if dlq != nil {
						if sm != nil {
							sm.IncDeadLetter()
						}
						_ = dlq.HandleDeadLetter(stageCtx, DeadLetterEntry{
							StageName: a.stage.Name(),
							Item:      raw,
							Err:       typeErr,
							Timestamp: time.Now().UTC(),
						})
						continue
					}
					stageCancel()
					select {
					case errChan <- typeErr:
					default:
					}
					return
				}
				select {
				case <-stageCtx.Done():
					return
				case typedIn <- typedVal:
				}
			}
		}
	}()

	// Stage execution
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(typedOut)
		if err := a.stage.RunWithContext(stageCtx, typedIn, typedOut, sm, dlq); err != nil {
			stageCancel()
			select {
			case errChan <- err:
			default:
			}
		}
	}()

	// Egress adapter: Out -> any
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stageCtx.Done():
				return
			case val, ok := <-typedOut:
				if !ok {
					return
				}
				select {
				case <-stageCtx.Done():
					return
				case out <- val:
				}
			}
		}
	}()

	wg.Wait()

	select {
	case err := <-errChan:
		return err
	default:
		return nil
	}
}

// AddStage appends a type-safe stage to the pipeline.
func AddStage[In, Out, SIn, SOut any](p *Pipeline[In, Out], stage Stage[SIn, SOut]) *Pipeline[In, Out] {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stages = append(p.stages, &typedStageAdapter[SIn, SOut]{stage: stage})
	return p
}

// Add appends a StageRunner to the pipeline.
func (p *Pipeline[In, Out]) Add(stage StageRunner) *Pipeline[In, Out] {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stages = append(p.stages, stage)
	return p
}

// Name returns the configured pipeline name.
func (p *Pipeline[In, Out]) Name() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.name
}

// Stats returns a snapshot of pipeline and stage operational metrics.
func (p *Pipeline[In, Out]) Stats() Stats {
	p.mu.RLock()
	st := p.startTime
	p.mu.RUnlock()
	return p.collector.snapshot(st)
}

// Run executes the pipeline streaming graph.
// It returns an output channel producing items of type Out, and an error channel that receives any fatal stage errors.
func (p *Pipeline[In, Out]) Run(ctx context.Context, in <-chan In) (<-chan Out, <-chan error) {
	p.mu.Lock()
	p.startTime = time.Now().UTC()
	stages := make([]StageRunner, len(p.stages))
	copy(stages, p.stages)
	bufSize := p.bufferSize
	dlq := p.dlq
	p.mu.Unlock()

	pipeCtx, pipeCancel := context.WithCancel(ctx)

	out := make(chan Out, bufSize)
	errChan := make(chan error, 1)

	// If no stages, wire ingress directly to egress
	if len(stages) == 0 {
		go func() {
			defer pipeCancel()
			defer close(out)
			defer close(errChan)
			for {
				select {
				case <-pipeCtx.Done():
					return
				case item, ok := <-in:
					if !ok {
						return
					}
					val, ok := any(item).(Out)
					if !ok {
						errChan <- fmt.Errorf("%w: cannot directly convert %T to %T in zero-stage pipeline", ErrInvalidConfiguration, item, *new(Out))
						return
					}
					select {
					case <-pipeCtx.Done():
						return
					case out <- val:
					}
				}
			}
		}()
		return out, errChan
	}

	// Create internal channel pipeline
	channels := make([]chan any, len(stages)+1)
	for i := 0; i <= len(stages); i++ {
		channels[i] = make(chan any, bufSize)
	}

	var pipelineWg sync.WaitGroup

	// Ingress feeder
	pipelineWg.Add(1)
	go func() {
		defer pipelineWg.Done()
		defer close(channels[0])
		for {
			select {
			case <-pipeCtx.Done():
				return
			case item, ok := <-in:
				if !ok {
					return
				}
				select {
				case <-pipeCtx.Done():
					return
				case channels[0] <- item:
				}
			}
		}
	}()

	// Stage execution chain
	var stageWg sync.WaitGroup
	stageErrCh := make(chan error, len(stages))

	for i, st := range stages {
		stageIdx := i
		stage := st
		sm := p.collector.register(stage.Name())

		stageWg.Add(1)
		go func() {
			defer stageWg.Done()
			defer close(channels[stageIdx+1])
			if err := stage.RunUntyped(pipeCtx, channels[stageIdx], channels[stageIdx+1], sm, dlq); err != nil {
				pipeCancel()
				select {
				case stageErrCh <- err:
				default:
				}
			}
		}()
	}

	// Egress consumer
	pipelineWg.Add(1)
	go func() {
		defer pipelineWg.Done()
		defer close(out)
		lastCh := channels[len(stages)]
		for {
			select {
			case <-pipeCtx.Done():
				return
			case item, ok := <-lastCh:
				if !ok {
					return
				}
				outVal, ok := item.(Out)
				if !ok {
					typeErr := fmt.Errorf("%w: pipeline output type mismatch: got %T, expected %T", ErrInvalidConfiguration, item, *new(Out))
					pipeCancel()
					select {
					case stageErrCh <- typeErr:
					default:
					}
					return
				}
				select {
				case <-pipeCtx.Done():
					return
				case out <- outVal:
				}
			}
		}
	}()

	// Monitor completion
	go func() {
		stageWg.Wait()
		pipelineWg.Wait()
		pipeCancel()
		defer close(errChan)

		select {
		case err := <-stageErrCh:
			errChan <- err
		default:
		}
	}()

	return out, errChan
}

// RunSlice synchronously processes a slice of items through the pipeline and collects all outputs.
func (p *Pipeline[In, Out]) RunSlice(ctx context.Context, items []In) ([]Out, error) {
	in := make(chan In, len(items))
	for _, it := range items {
		in <- it
	}
	close(in)

	outCh, errCh := p.Run(ctx, in)

	var results []Out
	for item := range outCh {
		results = append(results, item)
	}

	if err, ok := <-errCh; ok && err != nil {
		return results, err
	}
	return results, nil
}

// Sink consumes all items emitted from the pipeline using the provided sink function.
func (p *Pipeline[In, Out]) Sink(
	ctx context.Context,
	in <-chan In,
	sinkFn func(ctx context.Context, item Out) error,
) error {
	outCh, errCh := p.Run(ctx, in)

	for item := range outCh {
		if err := sinkFn(ctx, item); err != nil {
			return err
		}
	}

	if err, ok := <-errCh; ok && err != nil {
		return err
	}
	return nil
}
