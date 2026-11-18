package pipeline

import (
	"context"
	"sync"
)

// FanIn multiplexes multiple read-only input channels of type T into a single unified output channel.
// The output channel is closed once all input channels have drained and closed, or upon context cancellation.
func FanIn[T any](ctx context.Context, inputs ...<-chan T) <-chan T {
	out := make(chan T)
	if len(inputs) == 0 {
		close(out)
		return out
	}

	var wg sync.WaitGroup
	wg.Add(len(inputs))

	for _, in := range inputs {
		go func(ch <-chan T) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case val, ok := <-ch:
					if !ok {
						return
					}
					select {
					case <-ctx.Done():
						return
					case out <- val:
					}
				}
			}
		}(in)
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

// FanOut distributes items from a single input channel across multiple output channels in a round-robin distribution.
// Once the input channel is closed, all output channels are closed.
func FanOut[T any](ctx context.Context, in <-chan T, count int, bufSize int) []<-chan T {
	if count <= 0 {
		count = 1
	}
	if bufSize < 0 {
		bufSize = 0
	}

	outputs := make([]chan T, count)
	readOnlyOutputs := make([]<-chan T, count)
	for i := 0; i < count; i++ {
		ch := make(chan T, bufSize)
		outputs[i] = ch
		readOnlyOutputs[i] = ch
	}

	go func() {
		defer func() {
			for _, ch := range outputs {
				close(ch)
			}
		}()

		var idx int
		for {
			select {
			case <-ctx.Done():
				return
			case val, ok := <-in:
				if !ok {
					return
				}
				target := outputs[idx%count]
				idx++
				select {
				case <-ctx.Done():
					return
				case target <- val:
				}
			}
		}
	}()

	return readOnlyOutputs
}

// Broadcast duplicates every element received on the input channel to all returned output channels.
// Each output channel is closed once the input channel has closed or the context is cancelled.
func Broadcast[T any](ctx context.Context, in <-chan T, count int, bufSize int) []<-chan T {
	if count <= 0 {
		count = 1
	}
	if bufSize < 0 {
		bufSize = 0
	}

	outputs := make([]chan T, count)
	readOnlyOutputs := make([]<-chan T, count)
	for i := 0; i < count; i++ {
		ch := make(chan T, bufSize)
		outputs[i] = ch
		readOnlyOutputs[i] = ch
	}

	go func() {
		defer func() {
			for _, ch := range outputs {
				close(ch)
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case val, ok := <-in:
				if !ok {
					return
				}
				for _, ch := range outputs {
					select {
					case <-ctx.Done():
						return
					case ch <- val:
					}
				}
			}
		}
	}()

	return readOnlyOutputs
}
