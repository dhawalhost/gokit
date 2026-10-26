package session

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Data represents the persistent, serializable state of an HTTP session.
type Data struct {
	Values      map[string]any   `json:"values"`
	Flashes     map[string][]any `json:"flashes,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	AccessedAt  time.Time        `json:"accessed_at"`
	Fingerprint string           `json:"fingerprint,omitempty"`
}

// Session encapsulates an active user session bound to an HTTP request context.
type Session struct {
	mu          sync.RWMutex
	id          string
	data        Data
	isNew       bool
	isModified  bool
	isDestroyed bool
	regenerate  bool
}

// NewSession creates an initialized Session instance with a new random ID.
func NewSession(fingerprint string) *Session {
	now := time.Now().UTC()
	return &Session{
		id: generateSessionID(),
		data: Data{
			Values:      make(map[string]any),
			Flashes:     make(map[string][]any),
			CreatedAt:   now,
			AccessedAt:  now,
			Fingerprint: fingerprint,
		},
		isNew:      true,
		isModified: true,
	}
}

// ID returns the unique session token.
func (s *Session) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// Get retrieves a value from session storage.
func (s *Session) Get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.isDestroyed || s.data.Values == nil {
		return nil, false
	}
	val, ok := s.data.Values[key]
	return val, ok
}

// GetString retrieves a string value, returning "" if not found or of another type.
func (s *Session) GetString(key string) string {
	val, ok := s.Get(key)
	if !ok {
		return ""
	}
	if str, ok := val.(string); ok {
		return str
	}
	return ""
}

// GetInt retrieves an integer value, handling int, float64 (from JSON), etc.
func (s *Session) GetInt(key string) int {
	val, ok := s.Get(key)
	if !ok {
		return 0
	}
	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// GetBool retrieves a boolean value, returning false if absent or not a bool.
func (s *Session) GetBool(key string) bool {
	val, ok := s.Get(key)
	if !ok {
		return false
	}
	if b, ok := val.(bool); ok {
		return b
	}
	return false
}

// Set stores a key-value pair in the session and marks it as modified.
func (s *Session) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isDestroyed {
		return
	}
	if s.data.Values == nil {
		s.data.Values = make(map[string]any)
	}
	s.data.Values[key] = value
	s.isModified = true
}

// Delete removes a key from the session.
func (s *Session) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isDestroyed || s.data.Values == nil {
		return
	}
	delete(s.data.Values, key)
	s.isModified = true
}

// Clear removes all stored values.
func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isDestroyed {
		return
	}
	s.data.Values = make(map[string]any)
	s.isModified = true
}

// AddFlash enqueues a one-time flash message under key.
func (s *Session) AddFlash(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isDestroyed {
		return
	}
	if s.data.Flashes == nil {
		s.data.Flashes = make(map[string][]any)
	}
	s.data.Flashes[key] = append(s.data.Flashes[key], value)
	s.isModified = true
}

// Flashes pops and returns all flash messages stored under key, clearing them.
func (s *Session) Flashes(key string) []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isDestroyed || s.data.Flashes == nil {
		return nil
	}
	flashes, ok := s.data.Flashes[key]
	if !ok || len(flashes) == 0 {
		return nil
	}
	delete(s.data.Flashes, key)
	s.isModified = true
	return flashes
}

// Regenerate marks the session for re-keying with a brand-new ID upon saving,
// mitigating session fixation attacks upon privilege level transitions.
func (s *Session) Regenerate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.regenerate = true
	s.isModified = true
}

// Destroy marks the session for immediate revocation and cookie removal.
func (s *Session) Destroy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isDestroyed = true
	s.isModified = true
	s.data.Values = nil
	s.data.Flashes = nil
}

// IsDestroyed returns true if the session has been marked for revocation.
func (s *Session) IsDestroyed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isDestroyed
}

// IsModified returns true if the session contains uncommitted changes.
func (s *Session) IsModified() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isModified
}

func generateSessionID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
