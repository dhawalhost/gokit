package session

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

type memoryEntry struct {
	data      []byte
	expiresAt time.Time
}

// MemoryStore provides a high-performance in-memory session store with active TTL eviction.
type MemoryStore struct {
	mu      sync.RWMutex
	entries map[string]memoryEntry
	stopCh  chan struct{}
}

// NewMemoryStore constructs a MemoryStore and starts a background cleanup ticker.
func NewMemoryStore(cleanupInterval time.Duration) *MemoryStore {
	if cleanupInterval <= 0 {
		cleanupInterval = time.Minute
	}

	ms := &MemoryStore{
		entries: make(map[string]memoryEntry),
		stopCh:  make(chan struct{}),
	}

	go ms.startCleanup(cleanupInterval)
	return ms
}

func (m *MemoryStore) startCleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case now := <-ticker.C:
			m.mu.Lock()
			for id, entry := range m.entries {
				if now.After(entry.expiresAt) {
					delete(m.entries, id)
				}
			}
			m.mu.Unlock()
		}
	}
}

// Close stops the background cleanup worker.
func (m *MemoryStore) Close() {
	select {
	case <-m.stopCh:
		// already closed
	default:
		close(m.stopCh)
	}
}

// Load retrieves and deserializes session data if not expired.
func (m *MemoryStore) Load(ctx context.Context, id string) (*Data, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrSessionNotFound
	}

	m.mu.RLock()
	entry, exists := m.entries[id]
	m.mu.RUnlock()

	if !exists {
		return nil, ErrSessionNotFound
	}

	if time.Now().After(entry.expiresAt) {
		m.mu.Lock()
		delete(m.entries, id)
		m.mu.Unlock()
		return nil, ErrSessionNotFound
	}

	var data Data
	if err := json.Unmarshal(entry.data, &data); err != nil {
		return nil, ErrInvalidSession
	}

	return &data, nil
}

// Save serializes and persists session data with the specified TTL.
func (m *MemoryStore) Save(ctx context.Context, id string, data *Data, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" || data == nil {
		return ErrInvalidSession
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries[id] = memoryEntry{
		data:      raw,
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

// Destroy deletes the session data for id.
func (m *MemoryStore) Destroy(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, id)
	return nil
}

// Touch refreshes the expiration time for id without changing its data.
func (m *MemoryStore) Touch(ctx context.Context, id string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.entries[id]
	if !exists || time.Now().After(entry.expiresAt) {
		delete(m.entries, id)
		return ErrSessionNotFound
	}

	entry.expiresAt = time.Now().Add(ttl)
	m.entries[id] = entry
	return nil
}
