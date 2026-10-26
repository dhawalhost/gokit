package rabbitmq

import "errors"

var (
	// ErrURLRequired is returned when no AMQP connection URL is provided.
	ErrURLRequired = errors.New("rabbitmq: connection URL is required")

	// ErrBusClosed is returned when attempting an operation on a closed RabbitMQ bus.
	ErrBusClosed = errors.New("rabbitmq: event bus is closed")

	// ErrInvalidMessage is returned when a published message is nil or lacks essential fields.
	ErrInvalidMessage = errors.New("rabbitmq: invalid event message")

	// ErrTopicRequired is returned when subscribing with an empty topic string.
	ErrTopicRequired = errors.New("rabbitmq: topic name cannot be empty")
)
