package kafka

import "errors"

var (
	// ErrBrokersRequired is returned when no broker addresses are provided to the Kafka bus.
	ErrBrokersRequired = errors.New("kafka: at least one broker address is required")

	// ErrBusClosed is returned when attempting an operation on a closed Kafka bus.
	ErrBusClosed = errors.New("kafka: event bus is closed")

	// ErrInvalidMessage is returned when a published message is nil or lacks essential fields.
	ErrInvalidMessage = errors.New("kafka: invalid event message")

	// ErrTopicRequired is returned when subscribing with an empty topic string.
	ErrTopicRequired = errors.New("kafka: topic name cannot be empty")
)
