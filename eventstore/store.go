package eventstore

import "context"

// Store defines the persistent event store interface for append-only event logging,
// stream reading, and global sequence queries.
type Store interface {
	// Append appends one or more domain events to a stream identified by streamID,
	// validating the expectedVersion optimistic concurrency check.
	Append(ctx context.Context, streamID string, expectedVersion ExpectedVersion, events ...Event) error

	// ReadStream reads events from a single stream in forward sequential order starting at fromVersion.
	// If count is 0, all events from fromVersion to the current tail are returned.
	ReadStream(ctx context.Context, streamID string, fromVersion, count uint64) ([]Event, error)

	// ReadStreamBackward reads events from a single stream in reverse chronological order starting at fromVersion.
	// If count is 0, all preceding events down to version 1 are returned.
	ReadStreamBackward(ctx context.Context, streamID string, fromVersion, count uint64) ([]Event, error)

	// ReadGlobal reads events across all streams starting from global position fromPosition.
	// If count is 0, all events up to the global tail are returned.
	ReadGlobal(ctx context.Context, fromPosition, count uint64) ([]Event, error)

	// GetStreamVersion returns the current version of the stream.
	// Returns ErrStreamNotFound if the stream has not been created.
	GetStreamVersion(ctx context.Context, streamID string) (uint64, error)

	// Streams returns a slice of all stream identifiers in the store.
	Streams(ctx context.Context) ([]string, error)

	// GlobalPosition returns the current highest assigned global sequence position.
	GlobalPosition(ctx context.Context) (uint64, error)
}
