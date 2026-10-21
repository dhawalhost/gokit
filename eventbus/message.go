package eventbus

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Message represents an event published or received through the EventBus.
type Message struct {
	// ID is the unique identifier for the event.
	ID string `json:"id"`

	// Topic is the event category or stream name.
	Topic string `json:"topic"`

	// Key is an optional partition or ordering key.
	Key string `json:"key,omitempty"`

	// Payload is the raw byte content of the event.
	Payload []byte `json:"payload"`

	// Headers contains metadata attributes (e.g., traceparent, tenant_id).
	Headers map[string]string `json:"headers,omitempty"`

	// Timestamp is when the event was generated.
	Timestamp time.Time `json:"timestamp"`
}

// NewMessage creates a new Message with a generated UUID and current timestamp.
func NewMessage(topic string, payload []byte) *Message {
	return &Message{
		ID:        uuid.NewString(),
		Topic:     topic,
		Payload:   payload,
		Headers:   make(map[string]string),
		Timestamp: time.Now().UTC(),
	}
}

// NewJSONMessage marshals any Go data type into a JSON-encoded Message.
func NewJSONMessage(topic string, data any) (*Message, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("eventbus: marshal json payload: %w", err)
	}
	return NewMessage(topic, bytes), nil
}

// Unmarshal deserializes the message JSON payload into the target destination.
func (m *Message) Unmarshal(v any) error {
	if len(m.Payload) == 0 {
		return fmt.Errorf("eventbus: empty payload")
	}
	return json.Unmarshal(m.Payload, v)
}

// WithKey sets the partition or correlation key on the message.
func (m *Message) WithKey(key string) *Message {
	m.Key = key
	return m
}

// WithID sets a custom message ID.
func (m *Message) WithID(id string) *Message {
	m.ID = id
	return m
}

// WithHeader attaches a key-value header metadata to the message.
func (m *Message) WithHeader(key, value string) *Message {
	if m.Headers == nil {
		m.Headers = make(map[string]string)
	}
	m.Headers[key] = value
	return m
}

// WithHeaders merges the provided map into the message headers.
func (m *Message) WithHeaders(headers map[string]string) *Message {
	if m.Headers == nil {
		m.Headers = make(map[string]string, len(headers))
	}
	for k, v := range headers {
		m.Headers[k] = v
	}
	return m
}
