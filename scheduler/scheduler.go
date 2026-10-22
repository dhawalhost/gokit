package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Scheduler coordinates the registration, timing, and execution of recurring jobs.
type Scheduler struct {
	mu       sync.RWMutex
	stopMu   sync.RWMutex
	stopped  bool
	cfg      Config
	jobs     map[string]*jobEntry
	running  atomic.Bool
	stopCh   chan struct{}
	doneCh   chan struct{}
	activeWg sync.WaitGroup
	timeNow  func() time.Time // override for deterministic testing
}

// New creates a new configured Scheduler instance.
func New(opts ...Option) *Scheduler {
	cfg := Config{
		Location:     time.UTC,
		TickInterval: 500 * time.Millisecond,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	return &Scheduler{
		cfg:     cfg,
		jobs:    make(map[string]*jobEntry),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		timeNow: time.Now,
	}
}

// Register adds a Job to the scheduler with the specified cron/interval spec and options.
// Returns the unique job identifier assigned to the job.
func (s *Scheduler) Register(spec string, job Job, opts ...JobOption) (string, error) {
	if job == nil {
		return "", errors.New("scheduler: job cannot be nil")
	}

	sch, err := Parse(spec)
	if err != nil {
		return "", err
	}

	jobCfg := JobConfig{
		OverlapPolicy: OverlapSkip,
	}
	for _, opt := range opts {
		opt(&jobCfg)
	}

	id := uuid.New().String()
	if jobCfg.Name == "" {
		jobCfg.Name = id
	}

	now := s.now()
	entry := &jobEntry{
		id:       id,
		spec:     spec,
		schedule: sch,
		job:      job,
		cfg:      jobCfg,
		state:    StateIdle,
		nextRun:  sch.Next(now),
	}

	s.mu.Lock()
	s.jobs[id] = entry
	s.mu.Unlock()

	return id, nil
}

// RegisterFunc registers a function as a scheduled job.
func (s *Scheduler) RegisterFunc(spec string, fn func(context.Context) error, opts ...JobOption) (string, error) {
	if fn == nil {
		return "", errors.New("scheduler: job function cannot be nil")
	}
	return s.Register(spec, JobFunc(fn), opts...)
}

// Unregister removes a job by its ID.
func (s *Scheduler) Unregister(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[jobID]; !exists {
		return ErrJobNotFound
	}
	delete(s.jobs, jobID)
	return nil
}

// Job returns a snapshot of a single scheduled job.
func (s *Scheduler) Job(jobID string) (JobSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.jobs[jobID]
	if !exists {
		return JobSnapshot{}, false
	}
	return entry.Snapshot(), true
}

// Jobs returns snapshots of all currently registered jobs.
func (s *Scheduler) Jobs() []JobSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshots := make([]JobSnapshot, 0, len(s.jobs))
	for _, entry := range s.jobs {
		snapshots = append(snapshots, entry.Snapshot())
	}
	return snapshots
}

// Count returns the number of currently registered jobs.
func (s *Scheduler) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.jobs)
}

// Start begins evaluating and triggering scheduled jobs until the context is cancelled or Stop is called.
func (s *Scheduler) Start(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return ErrSchedulerRunning
	}

	s.stopMu.Lock()
	s.stopped = false
	s.stopMu.Unlock()

	ticker := time.NewTicker(s.cfg.TickInterval)
	defer ticker.Stop()
	defer close(s.doneCh)

	for {
		select {
		case <-ctx.Done():
			_ = s.Stop(context.Background())
			return ctx.Err()
		case <-s.stopCh:
			return nil
		case t := <-ticker.C:
			s.tick(ctx, t)
		}
	}
}

// StartAsync starts the scheduler in a background goroutine.
func (s *Scheduler) StartAsync(ctx context.Context) {
	go func() {
		_ = s.Start(ctx)
	}()
}

// Stop gracefully stops the scheduler, waiting for active jobs to complete or context expiration.
func (s *Scheduler) Stop(ctx context.Context) error {
	if !s.running.CompareAndSwap(true, false) {
		return nil
	}

	s.stopMu.Lock()
	s.stopped = true
	s.stopMu.Unlock()

	close(s.stopCh)

	done := make(chan struct{})
	go func() {
		s.activeWg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// RunJob immediately executes the specified job out-of-schedule.
func (s *Scheduler) RunJob(ctx context.Context, jobID string) error {
	s.mu.RLock()
	entry, exists := s.jobs[jobID]
	s.mu.RUnlock()

	if !exists {
		return ErrJobNotFound
	}

	s.activeWg.Add(1)
	defer s.activeWg.Done()

	return s.executeJob(ctx, entry, true)
}

// tick inspects all registered jobs and triggers executions whose nextRun is due.
func (s *Scheduler) tick(ctx context.Context, now time.Time) {
	s.mu.RLock()
	entries := make([]*jobEntry, 0, len(s.jobs))
	for _, entry := range s.jobs {
		entries = append(entries, entry)
	}
	s.mu.RUnlock()

	for _, entry := range entries {
		entry.mu.RLock()
		due := !entry.nextRun.IsZero() && !now.Before(entry.nextRun)
		entry.mu.RUnlock()

		if due {
			s.stopMu.RLock()
			if s.stopped {
				s.stopMu.RUnlock()
				return
			}
			s.activeWg.Add(1)
			s.stopMu.RUnlock()

			go func(e *jobEntry) {
				defer s.activeWg.Done()
				_ = s.executeJob(ctx, e, false)
			}(entry)
		}
	}
}

// executeJob handles concurrency control, distributed locking, timeout, retry, and execution metrics.
func (s *Scheduler) executeJob(ctx context.Context, e *jobEntry, isManual bool) error {
	e.mu.Lock()
	now := s.now()

	// Handle overlap policy
	if e.running && e.cfg.OverlapPolicy == OverlapSkip {
		e.skippedCount++
		e.state = StateSkipped
		snap := e.snapshotLocked()
		e.mu.Unlock()
		s.notifySkipped(snap, "overlap")
		return nil
	}

	e.running = true
	e.state = StateRunning
	e.prevRun = now
	if !isManual {
		e.nextRun = e.schedule.Next(now)
	}
	e.mu.Unlock()

	snap := e.Snapshot()
	s.notifyStart(snap)

	// Distributed lock handling
	var unlockFunc func()
	if e.cfg.LockKey != "" && s.cfg.Locker != nil {
		ttl := e.cfg.LockTTL
		if ttl <= 0 {
			ttl = 30 * time.Second
		}

		lock, err := s.cfg.Locker.Obtain(ctx, e.cfg.LockKey, ttl)
		if err != nil {
			// Lock not acquired: another replica is executing this job
			e.mu.Lock()
			e.running = false
			e.skippedCount++
			e.state = StateSkipped
			snapAfter := e.snapshotLocked()
			e.mu.Unlock()

			s.notifySkipped(snapAfter, "locked_by_peer")
			return nil
		}

		unlockFunc = func() {
			_ = lock.Unlock(context.Background())
		}
	}

	if unlockFunc != nil {
		defer unlockFunc()
	}

	// Execution with timeout and retries
	start := s.now()
	err := s.runWithRetries(ctx, e)
	duration := s.now().Sub(start)

	e.mu.Lock()
	e.running = false
	e.runCount++
	e.lastDuration = duration
	e.lastError = err

	if err != nil {
		e.failureCount++
		e.state = StateFailed
	} else {
		e.successCount++
		e.state = StateSuccess
	}
	finalSnap := e.snapshotLocked()
	e.mu.Unlock()

	if err != nil {
		s.notifyFailure(finalSnap, duration, err)
		return err
	}

	s.notifySuccess(finalSnap, duration)
	return nil
}

// runWithRetries executes the job, applying timeout and retry logic with panic recovery.
func (s *Scheduler) runWithRetries(ctx context.Context, e *jobEntry) (err error) {
	attempts := 1 + e.cfg.MaxRetries

	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		runCtx := ctx
		var cancel context.CancelFunc
		if e.cfg.Timeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, e.cfg.Timeout)
		}

		err = s.safeRun(runCtx, e.job)
		if cancel != nil {
			cancel()
		}

		if err == nil {
			return nil
		}

		// Wait backoff if retrying
		if i+1 < attempts && e.cfg.RetryBackoff > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(e.cfg.RetryBackoff):
			}
		}
	}

	return err
}

// safeRun executes the job with panic protection.
func (s *Scheduler) safeRun(ctx context.Context, job Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("scheduler: job panic: %v", r)
		}
	}()
	return job.Run(ctx)
}

func (s *Scheduler) now() time.Time {
	if s.timeNow != nil {
		return s.timeNow().In(s.cfg.Location)
	}
	return time.Now().In(s.cfg.Location)
}

func (s *Scheduler) notifyStart(snap JobSnapshot) {
	for _, h := range s.cfg.Hooks {
		if h.OnJobStart != nil {
			h.OnJobStart(snap)
		}
	}
}

func (s *Scheduler) notifySuccess(snap JobSnapshot, dur time.Duration) {
	for _, h := range s.cfg.Hooks {
		if h.OnJobSuccess != nil {
			h.OnJobSuccess(snap, dur)
		}
	}
}

func (s *Scheduler) notifyFailure(snap JobSnapshot, dur time.Duration, err error) {
	for _, h := range s.cfg.Hooks {
		if h.OnJobFailure != nil {
			h.OnJobFailure(snap, dur, err)
		}
	}
}

func (s *Scheduler) notifySkipped(snap JobSnapshot, reason string) {
	for _, h := range s.cfg.Hooks {
		if h.OnJobSkipped != nil {
			h.OnJobSkipped(snap, reason)
		}
	}
}
