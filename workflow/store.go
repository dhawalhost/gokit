package workflow

import (
	"context"
	"sync"
)

// Store provides persistence and retrieval operations for workflow instances with optimistic locking.
type Store interface {
	// Save persists an instance. If the instance is new (version 1 and not present), it is inserted.
	// If the instance already exists, instance.Version must equal existing.Version + 1.
	// Otherwise, ErrOptimisticLockConflict is returned.
	Save(ctx context.Context, instance *Instance) error

	// Get retrieves an instance by its unique ID. Returns ErrInstanceNotFound if it does not exist.
	Get(ctx context.Context, id string) (*Instance, error)

	// Delete removes an instance by its unique ID. Returns ErrInstanceNotFound if it does not exist.
	Delete(ctx context.Context, id string) error
}

// MemoryStore is an in-memory, thread-safe implementation of Store.
type MemoryStore struct {
	mu        sync.RWMutex
	instances map[string]*Instance
}

// NewMemoryStore initializes an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		instances: make(map[string]*Instance),
	}
}

// Save persists the instance to memory while enforcing optimistic concurrency control.
func (s *MemoryStore) Save(ctx context.Context, instance *Instance) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if instance == nil || instance.ID == "" {
		return ErrInstanceNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.instances[instance.ID]
	if !exists {
		if instance.Version != 1 {
			return ErrOptimisticLockConflict
		}
		s.instances[instance.ID] = instance.Clone()
		return nil
	}

	if instance.Version != existing.Version+1 {
		return ErrOptimisticLockConflict
	}

	s.instances[instance.ID] = instance.Clone()
	return nil
}

// Get retrieves an instance by ID from memory.
func (s *MemoryStore) Get(ctx context.Context, id string) (*Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrInstanceNotFound
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	inst, exists := s.instances[id]
	if !exists {
		return nil, ErrInstanceNotFound
	}

	return inst.Clone(), nil
}

// Delete removes an instance by ID from memory.
func (s *MemoryStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" {
		return ErrInstanceNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.instances[id]; !exists {
		return ErrInstanceNotFound
	}

	delete(s.instances, id)
	return nil
}
