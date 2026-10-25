package batcher

import (
	"time"
)

// BackpressureStrategy specifies the action to take when the batcher queue is full.
type BackpressureStrategy int

const (
	// BackpressureBlock blocks caller until queue capacity is available or context is canceled.
	BackpressureBlock BackpressureStrategy = iota
	// BackpressureDropOldest evicts the oldest item in the queue to admit the new item.
	BackpressureDropOldest
	// BackpressureReject immediately rejects the item returning ErrQueueFull.
	BackpressureReject
)

// Config configures the behavior of a Batcher.
type Config struct {
	MaxBatchSize  int
	FlushInterval time.Duration
	QueueSize     int
	Workers       int
	RetryCount    int
	RetryBackoff  time.Duration
	Backpressure  BackpressureStrategy
}

// DefaultConfig returns recommended production defaults for micro-batching.
func DefaultConfig() Config {
	return Config{
		MaxBatchSize:  100,
		FlushInterval: 50 * time.Millisecond,
		QueueSize:     1000,
		Workers:       1,
		RetryCount:    0,
		RetryBackoff:  10 * time.Millisecond,
		Backpressure:  BackpressureBlock,
	}
}

// Option modifies a Config instance.
type Option func(*Config)

// WithMaxBatchSize sets the maximum number of items dispatched in a single batch.
func WithMaxBatchSize(size int) Option {
	return func(c *Config) {
		if size > 0 {
			c.MaxBatchSize = size
		}
	}
}

// WithFlushInterval sets the maximum duration to wait before flushing a partial batch.
func WithFlushInterval(interval time.Duration) Option {
	return func(c *Config) {
		if interval > 0 {
			c.FlushInterval = interval
		}
	}
}

// WithQueueSize sets the size of the internal submission buffer.
func WithQueueSize(size int) Option {
	return func(c *Config) {
		if size > 0 {
			c.QueueSize = size
		}
	}
}

// WithWorkers sets the number of concurrent flush worker goroutines.
func WithWorkers(count int) Option {
	return func(c *Config) {
		if count > 0 {
			c.Workers = count
		}
	}
}

// WithRetries sets the retry policy when the batch processing function fails.
func WithRetries(count int, backoff time.Duration) Option {
	return func(c *Config) {
		if count >= 0 {
			c.RetryCount = count
		}
		if backoff > 0 {
			c.RetryBackoff = backoff
		}
	}
}

// WithBackpressure configures the behavior when the internal queue is full.
func WithBackpressure(strategy BackpressureStrategy) Option {
	return func(c *Config) {
		c.Backpressure = strategy
	}
}
