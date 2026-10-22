package idempotency

import "time"

// CleanupForTest exposes internal cleanup for unit testing.
func (s *InMemoryStore) CleanupForTest() {
	s.cleanup()
}

// SetExpiredResponse injects an expired entry to verify expiration purge.
func (s *InMemoryStore) SetExpiredResponse(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[key] = &Response{StatusCode: 200}
	s.expiries[key] = time.Now().Add(-1 * time.Minute)
	s.locks[key] = time.Now().Add(-1 * time.Minute)
}

// HasResponse checks if the key is stored in memory regardless of expiration.
func (s *InMemoryStore) HasResponse(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.responses[key]
	return ok
}
