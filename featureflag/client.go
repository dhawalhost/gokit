package featureflag

import (
	"context"
	"sync"
)

// Client provides typed feature flag evaluations.
type Client struct {
	mu    sync.RWMutex
	flags map[string]Flag
}

// NewClient constructs a new feature flag client.
func NewClient(flags ...Flag) *Client {
	c := &Client{
		flags: make(map[string]Flag),
	}
	for _, f := range flags {
		c.flags[f.Key] = f
	}
	return c
}

// SetFlag dynamically adds or updates a flag configuration in memory.
func (c *Client) SetFlag(flag Flag) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.flags[flag.Key] = flag
}

// BooleanValue evaluates a flag and returns a boolean value.
func (c *Client) BooleanValue(ctx context.Context, flagKey string, defaultValue bool, evalCtx Context) bool {
	c.mu.RLock()
	flag, exists := c.flags[flagKey]
	c.mu.RUnlock()

	if !exists {
		return defaultValue
	}

	val := Evaluate(flag, evalCtx)
	if b, ok := val.(bool); ok {
		return b
	}
	return defaultValue
}

// StringValue evaluates a flag and returns a string value.
func (c *Client) StringValue(ctx context.Context, flagKey string, defaultValue string, evalCtx Context) string {
	c.mu.RLock()
	flag, exists := c.flags[flagKey]
	c.mu.RUnlock()

	if !exists {
		return defaultValue
	}

	val := Evaluate(flag, evalCtx)
	if s, ok := val.(string); ok {
		return s
	}
	return defaultValue
}

// IntValue evaluates a flag and returns an int value.
func (c *Client) IntValue(ctx context.Context, flagKey string, defaultValue int, evalCtx Context) int {
	c.mu.RLock()
	flag, exists := c.flags[flagKey]
	c.mu.RUnlock()

	if !exists {
		return defaultValue
	}

	val := Evaluate(flag, evalCtx)
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case int64:
		return int(v)
	default:
		return defaultValue
	}
}

// FloatValue evaluates a flag and returns a float64 value.
func (c *Client) FloatValue(ctx context.Context, flagKey string, defaultValue float64, evalCtx Context) float64 {
	c.mu.RLock()
	flag, exists := c.flags[flagKey]
	c.mu.RUnlock()

	if !exists {
		return defaultValue
	}

	val := Evaluate(flag, evalCtx)
	switch v := val.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return defaultValue
	}
}

// NewBoolFlag is a convenience builder for simple boolean flags.
func NewBoolFlag(key string, enabled bool, defaultValue bool) Flag {
	defaultVar := "false"
	if defaultValue {
		defaultVar = "true"
	}
	return Flag{
		Key:              key,
		Enabled:          enabled,
		DefaultVariation: defaultVar,
		Variations: map[string]any{
			"true":  true,
			"false": false,
		},
	}
}
