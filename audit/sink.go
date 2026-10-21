package audit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ErrSinkClosed is returned when writing to a closed sink.
var ErrSinkClosed = errors.New("audit: sink is closed")

// Sink defines the contract for persisting or emitting audit log events.
type Sink interface {
	Write(ctx context.Context, event *Event) error
	Close() error
}

// MemorySink retains audit events in memory. Primarily used for testing and assertions.
type MemorySink struct {
	mu     sync.RWMutex
	events []*Event
	closed bool
}

// NewMemorySink creates a new in-memory audit sink.
func NewMemorySink() *MemorySink {
	return &MemorySink{
		events: make([]*Event, 0),
	}
}

// Write records the event in memory.
func (s *MemorySink) Write(_ context.Context, event *Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSinkClosed
	}
	s.events = append(s.events, event)
	return nil
}

// Events returns a copy of all captured events.
func (s *MemorySink) Events() []*Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]*Event, len(s.events))
	copy(res, s.events)
	return res
}

// Clear removes all stored events.
func (s *MemorySink) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = s.events[:0]
}

// Close marks the sink closed.
func (s *MemorySink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// LoggerSink emits sanitized audit events through an existing zap.Logger instance.
type LoggerSink struct {
	logger *zap.Logger
}

// NewLoggerSink constructs a Sink backed by a Zap logger.
func NewLoggerSink(log *zap.Logger) *LoggerSink {
	return &LoggerSink{logger: log}
}

// Write logs the event at INFO level with structured audit attributes.
func (s *LoggerSink) Write(_ context.Context, event *Event) error {
	sanitized := event.Sanitize()
	s.logger.Info("audit_event",
		zap.Bool("audit", true),
		zap.String("event_id", sanitized.ID),
		zap.Time("timestamp", sanitized.Timestamp),
		zap.String("tenant_id", sanitized.TenantID),
		zap.String("actor_id", sanitized.Actor.ID),
		zap.String("actor_type", sanitized.Actor.Type),
		zap.String("action", sanitized.Action),
		zap.String("resource_type", sanitized.Resource.Type),
		zap.String("resource_id", sanitized.Resource.ID),
		zap.String("status", string(sanitized.Status)),
		zap.String("reason", sanitized.Reason),
		zap.Any("changes", sanitized.Changes),
		zap.Any("metadata", sanitized.Metadata),
	)
	return nil
}

// Close is a no-op for LoggerSink.
func (s *LoggerSink) Close() error {
	return nil
}

// MultiSink fans out an audit event to multiple downstream sinks.
type MultiSink struct {
	sinks []Sink
}

// NewMultiSink combines several sinks into one.
func NewMultiSink(sinks ...Sink) *MultiSink {
	return &MultiSink{sinks: sinks}
}

// Write passes the event to all configured sinks.
func (m *MultiSink) Write(ctx context.Context, event *Event) error {
	var errs []error
	for _, s := range m.sinks {
		if err := s.Write(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("audit: multi sink errors: %v", errs)
	}
	return nil
}

// Close closes all underlying sinks.
func (m *MultiSink) Close() error {
	var errs []error
	for _, s := range m.sinks {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("audit: multi sink close errors: %v", errs)
	}
	return nil
}

// AsyncSinkConfig configures the asynchronous background audit worker.
type AsyncSinkConfig struct {
	BufferSize    int           // Size of in-memory queue channel (default: 1024)
	Workers       int           // Number of parallel workers (default: 1)
	BatchSize     int           // Max events processed per batch (default: 50)
	FlushInterval time.Duration // Max wait before flushing a partial batch (default: 100ms)
	DropOnFull    bool          // If true, drop events when queue is full instead of blocking
	OnDrop        func(*Event)  // Optional callback executed when an event is dropped
}

// AsyncSink decouples audit writing from request execution via a buffered channel and worker pool.
type AsyncSink struct {
	target Sink
	queue  chan *Event
	cfg    AsyncSinkConfig
	done   chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
}

// NewAsyncSink creates an AsyncSink that wraps a target sink.
func NewAsyncSink(target Sink, cfg AsyncSinkConfig) *AsyncSink {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 1024
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 100 * time.Millisecond
	}

	as := &AsyncSink{
		target: target,
		queue:  make(chan *Event, cfg.BufferSize),
		cfg:    cfg,
		done:   make(chan struct{}),
	}

	for i := 0; i < cfg.Workers; i++ {
		as.wg.Add(1)
		go as.worker()
	}

	return as
}

// Write enqueues an event for asynchronous processing.
func (a *AsyncSink) Write(ctx context.Context, event *Event) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrSinkClosed
	}
	a.mu.Unlock()

	if a.cfg.DropOnFull {
		select {
		case a.queue <- event:
			return nil
		default:
			if a.cfg.OnDrop != nil {
				a.cfg.OnDrop(event)
			}
			return nil
		}
	}

	select {
	case a.queue <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		return ErrSinkClosed
	}
}

func (a *AsyncSink) worker() {
	defer a.wg.Done()
	ticker := time.NewTicker(a.cfg.FlushInterval)
	defer ticker.Stop()

	batch := make([]*Event, 0, a.cfg.BatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, e := range batch {
			_ = a.target.Write(ctx, e)
		}
		batch = batch[:0]
	}

	for {
		select {
		case event, ok := <-a.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, event)
			if len(batch) >= a.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Close gracefully flushes all queued events and shuts down background workers.
func (a *AsyncSink) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	close(a.done)
	close(a.queue)
	a.mu.Unlock()

	a.wg.Wait()
	return a.target.Close()
}
