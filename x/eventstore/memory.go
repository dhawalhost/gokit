package eventstore

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore provides a high-throughput, thread-safe in-memory implementation of the Store interface.
type MemoryStore struct {
	mu        sync.RWMutex
	streams   map[string][]Event
	global    []Event
	globalPos uint64
	listeners map[chan Event]struct{}
}

// NewMemoryStore constructs a new, ready-to-use MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		streams:   make(map[string][]Event),
		global:    make([]Event, 0, 1024),
		listeners: make(map[chan Event]struct{}),
	}
}

// Append writes one or more events to the specified stream, validating the expectedVersion constraint.
func (s *MemoryStore) Append(ctx context.Context, streamID string, expectedVersion ExpectedVersion, events ...Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if streamID == "" {
		return ErrEmptyStreamID
	}
	if len(events) == 0 {
		return nil
	}

	for _, e := range events {
		if e.Type == "" {
			return ErrNilEvent
		}
	}

	var committed []Event

	s.mu.Lock()
	stream, exists := s.streams[streamID]
	var currentVersion uint64
	if exists && len(stream) > 0 {
		currentVersion = stream[len(stream)-1].Version
	}

	if expectedVersion == ExpectedVersionNoStream && exists && len(stream) > 0 {
		s.mu.Unlock()
		return ErrStreamExists
	}

	if !expectedVersion.Matches(exists && len(stream) > 0, currentVersion) {
		s.mu.Unlock()
		return &ConcurrencyConflictError{
			StreamID:        streamID,
			ExpectedVersion: expectedVersion,
			ActualVersion:   currentVersion,
		}
	}

	committed = make([]Event, len(events))
	now := time.Now().UTC()

	for i, ev := range events {
		currentVersion++
		s.globalPos++

		evCopy := ev
		if evCopy.ID == "" {
			evCopy.ID = generateRandomID()
		}
		if evCopy.Timestamp.IsZero() {
			evCopy.Timestamp = now
		}
		evCopy.StreamID = streamID
		evCopy.Version = currentVersion
		evCopy.GlobalPosition = s.globalPos

		// Deep copy metadata if present
		if ev.Metadata != nil {
			metaCopy := make(map[string]any, len(ev.Metadata))
			for k, v := range ev.Metadata {
				metaCopy[k] = v
			}
			evCopy.Metadata = metaCopy
		}

		stream = append(stream, evCopy)
		s.global = append(s.global, evCopy)
		committed[i] = evCopy
	}

	s.streams[streamID] = stream

	// Snapshot listeners to notify
	activeListeners := make([]chan Event, 0, len(s.listeners))
	for ch := range s.listeners {
		activeListeners = append(activeListeners, ch)
	}
	s.mu.Unlock()

	// Broadcast committed events non-blockingly to listeners
	for _, ch := range activeListeners {
		for _, ev := range committed {
			select {
			case ch <- ev:
			default:
				// Listener channel buffer full; drop or slow consumer skipped
			}
		}
	}

	return nil
}

// ReadStream reads events from streamID sequentially from fromVersion.
func (s *MemoryStore) ReadStream(ctx context.Context, streamID string, fromVersion, count uint64) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if streamID == "" {
		return nil, ErrEmptyStreamID
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	stream, exists := s.streams[streamID]
	if !exists || len(stream) == 0 {
		return nil, ErrStreamNotFound
	}

	if fromVersion == 0 {
		fromVersion = 1
	}

	total := uint64(len(stream))
	if fromVersion > total {
		return []Event{}, nil
	}

	startIndex := fromVersion - 1
	endIndex := total
	if count > 0 && startIndex+count < total {
		endIndex = startIndex + count
	}

	result := make([]Event, endIndex-startIndex)
	copy(result, stream[startIndex:endIndex])
	return result, nil
}

// ReadStreamBackward reads events from streamID in reverse order starting at fromVersion.
func (s *MemoryStore) ReadStreamBackward(ctx context.Context, streamID string, fromVersion, count uint64) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if streamID == "" {
		return nil, ErrEmptyStreamID
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	stream, exists := s.streams[streamID]
	if !exists || len(stream) == 0 {
		return nil, ErrStreamNotFound
	}

	total := uint64(len(stream))
	if fromVersion == 0 || fromVersion > total {
		fromVersion = total
	}

	var result []Event
	for i := fromVersion; i >= 1; i-- {
		result = append(result, stream[i-1])
		if count > 0 && uint64(len(result)) >= count {
			break
		}
	}

	return result, nil
}

// ReadGlobal reads events across all streams starting from global position fromPosition.
func (s *MemoryStore) ReadGlobal(ctx context.Context, fromPosition, count uint64) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if fromPosition == 0 {
		fromPosition = 1
	}

	total := uint64(len(s.global))
	if fromPosition > total {
		return []Event{}, nil
	}

	startIndex := fromPosition - 1
	endIndex := total
	if count > 0 && startIndex+count < total {
		endIndex = startIndex + count
	}

	result := make([]Event, endIndex-startIndex)
	copy(result, s.global[startIndex:endIndex])
	return result, nil
}

// GetStreamVersion returns the highest version of the stream.
func (s *MemoryStore) GetStreamVersion(ctx context.Context, streamID string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if streamID == "" {
		return 0, ErrEmptyStreamID
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	stream, exists := s.streams[streamID]
	if !exists || len(stream) == 0 {
		return 0, ErrStreamNotFound
	}

	return stream[len(stream)-1].Version, nil
}

// Streams returns a sorted list of all active stream identifiers.
func (s *MemoryStore) Streams(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.streams))
	for k := range s.streams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// GlobalPosition returns the latest global position sequence number.
func (s *MemoryStore) GlobalPosition(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.globalPos, nil
}

// Subscribe registers a live event listener channel with a given buffer capacity.
// Returned function unregisters the subscriber.
func (s *MemoryStore) Subscribe(bufferSize int) (<-chan Event, func()) {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	ch := make(chan Event, bufferSize)

	s.mu.Lock()
	s.listeners[ch] = struct{}{}
	s.mu.Unlock()

	unsubscribe := func() {
		s.mu.Lock()
		delete(s.listeners, ch)
		close(ch)
		s.mu.Unlock()
	}

	return ch, unsubscribe
}
