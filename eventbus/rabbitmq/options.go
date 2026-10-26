package rabbitmq

import (
	"context"
	"crypto/tls"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Channel abstracts the AMQP channel methods used by Bus for testing and mock injection.
type Channel interface {
	PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error
	ConsumeWithContext(ctx context.Context, queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error)
	QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error)
	QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error
	ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error
	Qos(prefetchCount, prefetchSize int, global bool) error
	Close() error
}

// ChannelFactory creates new AMQP channels on demand.
type ChannelFactory func() (Channel, error)

// Config holds configuration parameters for the RabbitMQ EventBus.
type Config struct {
	URL            string
	Exchange       string
	ExchangeType   string
	Durable        bool
	PrefetchCount  int
	RequeueOnNack  bool
	TLS            *tls.Config
	ChannelFactory ChannelFactory
}

// DefaultConfig returns resilient, production-ready defaults.
func DefaultConfig() Config {
	return Config{
		Exchange:      "events",
		ExchangeType:  "topic",
		Durable:       true,
		PrefetchCount: 50,
		RequeueOnNack: true,
	}
}

// Option configures RabbitMQ bus settings.
type Option func(*Config)

// WithURL sets the AMQP connection URL.
func WithURL(url string) Option {
	return func(c *Config) {
		c.URL = url
	}
}

// WithExchange sets the target AMQP exchange name.
func WithExchange(exchange string) Option {
	return func(c *Config) {
		if exchange != "" {
			c.Exchange = exchange
		}
	}
}

// WithExchangeType sets the exchange kind (topic, direct, fanout).
func WithExchangeType(kind string) Option {
	return func(c *Config) {
		if kind != "" {
			c.ExchangeType = kind
		}
	}
}

// WithDurable sets whether declared exchanges and queues are durable.
func WithDurable(durable bool) Option {
	return func(c *Config) {
		c.Durable = durable
	}
}

// WithPrefetchCount sets the prefetch QoS window for consumers.
func WithPrefetchCount(n int) Option {
	return func(c *Config) {
		if n > 0 {
			c.PrefetchCount = n
		}
	}
}

// WithRequeueOnNack determines whether unhandled failed deliveries are requeued.
func WithRequeueOnNack(requeue bool) Option {
	return func(c *Config) {
		c.RequeueOnNack = requeue
	}
}

// WithTLS sets TLS configuration for AMQPS connections.
func WithTLS(cfg *tls.Config) Option {
	return func(c *Config) {
		c.TLS = cfg
	}
}

// WithChannelFactory injects a custom ChannelFactory (useful for unit tests).
func WithChannelFactory(fn ChannelFactory) Option {
	return func(c *Config) {
		c.ChannelFactory = fn
	}
}
