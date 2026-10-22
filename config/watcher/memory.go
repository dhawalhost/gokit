package watcher

import (
	"context"
	"sync"
)

// MemoryProvider is an in-memory configuration provider primarily used for testing,
// local mocks, and programmatic runtime overrides.
type MemoryProvider struct {
	mu        sync.RWMutex
	data      map[string]any
	listeners []func(values map[string]any, err error)
}

// NewMemoryProvider constructs a MemoryProvider initialized with the provided key-value map.
func NewMemoryProvider(initial map[string]any) *MemoryProvider {
	data := make(map[string]any)
	for k, v := range initial {
		data[k] = v
	}
	return &MemoryProvider{
		data: data,
	}
}

// Name returns the provider identifier.
func (m *MemoryProvider) Name() string {
	return "memory"
}

// Fetch returns a clone of the current in-memory configuration map.
func (m *MemoryProvider) Fetch(ctx context.Context) (map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.clone(), nil
}

// Watch registers an update callback.
func (m *MemoryProvider) Watch(ctx context.Context, onChange func(values map[string]any, err error)) error {
	m.mu.Lock()
	m.listeners = append(m.listeners, onChange)
	m.mu.Unlock()

	// Clean up listeners on context cancellation
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		defer m.mu.Unlock()
		for i, l := range m.listeners {
			// Compare function pointers
			if &l == &onChange {
				m.listeners = append(m.listeners[:i], m.listeners[i+1:]...)
				break
			}
		}
	}()

	return nil
}

// Set updates or inserts a single key and notifies listeners.
func (m *MemoryProvider) Set(key string, val any) {
	m.mu.Lock()
	m.data[key] = val
	snapshot := m.clone()
	listeners := make([]func(values map[string]any, err error), len(m.listeners))
	copy(listeners, m.listeners)
	m.mu.Unlock()

	for _, l := range listeners {
		l(snapshot, nil)
	}
}

// SetAll overwrites all values with the new map and notifies listeners.
func (m *MemoryProvider) SetAll(values map[string]any) {
	m.mu.Lock()
	m.data = make(map[string]any, len(values))
	for k, v := range values {
		m.data[k] = v
	}
	snapshot := m.clone()
	listeners := make([]func(values map[string]any, err error), len(m.listeners))
	copy(listeners, m.listeners)
	m.mu.Unlock()

	for _, l := range listeners {
		l(snapshot, nil)
	}
}

// Delete removes a key and notifies listeners.
func (m *MemoryProvider) Delete(key string) {
	m.mu.Lock()
	delete(m.data, key)
	snapshot := m.clone()
	listeners := make([]func(values map[string]any, err error), len(m.listeners))
	copy(listeners, m.listeners)
	m.mu.Unlock()

	for _, l := range listeners {
		l(snapshot, nil)
	}
}

func (m *MemoryProvider) clone() map[string]any {
	res := make(map[string]any, len(m.data))
	for k, v := range m.data {
		res[k] = v
	}
	return res
}
