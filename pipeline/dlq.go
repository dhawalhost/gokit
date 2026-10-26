package pipeline

import (
	"context"
	"sync"
	"time"
)

// DeadLetterEntry encapsulates an item that failed processing within a stage, along with diagnostic context.
type DeadLetterEntry struct {
	StageName string    `json:"stage_name"`
	Item      any       `json:"item"`
	Err       error     `json:"error"`
	Timestamp time.Time `json:"timestamp"`
}

// DeadLetterSink defines an interface for handling and routing rejected or erroring pipeline items.
type DeadLetterSink interface {
	HandleDeadLetter(ctx context.Context, entry DeadLetterEntry) error
}

// DeadLetterFunc adapts a function signature into a DeadLetterSink.
type DeadLetterFunc func(ctx context.Context, entry DeadLetterEntry) error

// HandleDeadLetter executes the underlying function.
func (f DeadLetterFunc) HandleDeadLetter(ctx context.Context, entry DeadLetterEntry) error {
	return f(ctx, entry)
}

// DiscardDeadLetterSink quietly ignores any dead-letter items without error.
type DiscardDeadLetterSink struct{}

// HandleDeadLetter discards the entry.
func (DiscardDeadLetterSink) HandleDeadLetter(ctx context.Context, entry DeadLetterEntry) error {
	return nil
}

// ChanDeadLetterSink pipes dead-letter entries directly into a Go channel.
type ChanDeadLetterSink struct {
	ch chan<- DeadLetterEntry
}

// NewChanDeadLetterSink creates a new channel-backed dead letter sink.
func NewChanDeadLetterSink(ch chan<- DeadLetterEntry) *ChanDeadLetterSink {
	return &ChanDeadLetterSink{ch: ch}
}

// HandleDeadLetter sends the entry into the channel, observing context cancellation.
func (s *ChanDeadLetterSink) HandleDeadLetter(ctx context.Context, entry DeadLetterEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.ch <- entry:
		return nil
	}
}

// MemoryDeadLetterQueue provides a thread-safe, bounded in-memory buffer for dead-letter items.
type MemoryDeadLetterQueue struct {
	mu       sync.RWMutex
	capacity int
	entries  []DeadLetterEntry
}

// NewMemoryDeadLetterQueue constructs an in-memory DLQ buffer with the specified max capacity.
// If capacity <= 0, a default capacity of 1000 is used.
func NewMemoryDeadLetterQueue(capacity int) *MemoryDeadLetterQueue {
	if capacity <= 0 {
		capacity = 1000
	}
	return &MemoryDeadLetterQueue{
		capacity: capacity,
		entries:  make([]DeadLetterEntry, 0, capacity),
	}
}

// HandleDeadLetter appends an entry to the ring buffer. If capacity is reached, the oldest entry is dropped.
func (q *MemoryDeadLetterQueue) HandleDeadLetter(ctx context.Context, entry DeadLetterEntry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.entries) >= q.capacity {
		// Evict oldest item to maintain ring buffer property
		q.entries = q.entries[1:]
	}
	q.entries = append(q.entries, entry)
	return nil
}

// Entries returns a copy of all current dead-letter entries stored in memory.
func (q *MemoryDeadLetterQueue) Entries() []DeadLetterEntry {
	q.mu.RLock()
	defer q.mu.RUnlock()

	copied := make([]DeadLetterEntry, len(q.entries))
	copy(copied, q.entries)
	return copied
}

// Drain clears and returns all stored dead-letter entries atomically.
func (q *MemoryDeadLetterQueue) Drain() []DeadLetterEntry {
	q.mu.Lock()
	defer q.mu.Unlock()

	drained := q.entries
	q.entries = make([]DeadLetterEntry, 0, q.capacity)
	return drained
}

// Len returns the current count of buffered dead-letter entries.
func (q *MemoryDeadLetterQueue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.entries)
}

// Clear removes all stored dead-letter entries.
func (q *MemoryDeadLetterQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.entries = q.entries[:0]
}
