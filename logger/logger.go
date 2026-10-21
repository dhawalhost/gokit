package logger

import (
	"fmt"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	global     *zap.Logger
	globalOnce sync.Once
	globalMu   sync.RWMutex
)

// isSensitiveKey checks whether a log field key is likely to contain sensitive data.
func isSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "password") ||
		strings.Contains(k, "secret") ||
		strings.Contains(k, "token") ||
		strings.Contains(k, "authorization") ||
		strings.Contains(k, "cookie") ||
		strings.Contains(k, "api_key") ||
		strings.Contains(k, "apikey") ||
		strings.Contains(k, "dsn")
}

// maskFields replaces sensitive field values with a standard masked placeholder.
func maskFields(fields []zapcore.Field) []zapcore.Field {
	if len(fields) == 0 {
		return fields
	}
	masked := make([]zapcore.Field, len(fields))
	copy(masked, fields)
	for i := range masked {
		if isSensitiveKey(masked[i].Key) {
			masked[i] = zap.String(masked[i].Key, "********")
		}
	}
	return masked
}

type maskingCore struct {
	zapcore.Core
}

func (mc *maskingCore) With(fields []zapcore.Field) zapcore.Core {
	return &maskingCore{Core: mc.Core.With(maskFields(fields))}
}

func (mc *maskingCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	return mc.Core.Write(ent, maskFields(fields))
}

func (mc *maskingCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if mc.Enabled(ent.Level) {
		return ce.AddCore(ent, mc)
	}
	return ce
}

// WrapWithMasking wraps an existing zapcore.Core with automatic key redaction.
func WrapWithMasking(core zapcore.Core) zapcore.Core {
	return &maskingCore{Core: core}
}

// New creates a new zap.Logger with the given level, development flag,
// and automatic sensitive key redaction.
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

	baseLogger, err := cfg.Build()
	if err != nil {
		return nil, err
	}

	// Wrap core with automatic masking
	return zap.New(
		WrapWithMasking(baseLogger.Core()),
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
	), nil
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
			l, err := New("info", false)
			if err != nil {
				l = zap.NewNop()
			}
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
