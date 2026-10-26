package kafka

import (
	"context"
	"crypto/tls"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// MessageWriter abstracts kafkago.Writer for testing and custom wrappers.
type MessageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
	Close() error
}

// MessageReader abstracts kafkago.Reader for testing and custom wrappers.
type MessageReader interface {
	FetchMessage(ctx context.Context) (kafkago.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
	Close() error
}

// ReaderFactory instantiates a MessageReader for a specific topic and consumer group.
type ReaderFactory func(topic string, groupID string, cfg Config) (MessageReader, error)

// Config configures the Kafka eventbus adapter.
type Config struct {
	Brokers       []string
	ClientID      string
	Balancer      kafkago.Balancer
	RequiredAcks  kafkago.RequiredAcks
	MaxAttempts   int
	BatchSize     int
	BatchTimeout  time.Duration
	WriteTimeout  time.Duration
	ReadTimeout   time.Duration
	Compression   kafkago.Compression
	TLS           *tls.Config
	Dialer        *kafkago.Dialer
	Writer        MessageWriter
	ReaderFactory ReaderFactory
}

// DefaultConfig returns safe, high-throughput production defaults.
func DefaultConfig() Config {
	return Config{
		Balancer:     &kafkago.Hash{},
		RequiredAcks: kafkago.RequireAll,
		MaxAttempts:  3,
		BatchSize:    100,
		BatchTimeout: 10 * time.Millisecond,
		WriteTimeout: 5 * time.Second,
		ReadTimeout:  10 * time.Second,
	}
}

// Option configures Kafka bus settings.
type Option func(*Config)

// WithBrokers sets the Kafka seed broker addresses.
func WithBrokers(brokers ...string) Option {
	return func(c *Config) {
		c.Brokers = append(c.Brokers, brokers...)
	}
}

// WithClientID sets the client identifier reported to the Kafka cluster.
func WithClientID(id string) Option {
	return func(c *Config) {
		c.ClientID = id
	}
}

// WithBalancer customizes the partition balancer for published messages.
func WithBalancer(b kafkago.Balancer) Option {
	return func(c *Config) {
		if b != nil {
			c.Balancer = b
		}
	}
}

// WithRequiredAcks sets the required broker acknowledgments for writes.
func WithRequiredAcks(acks kafkago.RequiredAcks) Option {
	return func(c *Config) {
		c.RequiredAcks = acks
	}
}

// WithBatchSize sets the maximum message batch size before sending.
func WithBatchSize(size int) Option {
	return func(c *Config) {
		if size > 0 {
			c.BatchSize = size
		}
	}
}

// WithBatchTimeout sets the maximum linger delay before dispatching a batch.
func WithBatchTimeout(d time.Duration) Option {
	return func(c *Config) {
		if d > 0 {
			c.BatchTimeout = d
		}
	}
}

// WithWriteTimeout sets the maximum timeout for write operations.
func WithWriteTimeout(d time.Duration) Option {
	return func(c *Config) {
		if d > 0 {
			c.WriteTimeout = d
		}
	}
}

// WithReadTimeout sets the maximum timeout for read operations.
func WithReadTimeout(d time.Duration) Option {
	return func(c *Config) {
		if d > 0 {
			c.ReadTimeout = d
		}
	}
}

// WithCompression configures message payload compression.
func WithCompression(codec kafkago.Compression) Option {
	return func(c *Config) {
		c.Compression = codec
	}
}

// WithTLS configures TLS security for Kafka broker connections.
func WithTLS(tlsConfig *tls.Config) Option {
	return func(c *Config) {
		c.TLS = tlsConfig
	}
}

// WithDialer provides a custom kafkago.Dialer.
func WithDialer(d *kafkago.Dialer) Option {
	return func(c *Config) {
		c.Dialer = d
	}
}

// WithWriter injects a custom MessageWriter (useful for testing).
func WithWriter(w MessageWriter) Option {
	return func(c *Config) {
		c.Writer = w
	}
}

// WithReaderFactory injects a custom ReaderFactory (useful for testing).
func WithReaderFactory(fn ReaderFactory) Option {
	return func(c *Config) {
		c.ReaderFactory = fn
	}
}
