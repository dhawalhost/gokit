package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Watcher coordinates one or more configuration providers, maintains an atomic
// thread-safe snapshot of the current configuration, and triggers change listeners.
type Watcher struct {
	providers []Provider
	logger    *zap.Logger

	mu       sync.RWMutex
	snapshot map[string]any

	onReload    []func(snapshot map[string]any)
	onKeyChange map[string][]func(oldVal, newVal any)
	onError     []func(err error)

	cancel context.CancelFunc
	closed bool
}

// New constructs a Watcher backed by the specified providers.
// If multiple providers are specified, later providers override keys from earlier ones.
func New(providers ...Provider) *Watcher {
	return &Watcher{
		providers:   providers,
		logger:      zap.NewNop(),
		snapshot:    make(map[string]any),
		onKeyChange: make(map[string][]func(oldVal, newVal any)),
	}
}

// WithOptions applies functional configuration options to the Watcher.
func (w *Watcher) WithOptions(opts ...Option) *Watcher {
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Option configures a Watcher.
type Option func(*Watcher)

// WithLogger configures a Zap logger for the Watcher.
func WithLogger(l *zap.Logger) Option {
	return func(w *Watcher) {
		if l != nil {
			w.logger = l
		}
	}
}

// Start loads the initial configuration from all providers and begins watching for changes.
func (w *Watcher) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	// Initial load
	merged := make(map[string]any)
	for _, p := range w.providers {
		data, err := p.Fetch(ctx)
		if err != nil {
			cancel()
			return fmt.Errorf("watcher: initial load from %s: %w", p.Name(), err)
		}
		for k, v := range data {
			merged[k] = v
		}
	}

	w.mu.Lock()
	w.snapshot = merged
	w.mu.Unlock()

	// Start watching all providers
	for _, p := range w.providers {
		provider := p
		err := provider.Watch(ctx, func(values map[string]any, err error) {
			if err != nil {
				w.notifyError(err)
				return
			}
			w.applyUpdate(provider.Name(), values)
		})
		if err != nil {
			cancel()
			return fmt.Errorf("watcher: start watch on %s: %w", provider.Name(), err)
		}
	}

	return nil
}

// Stop terminates all provider watchers and frees resources.
func (w *Watcher) Stop() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	cancel := w.cancel
	w.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

func (w *Watcher) applyUpdate(source string, newValues map[string]any) {
	w.mu.Lock()
	oldSnapshot := w.snapshot
	newSnapshot := make(map[string]any, len(oldSnapshot)+len(newValues))
	for k, v := range oldSnapshot {
		newSnapshot[k] = v
	}
	for k, v := range newValues {
		newSnapshot[k] = v
	}

	w.snapshot = newSnapshot

	reloadHandlers := make([]func(map[string]any), len(w.onReload))
	copy(reloadHandlers, w.onReload)

	// Collect key changes
	var changedCallbacks []func()
	for key, handlers := range w.onKeyChange {
		oldVal := lookupKey(oldSnapshot, key)
		newVal := lookupKey(newSnapshot, key)
		if !reflect.DeepEqual(oldVal, newVal) {
			for _, h := range handlers {
				cb := h
				ov, nv := oldVal, newVal
				changedCallbacks = append(changedCallbacks, func() {
					cb(ov, nv)
				})
			}
		}
	}
	w.mu.Unlock()

	// Run callbacks outside of lock
	for _, h := range reloadHandlers {
		h(newSnapshot)
	}
	for _, cb := range changedCallbacks {
		cb()
	}

	w.logger.Debug("watcher: configuration reloaded", zap.String("source", source))
}

func (w *Watcher) notifyError(err error) {
	w.mu.RLock()
	handlers := make([]func(error), len(w.onError))
	copy(handlers, w.onError)
	w.mu.RUnlock()

	w.logger.Error("watcher: configuration error", zap.Error(err))
	for _, h := range handlers {
		h(err)
	}
}

// OnReload registers a callback invoked whenever any configuration value is reloaded.
func (w *Watcher) OnReload(fn func(snapshot map[string]any)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onReload = append(w.onReload, fn)
}

// OnKeyChange registers a callback invoked when a specific key's value changes.
func (w *Watcher) OnKeyChange(key string, fn func(oldVal, newVal any)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onKeyChange[key] = append(w.onKeyChange[key], fn)
}

// OnError registers a callback invoked when a provider encounters a retrieval or parsing error.
func (w *Watcher) OnError(fn func(err error)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onError = append(w.onError, fn)
}

// Snapshot returns a thread-safe copy of the entire current configuration map.
func (w *Watcher) Snapshot() map[string]any {
	w.mu.RLock()
	defer w.mu.RUnlock()
	res := make(map[string]any, len(w.snapshot))
	for k, v := range w.snapshot {
		res[k] = v
	}
	return res
}

// Get retrieves a raw value by key path (e.g. "server.port" or "timeout").
func (w *Watcher) Get(key string) (any, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	v := lookupKey(w.snapshot, key)
	return v, v != nil
}

// GetString returns the string representation of a key, or defaultValue if missing.
func (w *Watcher) GetString(key string, defaultValue ...string) string {
	val, ok := w.Get(key)
	if !ok || val == nil {
		if len(defaultValue) > 0 {
			return defaultValue[0]
		}
		return ""
	}
	return fmt.Sprintf("%v", val)
}

// GetInt returns the integer value of a key, or defaultValue if missing.
func (w *Watcher) GetInt(key string, defaultValue ...int) int {
	val, ok := w.Get(key)
	if !ok || val == nil {
		if len(defaultValue) > 0 {
			return defaultValue[0]
		}
		return 0
	}
	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return 0
}

// GetFloat64 returns the float64 value of a key, or defaultValue if missing.
func (w *Watcher) GetFloat64(key string, defaultValue ...float64) float64 {
	val, ok := w.Get(key)
	if !ok || val == nil {
		if len(defaultValue) > 0 {
			return defaultValue[0]
		}
		return 0
	}
	switch v := val.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return 0
}

// GetBool returns the boolean value of a key, or defaultValue if missing.
func (w *Watcher) GetBool(key string, defaultValue ...bool) bool {
	val, ok := w.Get(key)
	if !ok || val == nil {
		if len(defaultValue) > 0 {
			return defaultValue[0]
		}
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return false
}

// GetDuration returns the duration value of a key (parsed as string "10s", or integer milliseconds).
func (w *Watcher) GetDuration(key string, defaultValue ...time.Duration) time.Duration {
	val, ok := w.Get(key)
	if !ok || val == nil {
		if len(defaultValue) > 0 {
			return defaultValue[0]
		}
		return 0
	}
	switch v := val.(type) {
	case time.Duration:
		return v
	case string:
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	case int:
		return time.Duration(v) * time.Millisecond
	case float64:
		return time.Duration(v) * time.Millisecond
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return 0
}

// Unmarshal decodes the current configuration snapshot into a target struct.
func (w *Watcher) Unmarshal(target any) error {
	if target == nil {
		return errors.New("watcher: target cannot be nil")
	}

	snapshot := w.Snapshot()
	b, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("watcher: marshal snapshot: %w", err)
	}

	if err := json.Unmarshal(b, target); err != nil {
		return fmt.Errorf("watcher: unmarshal into target: %w", err)
	}
	return nil
}

// lookupKey supports nested dot syntax like "server.port".
func lookupKey(m map[string]any, path string) any {
	if val, ok := m[path]; ok {
		return val
	}

	parts := strings.Split(path, ".")
	var current any = m

	for _, part := range parts {
		asMap, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		val, exists := asMap[part]
		if !exists {
			return nil
		}
		current = val
	}
	return current
}
