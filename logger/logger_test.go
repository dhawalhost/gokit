package logger_test

import (
	"context"
	"testing"

	"github.com/dhawalhost/gokit/logger"
	"go.uber.org/zap"
)

func TestNewDevelopment(t *testing.T) {
	l, err := logger.New("debug", true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if l == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewProduction(t *testing.T) {
	l, err := logger.New("info", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if l == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewInvalidLevel(t *testing.T) {
	_, err := logger.New("badlevel", false)
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
}

func TestNewNop(t *testing.T) {
	l := logger.NewNop()
	if l == nil {
		t.Fatal("expected non-nil nop logger")
	}
}

func TestGlobal(t *testing.T) {
	l := logger.Global()
	if l == nil {
		t.Fatal("expected non-nil global logger")
	}
}

func TestSetGlobal(t *testing.T) {
	original := logger.Global()
	nop := zap.NewNop()
	logger.SetGlobal(nop)
	if logger.Global() != nop {
		t.Error("SetGlobal did not update the global logger")
	}
	// Restore
	logger.SetGlobal(original)
}

func TestWithContextFromContext(t *testing.T) {
	l := zap.NewNop()
	ctx := logger.WithContext(context.Background(), l)
	got := logger.FromContext(ctx)
	if got != l {
		t.Error("FromContext should return the same logger stored by WithContext")
	}
}

func TestFromContextFallsBack(t *testing.T) {
	got := logger.FromContext(context.Background())
	if got == nil {
		t.Fatal("expected non-nil logger from empty context")
	}
}
