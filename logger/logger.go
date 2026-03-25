package logger

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	global *zap.Logger
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
func Global() *zap.Logger {
	if global == nil {
		l, _ := zap.NewProduction()
		global = l
	}
	return global
}

// SetGlobal replaces the global logger instance.
func SetGlobal(l *zap.Logger) {
	global = l
}
