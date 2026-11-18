package eventstore

import (
	"context"
	"sync"
	"time"
)

// Snapshot represents a point-in-time serialized state of an aggregate root.
type Snapshot struct {
	// StreamID is the unique identifier of the aggregate stream.
	StreamID string `json:"stream_id"`
	// AggregateType is the logical type name of the aggregate.
	AggregateType string `json:"aggregate_type,omitempty"`
	// Version is the aggregate version at the time the snapshot was captured.
	Version uint64 `json:"version"`
	// State is the binary or JSON serialized aggregate state payload.
	State []byte `json:"state"`
	// Timestamp is when the snapshot was persisted.
	Timestamp time.Time `json:"timestamp"`
}

// Snapshotter is an optional interface implemented by aggregates that support fast state snapshotting.
type Snapshotter interface {
	// CreateSnapshot serializes the aggregate's current internal state into bytes.
	CreateSnapshot() ([]byte, error)

	// ApplySnapshot restores the aggregate's internal state from a serialized snapshot payload.
	ApplySnapshot(state []byte) error
}

// SnapshotStore defines the storage mechanism for aggregate point-in-time snapshots.
type SnapshotStore interface {
	// SaveSnapshot persists a snapshot for the stream.
	SaveSnapshot(ctx context.Context, snapshot Snapshot) error

	// GetSnapshot retrieves the latest snapshot for the given streamID.
	// Returns ErrSnapshotNotFound if no snapshot exists.
	GetSnapshot(ctx context.Context, streamID string) (*Snapshot, error)

	// DeleteSnapshot removes the snapshot for streamID.
	DeleteSnapshot(ctx context.Context, streamID string) error
}

// SnapshotStrategy determines whether an aggregate should be snapshotted after uncommitted events are saved.
type SnapshotStrategy func(agg AggregateRoot, uncommittedCount int) bool

// EveryNEvents creates a SnapshotStrategy that triggers a snapshot whenever the aggregate version reaches a multiple of n,
// or when the number of uncommitted events processed spans across a multiple of n.
func EveryNEvents(n uint64) SnapshotStrategy {
	return func(agg AggregateRoot, uncommittedCount int) bool {
		if n == 0 || agg == nil {
			return false
		}
		v := agg.Version()
		if v == 0 {
			return false
		}
		if uncommittedCount <= 0 {
			return false
		}
		uc := uint64(uncommittedCount)
		var prevVersion uint64
		if v > uc {
			prevVersion = v - uc
		}
		return (v / n) > (prevVersion / n)
	}
}

// AlwaysSnapshot creates a SnapshotStrategy that captures a snapshot on every aggregate save.
func AlwaysSnapshot() SnapshotStrategy {
	return func(agg AggregateRoot, uncommittedCount int) bool {
		return agg != nil && uncommittedCount > 0
	}
}

// NeverSnapshot creates a SnapshotStrategy that disables automatic snapshotting.
func NeverSnapshot() SnapshotStrategy {
	return func(agg AggregateRoot, uncommittedCount int) bool {
		return false
	}
}

// MemorySnapshotStore provides a thread-safe in-memory store for snapshots.
type MemorySnapshotStore struct {
	mu        sync.RWMutex
	snapshots map[string]Snapshot
}

// NewMemorySnapshotStore creates a new MemorySnapshotStore.
func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{
		snapshots: make(map[string]Snapshot),
	}
}

// SaveSnapshot saves or overwrites the snapshot for the stream.
func (m *MemorySnapshotStore) SaveSnapshot(ctx context.Context, snapshot Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot.StreamID == "" {
		return ErrEmptyStreamID
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	stateCopy := make([]byte, len(snapshot.State))
	copy(stateCopy, snapshot.State)

	snapshot.State = stateCopy
	if snapshot.Timestamp.IsZero() {
		snapshot.Timestamp = time.Now().UTC()
	}

	m.snapshots[snapshot.StreamID] = snapshot
	return nil
}

// GetSnapshot retrieves the snapshot for streamID.
func (m *MemorySnapshotStore) GetSnapshot(ctx context.Context, streamID string) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if streamID == "" {
		return nil, ErrEmptyStreamID
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	snap, exists := m.snapshots[streamID]
	if !exists {
		return nil, ErrSnapshotNotFound
	}

	snapCopy := snap
	snapCopy.State = make([]byte, len(snap.State))
	copy(snapCopy.State, snap.State)
	return &snapCopy, nil
}

// DeleteSnapshot removes any snapshot stored for streamID.
func (m *MemorySnapshotStore) DeleteSnapshot(ctx context.Context, streamID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if streamID == "" {
		return ErrEmptyStreamID
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.snapshots, streamID)
	return nil
}
