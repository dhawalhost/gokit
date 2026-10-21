package bulkhead

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrBulkheadFull is returned when max concurrent slots and queue capacity are exceeded.
	ErrBulkheadFull = errors.New("bulkhead: capacity exceeded (concurrency limit reached)")
)

// Bulkhead isolates concurrency per resource or tenant using buffered semaphores.
type Bulkhead struct {
	maxConcurrent int
	maxQueue      int
	sem           chan struct{}
	queue         chan struct{}
}

// Config configures a Bulkhead instance.
type Config struct {
	MaxConcurrent int // Maximum allowed in-flight concurrent executions (e.g. 10)
	MaxQueue      int // Maximum waiting queue size when concurrency is full (0 to reject immediately)
}

// New creates a new Bulkhead limiter.
func New(cfg Config) *Bulkhead {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 10
	}

	var queueChan chan struct{}
	if cfg.MaxQueue > 0 {
		queueChan = make(chan struct{}, cfg.MaxQueue)
	}

	return &Bulkhead{
		maxConcurrent: cfg.MaxConcurrent,
		maxQueue:      cfg.MaxQueue,
		sem:           make(chan struct{}, cfg.MaxConcurrent),
		queue:         queueChan,
	}
}

// Execute runs the provided function if concurrency and queue limits permit.
func (b *Bulkhead) Execute(ctx context.Context, fn func() error) error {
	// 1. Try to acquire execution slot immediately
	select {
	case <-ctx.Done():
		return ctx.Err()
	case b.sem <- struct{}{}:
		defer func() { <-b.sem }()
		return fn()
	default:
	}

	// 2. If no queue allowed, fail fast
	if b.maxQueue <= 0 {
		return ErrBulkheadFull
	}

	// 3. Try to join waiting queue
	select {
	case b.queue <- struct{}{}:
		defer func() { <-b.queue }()
	default:
		return ErrBulkheadFull
	}

	// 4. Wait for execution slot from queue
	select {
	case <-ctx.Done():
		return ctx.Err()
	case b.sem <- struct{}{}:
		defer func() { <-b.sem }()
		return fn()
	}
}

// PartitionedBulkhead manages dynamic bulkhead limiters keyed by tenant or dependency.
type PartitionedBulkhead struct {
	cfg       Config
	mu        sync.RWMutex
	bulkheads map[string]*Bulkhead
}

// NewPartitionedBulkhead creates a bulkhead partitioned dynamically by key.
func NewPartitionedBulkhead(cfg Config) *PartitionedBulkhead {
	return &PartitionedBulkhead{
		cfg:       cfg,
		bulkheads: make(map[string]*Bulkhead),
	}
}

// Get retrieves or initializes the bulkhead for a specific key (e.g. tenant_id).
func (pb *PartitionedBulkhead) Get(key string) *Bulkhead {
	pb.mu.RLock()
	b, ok := pb.bulkheads[key]
	pb.mu.RUnlock()
	if ok {
		return b
	}

	pb.mu.Lock()
	defer pb.mu.Unlock()
	if b, ok = pb.bulkheads[key]; ok {
		return b
	}
	b = New(pb.cfg)
	pb.bulkheads[key] = b
	return b
}
