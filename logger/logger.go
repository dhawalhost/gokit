package logger

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	global     *zap.Logger
	globalOnce sync.Once
	globalMu   sync.RWMutex
)

// New creates a new zap.Logger with the given level and development flag.
func New(level string, isDevelopment bool) (*zap.Logger, error) {
	var cfg zap.Config
	if isDevelopment {
		cfg = zap.NewDevelopmentConfig()
	} else {
		cfg = zap.NewProductionConfig()
	}

	var lvl zapcore.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("logger: invalid level %q: %w", level, err)
	}
	cfg.Level = zap.NewAtomicLevelAt(lvl)
	return cfg.Build()
}

// NewNop returns a no-op logger useful for tests.
func NewNop() *zap.Logger { return zap.NewNop() }

// Global returns the global logger instance.
// If not set via SetGlobal, it returns a default production logger (initialized once).
func Global() *zap.Logger {
	globalMu.RLock()
	if global != nil {
		globalMu.RUnlock()
		return global
	}
	globalMu.RUnlock()

	// Initialize default logger once
	globalOnce.Do(func() {
		globalMu.Lock()
		defer globalMu.Unlock()
		if global == nil {
			l, _ := zap.NewProduction()
			global = l
		}
	})

	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

// SetGlobal replaces the global logger instance.
func SetGlobal(l *zap.Logger) {
	globalMu.Lock()
	defer globalMu.Unlock()
	global = l
}
