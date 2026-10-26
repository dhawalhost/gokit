package diagnostics

import (
	"fmt"
	"io"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"
)

var (
	cpuMu       sync.Mutex
	cpuActive   bool
	pprofLookup = pprof.Lookup
)

// DumpHeap writes the current pprof heap profile into w.
func DumpHeap(w io.Writer) error {
	if w == nil {
		return ErrNilWriter
	}
	return pprof.WriteHeapProfile(w)
}

// DumpGoroutines writes active goroutine stacks into w.
// debugLevel: 1 formats concise stacks; 2 formats verbose stacks with function arguments.
func DumpGoroutines(w io.Writer, debugLevel int) error {
	if w == nil {
		return ErrNilWriter
	}
	p := pprofLookup("goroutine")
	if p == nil {
		return fmt.Errorf("diagnostics: goroutine profile unavailable")
	}
	return p.WriteTo(w, debugLevel)
}

// DumpCPU starts a CPU profiling session, collects profiling data for duration d, and stops.
// Returns ErrProfilingInProgress if another CPU profile is currently active.
func DumpCPU(w io.Writer, d time.Duration) error {
	if w == nil {
		return ErrNilWriter
	}
	if d <= 0 {
		d = time.Second
	}

	cpuMu.Lock()
	if cpuActive {
		cpuMu.Unlock()
		return ErrProfilingInProgress
	}
	cpuActive = true
	cpuMu.Unlock()

	defer func() {
		cpuMu.Lock()
		cpuActive = false
		cpuMu.Unlock()
	}()

	if err := pprof.StartCPUProfile(w); err != nil {
		return err
	}
	defer pprof.StopCPUProfile()

	time.Sleep(d)
	return nil
}

// DumpBlock writes a pprof block contention profile into w.
func DumpBlock(w io.Writer, rate int) error {
	if w == nil {
		return ErrNilWriter
	}
	runtime.SetBlockProfileRate(rate)
	p := pprofLookup("block")
	if p == nil {
		return fmt.Errorf("diagnostics: block profile unavailable")
	}
	return p.WriteTo(w, 1)
}

// DumpMutex writes a pprof mutex contention profile into w.
func DumpMutex(w io.Writer, fraction int) error {
	if w == nil {
		return ErrNilWriter
	}
	runtime.SetMutexProfileFraction(fraction)
	p := pprofLookup("mutex")
	if p == nil {
		return fmt.Errorf("diagnostics: mutex profile unavailable")
	}
	return p.WriteTo(w, 1)
}
