package diagnostics

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"
)

// MemorySummary encapsulates a point-in-time snapshot of runtime memory metrics.
type MemorySummary struct {
	Alloc       uint64    `json:"alloc_bytes"`
	TotalAlloc  uint64    `json:"total_alloc_bytes"`
	Sys         uint64    `json:"sys_bytes"`
	HeapAlloc   uint64    `json:"heap_alloc_bytes"`
	HeapSys     uint64    `json:"heap_sys_bytes"`
	HeapInuse   uint64    `json:"heap_inuse_bytes"`
	HeapObjects uint64    `json:"heap_objects"`
	NumGC       uint32    `json:"num_gc"`
	Timestamp   time.Time `json:"timestamp"`
}

// ReadMemorySummary captures the current runtime.MemStats metrics.
func ReadMemorySummary() MemorySummary {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return MemorySummary{
		Alloc:       m.Alloc,
		TotalAlloc:  m.TotalAlloc,
		Sys:         m.Sys,
		HeapAlloc:   m.HeapAlloc,
		HeapSys:     m.HeapSys,
		HeapInuse:   m.HeapInuse,
		HeapObjects: m.HeapObjects,
		NumGC:       m.NumGC,
		Timestamp:   time.Now().UTC(),
	}
}

// WatchdogOption configures the MemoryWatchdog.
type WatchdogOption func(*watchdogConfig)

type watchdogConfig struct {
	maxHeapAlloc        uint64
	maxSysAlloc         uint64
	checkInterval       time.Duration
	cooldown            time.Duration
	dumpDir             string
	onWatermarkExceeded func(summary MemorySummary, dumpPath string)
	dumpWriter          func(w io.Writer) error
}

// WithMaxHeapAlloc sets the maximum allowable HeapAlloc in bytes before triggering an automated heap dump.
func WithMaxHeapAlloc(bytes uint64) WatchdogOption {
	return func(c *watchdogConfig) {
		c.maxHeapAlloc = bytes
	}
}

// WithMaxSysAlloc sets the maximum allowable OS Sys memory in bytes before triggering an automated heap dump.
func WithMaxSysAlloc(bytes uint64) WatchdogOption {
	return func(c *watchdogConfig) {
		c.maxSysAlloc = bytes
	}
}

// WithCheckInterval sets the polling frequency of the watchdog (default: 5s).
func WithCheckInterval(d time.Duration) WatchdogOption {
	return func(c *watchdogConfig) {
		if d > 0 {
			c.checkInterval = d
		}
	}
}

// WithCooldown sets the minimum cooldown duration between successive heap dumps (default: 3m).
func WithCooldown(d time.Duration) WatchdogOption {
	return func(c *watchdogConfig) {
		if d > 0 {
			c.cooldown = d
		}
	}
}

// WithDumpDir specifies the filesystem directory where automated heap profiles will be written.
func WithDumpDir(dir string) WatchdogOption {
	return func(c *watchdogConfig) {
		c.dumpDir = dir
	}
}

// WithWatermarkHook registers a callback invoked whenever the memory watermark is breached.
func WithWatermarkHook(fn func(summary MemorySummary, dumpPath string)) WatchdogOption {
	return func(c *watchdogConfig) {
		c.onWatermarkExceeded = fn
	}
}

// WithDumpWriter overrides the default heap dump serializer (pprof.WriteHeapProfile).
func WithDumpWriter(fn func(w io.Writer) error) WatchdogOption {
	return func(c *watchdogConfig) {
		if fn != nil {
			c.dumpWriter = fn
		}
	}
}

// MemoryWatchdog monitors runtime memory consumption and captures automated heap profiles
// when memory approaches dangerous watermarks prior to OOM termination.
type MemoryWatchdog struct {
	mu       sync.Mutex
	cfg      watchdogConfig
	lastDump time.Time
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopped  bool
}

// NewMemoryWatchdog constructs an initialized MemoryWatchdog.
func NewMemoryWatchdog(opts ...WatchdogOption) *MemoryWatchdog {
	cfg := watchdogConfig{
		checkInterval: 5 * time.Second,
		cooldown:      3 * time.Minute,
		dumpWriter:    pprof.WriteHeapProfile,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	return &MemoryWatchdog{
		cfg: cfg,
	}
}

// Start launches the background monitoring goroutine.
func (w *MemoryWatchdog) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return ErrWatchdogStopped
	}
	if w.cancel != nil {
		w.mu.Unlock()
		return nil // already running
	}

	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.mu.Unlock()

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.cfg.checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				_, _, _ = w.CheckNow()
			}
		}
	}()

	return nil
}

// Stop gracefully terminates the memory monitoring worker.
func (w *MemoryWatchdog) Stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	if w.cancel != nil {
		w.cancel()
	}
	w.mu.Unlock()

	w.wg.Wait()
}

// CheckNow evaluates the current memory against configured watermarks and triggers an automated dump if breached.
func (w *MemoryWatchdog) CheckNow() (breached bool, summary MemorySummary, err error) {
	summary = ReadMemorySummary()

	heapBreached := w.cfg.maxHeapAlloc > 0 && summary.HeapAlloc >= w.cfg.maxHeapAlloc
	sysBreached := w.cfg.maxSysAlloc > 0 && summary.Sys >= w.cfg.maxSysAlloc

	if !heapBreached && !sysBreached {
		return false, summary, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now().UTC()
	if w.cfg.cooldown > 0 && now.Sub(w.lastDump) < w.cfg.cooldown {
		// Suppressed by cooldown rate limiter to prevent disk thrashing
		return true, summary, nil
	}

	w.lastDump = now

	dumpPath := ""
	if w.cfg.dumpDir != "" {
		if mkErr := os.MkdirAll(w.cfg.dumpDir, 0750); mkErr == nil {
			fileName := fmt.Sprintf("heap_watermark_%d.pprof", now.UnixNano())
			dumpPath = filepath.Clean(filepath.Join(w.cfg.dumpDir, fileName))
			// #nosec G304 -- dumpPath is strictly derived from timestamp inside configured dumpDir
			f, createErr := os.OpenFile(dumpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
			if createErr == nil {
				if writeErr := w.cfg.dumpWriter(f); writeErr != nil {
					err = writeErr
				}
				_ = f.Close()
			} else {
				err = createErr
			}
		}
	} else if w.cfg.dumpWriter != nil {
		if writeErr := w.cfg.dumpWriter(io.Discard); writeErr != nil {
			err = writeErr
		}
	}

	if w.cfg.onWatermarkExceeded != nil {
		w.cfg.onWatermarkExceeded(summary, dumpPath)
	}

	return true, summary, err
}
