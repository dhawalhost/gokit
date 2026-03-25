package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/config"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":8080" {
		t.Errorf("expected :8080, got %q", cfg.Server.Addr)
	}
	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Errorf("expected 30s read timeout, got %v", cfg.Server.ReadTimeout)
	}
	if cfg.Server.WriteTimeout != 30*time.Second {
		t.Errorf("expected 30s write timeout, got %v", cfg.Server.WriteTimeout)
	}
	if cfg.Server.IdleTimeout != 120*time.Second {
		t.Errorf("expected 120s idle timeout, got %v", cfg.Server.IdleTimeout)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("expected log level 'info', got %q", cfg.Log.Level)
	}
	if cfg.Database.MaxOpenConns != 25 {
		t.Errorf("expected 25 max open conns, got %d", cfg.Database.MaxOpenConns)
	}
}

func TestLoadEnvOverride(t *testing.T) {
	t.Setenv("APP_SERVER_ADDR", ":9090")
	t.Setenv("APP_LOG_LEVEL", "debug")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("expected :9090, got %q", cfg.Server.Addr)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("expected 'debug', got %q", cfg.Log.Level)
	}
}

func TestLoadYAMLFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("server:\n  addr: \":7070\"\n")
	_ = f.Close()

	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":7070" {
		t.Errorf("expected :7070, got %q", cfg.Server.Addr)
	}
}

func TestLoadBadFile(t *testing.T) {
	_, err := config.Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent config file")
	}
}

func TestMustLoadPanicsOnError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected MustLoad to panic")
		}
	}()
	config.MustLoad("/nonexistent/path/config.yaml")
}
