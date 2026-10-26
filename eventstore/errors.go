// Package eventstore provides a high-performance Event Sourcing and CQRS engine for Go microservices,
// featuring append-only event streams, optimistic concurrency control, aggregate root rehydration,
// snapshotting strategies, and projection subscription managers.
package eventstore

import (
	"errors"
	"fmt"
)

var (
	// ErrConcurrencyConflict is returned when appending events with an expected version that does not match the actual stream version.
	ErrConcurrencyConflict = errors.New("eventstore: optimistic concurrency conflict, stream version mismatch")
	// ErrStreamNotFound is returned when reading from a stream that does not exist.
	ErrStreamNotFound = errors.New("eventstore: stream not found")
	// ErrStreamExists is returned when appending to a stream that already exists with ExpectedVersionNoStream.
	ErrStreamExists = errors.New("eventstore: stream already exists")
	// ErrInvalidVersion is returned when a requested version is invalid.
	ErrInvalidVersion = errors.New("eventstore: invalid version specified")
	// ErrNilEvent is returned when an empty or nil event is supplied.
	ErrNilEvent = errors.New("eventstore: nil or invalid event")
	// ErrEmptyStreamID is returned when an operation is attempted with an empty stream ID.
	ErrEmptyStreamID = errors.New("eventstore: stream ID cannot be empty")
	// ErrSnapshotNotFound is returned when attempting to retrieve a non-existent snapshot.
	ErrSnapshotNotFound = errors.New("eventstore: snapshot not found")
	// ErrNilAggregate is returned when a nil aggregate root is passed to repository operations.
	ErrNilAggregate = errors.New("eventstore: nil aggregate root provided")
	// ErrSubscriptionClosed is returned when an operation is performed on a stopped subscription manager.
	ErrSubscriptionClosed = errors.New("eventstore: subscription manager is closed")
)

// ConcurrencyConflictError provides rich diagnostic details on concurrency conflicts.
type ConcurrencyConflictError struct {
	StreamID        string
	ExpectedVersion ExpectedVersion
	ActualVersion   uint64
}

func (e *ConcurrencyConflictError) Error() string {
	return fmt.Sprintf("eventstore: concurrency conflict on stream %q: expected version %s, actual version %d",
		e.StreamID, e.ExpectedVersion.String(), e.ActualVersion)
}

func (e *ConcurrencyConflictError) Unwrap() error {
	return ErrConcurrencyConflict
}
