package migration

import (
	"time"

	"github.com/dhawalhost/gokit/distlock"
)

// Config holds the configuration options for a Migrator.
type Config struct {
	TableName     string
	Locker        distlock.Locker
	LockKey       string
	LockTTL       time.Duration
	NoTransaction bool
}

// DefaultConfig returns recommended production defaults.
func DefaultConfig() Config {
	return Config{
		TableName: "schema_migrations",
		LockKey:   "gokit:schema_migration:lock",
		LockTTL:   30 * time.Second,
	}
}

// Option modifies a Config instance.
type Option func(*Config)

// WithTableName overrides the default migrations metadata table name (default: "schema_migrations").
func WithTableName(name string) Option {
	return func(c *Config) {
		if name != "" {
			c.TableName = name
		}
	}
}

// WithLocker configures a distributed lock to serialize migrations across multiple Kubernetes replicas.
func WithLocker(locker distlock.Locker, key string, ttl time.Duration) Option {
	return func(c *Config) {
		c.Locker = locker
		if key != "" {
			c.LockKey = key
		}
		if ttl > 0 {
			c.LockTTL = ttl
		}
	}
}

// WithNoTransaction disables automatic transaction wrapping for migrations (required for concurrent index creation).
func WithNoTransaction() Option {
	return func(c *Config) {
		c.NoTransaction = true
	}
}
