package eventstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Event represents an immutable domain event within an event stream.
type Event struct {
	// ID is the unique identifier of the event.
	ID string `json:"id"`
	// StreamID is the unique identifier of the aggregate/stream to which this event belongs.
	StreamID string `json:"stream_id"`
	// AggregateType is the logical type name of the aggregate (e.g. "Order", "BankAccount").
	AggregateType string `json:"aggregate_type,omitempty"`
	// Type is the event type name (e.g. "OrderCreated", "MoneyDeposited").
	Type string `json:"type"`
	// Version is the sequence number of this event within its stream (1-based, strictly monotonic).
	Version uint64 `json:"version"`
	// GlobalPosition is the monotonically increasing global sequence number across all streams.
	GlobalPosition uint64 `json:"global_position"`
	// Data is the raw serialized payload of the event.
	Data []byte `json:"data"`
	// Metadata contains optional contextual key-value pairs (e.g. correlation_id, causation_id, user_id).
	Metadata map[string]any `json:"metadata,omitempty"`
	// Timestamp is the wall-clock time when the event occurred.
	Timestamp time.Time `json:"timestamp"`
}

// UnmarshalData deserializes the event Data JSON into the target value.
func (e Event) UnmarshalData(target any) error {
	if len(e.Data) == 0 {
		return nil
	}
	return json.Unmarshal(e.Data, target)
}

// EventOption configures an Event during creation.
type EventOption func(*Event)

// WithEventID sets a custom unique event ID.
func WithEventID(id string) EventOption {
	return func(e *Event) {
		e.ID = id
	}
}

// WithAggregateType sets the aggregate type name on the event.
func WithAggregateType(aggType string) EventOption {
	return func(e *Event) {
		e.AggregateType = aggType
	}
}

// WithMetadata adds a metadata key-value pair to the event.
func WithMetadata(key string, value any) EventOption {
	return func(e *Event) {
		if e.Metadata == nil {
			e.Metadata = make(map[string]any)
		}
		e.Metadata[key] = value
	}
}

// WithMetadataMap merges a map of metadata into the event.
func WithMetadataMap(m map[string]any) EventOption {
	return func(e *Event) {
		if len(m) == 0 {
			return
		}
		if e.Metadata == nil {
			e.Metadata = make(map[string]any, len(m))
		}
		for k, v := range m {
			e.Metadata[k] = v
		}
	}
}

// WithTimestamp sets a custom occurrence timestamp on the event.
func WithTimestamp(t time.Time) EventOption {
	return func(e *Event) {
		e.Timestamp = t
	}
}

// NewEvent constructs a new Event with an auto-generated random ID and current UTC timestamp.
// If payload is already a []byte, it is copied directly; otherwise it is JSON-encoded.
func NewEvent(streamID, eventType string, payload any, opts ...EventOption) (Event, error) {
	if streamID == "" {
		return Event{}, ErrEmptyStreamID
	}
	if eventType == "" {
		return Event{}, ErrNilEvent
	}

	var data []byte
	if payload != nil {
		switch p := payload.(type) {
		case []byte:
			data = make([]byte, len(p))
			copy(data, p)
		default:
			var err error
			data, err = json.Marshal(p)
			if err != nil {
				return Event{}, fmt.Errorf("eventstore: failed to marshal payload: %w", err)
			}
		}
	}

	evt := Event{
		ID:        generateRandomID(),
		StreamID:  streamID,
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now().UTC(),
	}

	for _, opt := range opts {
		opt(&evt)
	}

	return evt, nil
}

func generateRandomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
