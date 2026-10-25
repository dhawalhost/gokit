package secrets

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type cachedSecret struct {
	secret    *Secret
	expiresAt time.Time
}

// ManagerConfig configures secret caching and rotation behavior.
type ManagerConfig struct {
	TTL             time.Duration
	CacheEnabled    bool
	AutoRefresh     bool
	RefreshInterval time.Duration
}

// DefaultManagerConfig returns recommended production defaults.
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		TTL:             5 * time.Minute,
		CacheEnabled:    true,
		AutoRefresh:     false,
		RefreshInterval: 1 * time.Minute,
	}
}

// ManagerOption modifies ManagerConfig.
type ManagerOption func(*ManagerConfig)

// WithTTL sets the in-memory cache TTL for loaded secrets.
func WithTTL(d time.Duration) ManagerOption {
	return func(c *ManagerConfig) {
		if d > 0 {
			c.TTL = d
		}
	}
}

// WithCache enables or disables secret caching.
func WithCache(enabled bool) ManagerOption {
	return func(c *ManagerConfig) {
		c.CacheEnabled = enabled
	}
}

// WithAutoRefresh enables background rotation checking for cached secrets.
func WithAutoRefresh(interval time.Duration) ManagerOption {
	return func(c *ManagerConfig) {
		c.AutoRefresh = true
		if interval > 0 {
			c.RefreshInterval = interval
		}
	}
}

// Manager coordinates secret retrieval, TTL memoization, and background refresh across providers.
type Manager struct {
	cfg       ManagerConfig
	provider  Provider
	cache     map[string]*cachedSecret
	mu        sync.RWMutex
	stopCh    chan struct{}
	stopped   atomic.Bool
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// NewManager initializes a new secret Manager.
func NewManager(provider Provider, opts ...ManagerOption) *Manager {
	if provider == nil {
		provider = NewMemoryProvider()
	}

	cfg := DefaultManagerConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	m := &Manager{
		cfg:      cfg,
		provider: provider,
		cache:    make(map[string]*cachedSecret),
		stopCh:   make(chan struct{}),
	}

	if cfg.AutoRefresh && cfg.CacheEnabled {
		m.wg.Add(1)
		go m.refreshLoop()
	}

	return m
}

// Get fetches a secret, serving from cache if fresh, or loading from the provider.
func (m *Manager) Get(ctx context.Context, key string) (*Secret, error) {
	if m.stopped.Load() {
		return nil, ErrManagerClosed
	}

	if m.cfg.CacheEnabled {
		m.mu.RLock()
		cached, exists := m.cache[key]
		m.mu.RUnlock()

		if exists && time.Now().Before(cached.expiresAt) {
			return cached.secret, nil
		}
	}

	sec, err := m.provider.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	if m.cfg.CacheEnabled {
		m.mu.Lock()
		m.cache[key] = &cachedSecret{
			secret:    sec,
			expiresAt: time.Now().Add(m.cfg.TTL),
		}
		m.mu.Unlock()
	}

	return sec, nil
}

// Set stores a secret in the underlying provider and updates the in-memory cache.
func (m *Manager) Set(ctx context.Context, key string, secret *Secret) error {
	if m.stopped.Load() {
		return ErrManagerClosed
	}

	if err := m.provider.Set(ctx, key, secret); err != nil {
		return err
	}

	if m.cfg.CacheEnabled {
		m.mu.Lock()
		m.cache[key] = &cachedSecret{
			secret:    secret,
			expiresAt: time.Now().Add(m.cfg.TTL),
		}
		m.mu.Unlock()
	}

	return nil
}

// Delete removes a secret from the underlying provider and purges it from cache.
func (m *Manager) Delete(ctx context.Context, key string) error {
	if m.stopped.Load() {
		return ErrManagerClosed
	}

	if err := m.provider.Delete(ctx, key); err != nil {
		return err
	}

	if m.cfg.CacheEnabled {
		m.mu.Lock()
		delete(m.cache, key)
		m.mu.Unlock()
	}

	return nil
}

// Close gracefully terminates background refresh routines.
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.stopped.Store(true)
		close(m.stopCh)
	})
	m.wg.Wait()
}

func (m *Manager) refreshLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.cfg.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.refreshExpiring()
		}
	}
}

func (m *Manager) refreshExpiring() {
	m.mu.RLock()
	keys := make([]string, 0, len(m.cache))
	for k := range m.cache {
		keys = append(keys, k)
	}
	m.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.RefreshInterval)
	defer cancel()

	for _, key := range keys {
		sec, err := m.provider.Get(ctx, key)
		if err == nil {
			m.mu.Lock()
			m.cache[key] = &cachedSecret{
				secret:    sec,
				expiresAt: time.Now().Add(m.cfg.TTL),
			}
			m.mu.Unlock()
		}
	}
}
