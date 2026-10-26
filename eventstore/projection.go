package eventstore

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Projection represents a read-side projection handler in a CQRS architecture.
type Projection interface {
	// Name returns a unique identifier for this projection (used for checkpointing).
	Name() string

	// Handle processes a single domain event to update read-model state.
	Handle(ctx context.Context, event Event) error
}

// CheckpointStore manages the last successfully processed global event position for projections.
type CheckpointStore interface {
	// GetCheckpoint returns the last processed global position for projectionName.
	// Returns 0 if the projection has never run.
	GetCheckpoint(ctx context.Context, projectionName string) (uint64, error)

	// SaveCheckpoint updates the last processed global position for projectionName.
	SaveCheckpoint(ctx context.Context, projectionName string, position uint64) error
}

// MemoryCheckpointStore provides an in-memory implementation of CheckpointStore.
type MemoryCheckpointStore struct {
	mu          sync.RWMutex
	checkpoints map[string]uint64
}

// NewMemoryCheckpointStore constructs a new MemoryCheckpointStore.
func NewMemoryCheckpointStore() *MemoryCheckpointStore {
	return &MemoryCheckpointStore{
		checkpoints: make(map[string]uint64),
	}
}

// GetCheckpoint retrieves the recorded checkpoint position.
func (m *MemoryCheckpointStore) GetCheckpoint(ctx context.Context, projectionName string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.checkpoints[projectionName], nil
}

// SaveCheckpoint saves the checkpoint position for projectionName.
func (m *MemoryCheckpointStore) SaveCheckpoint(ctx context.Context, projectionName string, position uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints[projectionName] = position
	return nil
}

// SubscriptionManager coordinates dispatching committed events to CQRS projections
// and manages catch-up and live streaming.
type SubscriptionManager struct {
	store           Store
	checkpointStore CheckpointStore
	batchSize       uint64
	pollInterval    time.Duration

	mu          sync.RWMutex
	projections []Projection

	cancelFn context.CancelFunc
	wg       sync.WaitGroup
	closed   bool
}

// SubscriptionOption configures the SubscriptionManager.
type SubscriptionOption func(*SubscriptionManager)

// WithBatchSize sets the max batch size when reading events during catch-up.
func WithBatchSize(batchSize uint64) SubscriptionOption {
	return func(sm *SubscriptionManager) {
		if batchSize > 0 {
			sm.batchSize = batchSize
		}
	}
}

// WithPollInterval sets the polling interval when awaiting new events.
func WithPollInterval(interval time.Duration) SubscriptionOption {
	return func(sm *SubscriptionManager) {
		if interval > 0 {
			sm.pollInterval = interval
		}
	}
}

// NewSubscriptionManager constructs a new SubscriptionManager.
func NewSubscriptionManager(store Store, checkpointStore CheckpointStore, opts ...SubscriptionOption) *SubscriptionManager {
	if checkpointStore == nil {
		checkpointStore = NewMemoryCheckpointStore()
	}

	sm := &SubscriptionManager{
		store:           store,
		checkpointStore: checkpointStore,
		batchSize:       100,
		pollInterval:    50 * time.Millisecond,
	}

	for _, opt := range opts {
		opt(sm)
	}

	return sm
}

// Register adds one or more projections to the subscription manager.
func (sm *SubscriptionManager) Register(projections ...Projection) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.projections = append(sm.projections, projections...)
}

// CatchUpProjection processes all historical events from the projection's checkpoint up to the global tail.
func (sm *SubscriptionManager) CatchUpProjection(ctx context.Context, p Projection) error {
	checkpoint, err := sm.checkpointStore.GetCheckpoint(ctx, p.Name())
	if err != nil {
		return fmt.Errorf("eventstore: failed to get checkpoint for %q: %w", p.Name(), err)
	}

	startPos := checkpoint + 1

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		events, err := sm.store.ReadGlobal(ctx, startPos, sm.batchSize)
		if err != nil {
			return fmt.Errorf("eventstore: failed to read global events: %w", err)
		}

		if len(events) == 0 {
			break
		}

		for _, ev := range events {
			if handleErr := p.Handle(ctx, ev); handleErr != nil {
				return fmt.Errorf("eventstore: projection %q failed to handle event %s: %w", p.Name(), ev.ID, handleErr)
			}
			if saveErr := sm.checkpointStore.SaveCheckpoint(ctx, p.Name(), ev.GlobalPosition); saveErr != nil {
				return fmt.Errorf("eventstore: failed to save checkpoint: %w", saveErr)
			}
			startPos = ev.GlobalPosition + 1
		}

		if uint64(len(events)) < sm.batchSize {
			break
		}
	}

	return nil
}

// CatchUp runs CatchUpProjection sequentially for all registered projections.
func (sm *SubscriptionManager) CatchUp(ctx context.Context) error {
	sm.mu.RLock()
	projections := make([]Projection, len(sm.projections))
	copy(projections, sm.projections)
	sm.mu.RUnlock()

	for _, p := range projections {
		if err := sm.CatchUpProjection(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// Start launches a background worker that keeps projections synchronized in real-time.
func (sm *SubscriptionManager) Start(ctx context.Context) error {
	sm.mu.Lock()
	if sm.closed {
		sm.mu.Unlock()
		return ErrSubscriptionClosed
	}

	subCtx, cancel := context.WithCancel(ctx)
	sm.cancelFn = cancel
	sm.mu.Unlock()

	// Initial catch-up
	if err := sm.CatchUp(subCtx); err != nil && subCtx.Err() == nil {
		return err
	}

	sm.wg.Add(1)
	go func() {
		defer sm.wg.Done()
		ticker := time.NewTicker(sm.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-subCtx.Done():
				return
			case <-ticker.C:
				_ = sm.CatchUp(subCtx)
			}
		}
	}()

	return nil
}

// Stop gracefully shuts down the background subscription worker and waits for pending handlers to finish.
func (sm *SubscriptionManager) Stop() {
	sm.mu.Lock()
	if sm.closed {
		sm.mu.Unlock()
		return
	}
	sm.closed = true
	if sm.cancelFn != nil {
		sm.cancelFn()
	}
	sm.mu.Unlock()

	sm.wg.Wait()
}
