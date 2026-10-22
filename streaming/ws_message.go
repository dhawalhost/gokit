package streaming

import (
	"encoding/json"
	"time"

	"github.com/gorilla/websocket"
)

// Standard WebSocket frame types.
const (
	MessageTypeText   = websocket.TextMessage
	MessageTypeBinary = websocket.BinaryMessage
)

// Message represents a message routed through the WebSocket Hub.
type Message struct {
	// Type specifies whether the message is Text (1) or Binary (2).
	Type int `json:"type"`
	// Topic identifies the pub/sub channel (empty string for broadcast all).
	Topic string `json:"topic,omitempty"`
	// Payload is the raw byte payload.
	Payload []byte `json:"payload"`
	// Metadata holds arbitrary key-value pairs (e.g. sender ID, correlation ID).
	Metadata map[string]string `json:"metadata,omitempty"`
	// Timestamp records when the message was created.
	Timestamp time.Time `json:"timestamp"`
}

// NewTextMessage creates a text-based Message for a specific topic.
func NewTextMessage(topic, text string) Message {
	return Message{
		Type:      MessageTypeText,
		Topic:     topic,
		Payload:   []byte(text),
		Timestamp: time.Now().UTC(),
	}
}

// NewBinaryMessage creates a binary Message for a specific topic.
func NewBinaryMessage(topic string, data []byte) Message {
	return Message{
		Type:      MessageTypeBinary,
		Topic:     topic,
		Payload:   data,
		Timestamp: time.Now().UTC(),
	}
}

// NewJSONMessage serializes an arbitrary struct/map into a JSON text Message.
func NewJSONMessage(topic string, v any) (Message, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return Message{}, err
	}
	return Message{
		Type:      MessageTypeText,
		Topic:     topic,
		Payload:   b,
		Timestamp: time.Now().UTC(),
	}, nil
}
