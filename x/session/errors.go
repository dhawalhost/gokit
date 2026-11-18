// Package session provides enterprise-grade, distributed HTTP session management for Go microservices,
// featuring pluggable storage backends (in-memory, Redis, encrypted cookie), anti-session-hijacking protection,
// sliding TTLs, flash messages, and session regeneration to mitigate fixation attacks.
package session

import "errors"

var (
	// ErrSessionNotFound is returned when querying for a session that does not exist or has expired.
	ErrSessionNotFound = errors.New("session: session not found")
	// ErrSessionExpired is returned when a session has exceeded its idle or absolute lifetime.
	ErrSessionExpired = errors.New("session: session has expired")
	// ErrNilStore is returned when initializing a session manager without a valid storage backend.
	ErrNilStore = errors.New("session: store cannot be nil")
	// ErrHijackingDetected is returned when client fingerprint validation (IP subnet or User-Agent) fails.
	ErrHijackingDetected = errors.New("session: potential session hijacking detected; fingerprint mismatch")
	// ErrInvalidSession is returned when session data or cookie signature is corrupted or tampered with.
	ErrInvalidSession = errors.New("session: invalid or tampered session data")
	// ErrSessionDestroyed is returned when attempting to mutate a session marked for destruction.
	ErrSessionDestroyed = errors.New("session: session is destroyed")
)
