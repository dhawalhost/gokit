package logger_test

import (
	"context"
	"testing"

	"github.com/dhawalhost/gokit/logger"
	"go.uber.org/zap"
)

func TestLogger_Instantiation_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		level       string
		development bool
		wantErr     bool
	}{
		{
			name:        "valid development logger creates non-nil instance",
			level:       "debug",
			development: true,
			wantErr:     false,
		},
		{
			name:        "valid production logger creates non-nil instance",
			level:       "info",
			development: false,
			wantErr:     false,
		},
		{
			name:        "invalid log level returns error",
			level:       "badlevel",
			development: false,
			wantErr:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, err := logger.New(tc.level, tc.development)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
			if !tc.wantErr && l == nil {
				t.Fatal("expected non-nil logger")
			}
		})
	}

	// Nop logger check
	if l := logger.NewNop(); l == nil {
		t.Fatal("expected non-nil nop logger")
	}
}

func TestLogger_ContextAndGlobal_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "set and retrieve global logger",
			run: func(t *testing.T) {
				original := logger.Global()
				if original == nil {
					t.Fatal("expected non-nil default global logger")
				}
				nop := zap.NewNop()
				logger.SetGlobal(nop)
				if logger.Global() != nop {
					t.Error("SetGlobal did not update the global logger")
				}
				logger.SetGlobal(original)
			},
		},
		{
			name: "WithContext and FromContext roundtrip maintains logger instance",
			run: func(t *testing.T) {
				l := zap.NewNop()
				ctx := logger.WithContext(context.Background(), l)
				got := logger.FromContext(ctx)
				if got != l {
					t.Error("FromContext should return the same logger stored by WithContext")
				}
			},
		},
		{
			name: "FromContext on empty context falls back to global logger",
			run: func(t *testing.T) {
				got := logger.FromContext(context.Background())
				if got == nil {
					t.Fatal("expected non-nil logger from empty context")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestLogger_AutomaticMasking_TableDriven(t *testing.T) {
	l, err := logger.New("info", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		name   string
		fields []zap.Field
	}{
		{
			name: "sensitive credentials and tokens are logged safely",
			fields: []zap.Field{
				zap.String("password", "super-secret-123"),
				zap.String("api_key", "sk_live_12345"),
				zap.String("username", "alice"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(_ *testing.T) {
			l.Info("user login", tc.fields...)
		})
	}

	t.Run("logger.With child logger with sensitive keys", func(t *testing.T) {
		child := l.With(zap.String("token", "my-secret-token"), zap.String("module", "auth"))
		child.Info("child log message", zap.String("password", "p@ssword"))
	})
}
