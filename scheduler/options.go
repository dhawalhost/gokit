package scheduler

import (
	"time"

	"github.com/dhawalhost/gokit/distlock"
)

// Hook provides lifecycle callbacks for observing scheduled job executions.
type Hook struct {
	OnJobStart   func(snapshot JobSnapshot)
	OnJobSuccess func(snapshot JobSnapshot, duration time.Duration)
	OnJobFailure func(snapshot JobSnapshot, duration time.Duration, err error)
	OnJobSkipped func(snapshot JobSnapshot, reason string)
}

// Config defines the runtime configuration of the Scheduler.
type Config struct {
	Location     *time.Location
	Locker       distlock.Locker
	TickInterval time.Duration
	Hooks        []Hook
}

// Option configures the Scheduler.
type Option func(*Config)

// WithLocation sets the default timezone location for cron expression evaluation.
func WithLocation(loc *time.Location) Option {
	return func(c *Config) {
		if loc != nil {
			c.Location = loc
		}
	}
}

// WithLocker configures a distributed locker for cluster-wide job synchronization.
// When jobs specify a lock key, this locker is used to guarantee single-replica execution.
func WithLocker(locker distlock.Locker) Option {
	return func(c *Config) {
		c.Locker = locker
	}
}

// WithTickInterval overrides the default scheduler evaluation interval (default: 500ms).
func WithTickInterval(d time.Duration) Option {
	return func(c *Config) {
		if d > 0 {
			c.TickInterval = d
		}
	}
}

// WithHook appends a lifecycle observation hook to the scheduler.
func WithHook(hook Hook) Option {
	return func(c *Config) {
		c.Hooks = append(c.Hooks, hook)
	}
}
