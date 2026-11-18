package eventstore

import (
	"context"
	"fmt"
	"reflect"
)

func isNil(v any) bool {
	if v == nil {
		return true
	}
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return val.IsNil()
	default:
		return false
	}
}

// Repository provides a type-safe abstraction for loading and persisting AggregateRoot entities.
type Repository[T AggregateRoot] struct {
	store            Store
	factory          func() T
	snapshotStore    SnapshotStore
	snapshotStrategy SnapshotStrategy
	enrichers        []func(ctx context.Context, event *Event)
}

// RepositoryOption configures a Repository.
type RepositoryOption func(*repoConfig)

type repoConfig struct {
	snapshotStore    SnapshotStore
	snapshotStrategy SnapshotStrategy
	enrichers        []func(ctx context.Context, event *Event)
}

// WithRepositorySnapshotStore configures a SnapshotStore and triggering SnapshotStrategy for the repository.
func WithRepositorySnapshotStore(store SnapshotStore, strategy SnapshotStrategy) RepositoryOption {
	return func(c *repoConfig) {
		c.snapshotStore = store
		c.snapshotStrategy = strategy
	}
}

// WithEventEnricher registers an enricher callback to append contextual metadata (e.g. correlation_id) before events are committed.
func WithEventEnricher(enricher func(ctx context.Context, event *Event)) RepositoryOption {
	return func(c *repoConfig) {
		if enricher != nil {
			c.enrichers = append(c.enrichers, enricher)
		}
	}
}

// NewRepository constructs a type-safe generic Repository for an aggregate root type.
func NewRepository[T AggregateRoot](store Store, factory func() T, opts ...RepositoryOption) *Repository[T] {
	cfg := &repoConfig{
		snapshotStrategy: NeverSnapshot(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return &Repository[T]{
		store:            store,
		factory:          factory,
		snapshotStore:    cfg.snapshotStore,
		snapshotStrategy: cfg.snapshotStrategy,
		enrichers:        cfg.enrichers,
	}
}

// Load rehydrates an aggregate root from its stream of historical events (and snapshot if available).
func (r *Repository[T]) Load(ctx context.Context, id string) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if id == "" {
		return zero, ErrEmptyStreamID
	}
	if r.factory == nil {
		return zero, fmt.Errorf("eventstore: repository factory cannot be nil")
	}

	agg := r.factory()
	if isNil(agg) {
		return zero, ErrNilAggregate
	}

	startVersion := uint64(1)
	loadedFromSnapshot := false

	// 1. Try loading from snapshot first
	if r.snapshotStore != nil {
		snap, err := r.snapshotStore.GetSnapshot(ctx, id)
		if err == nil && snap != nil {
			if snapshotter, ok := any(agg).(Snapshotter); ok {
				if applyErr := snapshotter.ApplySnapshot(snap.State); applyErr == nil {
					agg.SetVersion(snap.Version)
					startVersion = snap.Version + 1
					loadedFromSnapshot = true
				}
			}
		}
	}

	// 2. Read delta events from startVersion
	events, err := r.store.ReadStream(ctx, id, startVersion, 0)
	if err != nil {
		if err == ErrStreamNotFound {
			if loadedFromSnapshot {
				return agg, nil
			}
			return zero, ErrStreamNotFound
		}
		return zero, err
	}

	// 3. Replay events to rebuild aggregate state
	for _, ev := range events {
		agg.ApplyChange(ev)
		agg.SetVersion(ev.Version)
	}

	if agg.Version() == 0 && !loadedFromSnapshot {
		return zero, ErrStreamNotFound
	}

	return agg, nil
}

// Save commits uncommitted aggregate events to the event store with optimistic concurrency checks,
// and conditionally triggers a snapshot save if the configured strategy matches.
func (r *Repository[T]) Save(ctx context.Context, agg T) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if isNil(agg) {
		return ErrNilAggregate
	}

	streamID := agg.AggregateID()
	if streamID == "" {
		return ErrEmptyStreamID
	}

	uncommitted := agg.UncommittedEvents()
	if len(uncommitted) == 0 {
		return nil
	}

	// Enrich events with context metadata
	if len(r.enrichers) > 0 {
		for i := range uncommitted {
			for _, enricher := range r.enrichers {
				enricher(ctx, &uncommitted[i])
			}
		}
	}

	// Compute expected version prior to applying uncommitted events
	uncommittedCount := len(uncommitted)
	baseVersion := agg.Version()
	var expectedVersion ExpectedVersion
	if baseVersion == 0 {
		expectedVersion = ExpectedVersionNoStream
	} else {
		expectedVersion = ExpectedVersionExact(baseVersion)
	}

	// Commit events
	if err := r.store.Append(ctx, streamID, expectedVersion, uncommitted...); err != nil {
		return err
	}

	// Update aggregate version and clear uncommitted events
	newVersion := baseVersion + uint64(uncommittedCount)
	agg.SetVersion(newVersion)
	agg.ClearUncommittedEvents()

	// Check if snapshot is needed
	if r.snapshotStore != nil && r.snapshotStrategy != nil && r.snapshotStrategy(agg, uncommittedCount) {
		if snapshotter, ok := any(agg).(Snapshotter); ok {
			state, err := snapshotter.CreateSnapshot()
			if err == nil {
				_ = r.snapshotStore.SaveSnapshot(ctx, Snapshot{
					StreamID:      streamID,
					AggregateType: agg.AggregateType(),
					Version:       agg.Version(),
					State:         state,
				})
			}
		}
	}

	return nil
}
