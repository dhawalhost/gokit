package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
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

	cfg, err := Load("")
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

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":7070" {
		t.Errorf("expected :7070, got %q", cfg.Server.Addr)
	}
}

func TestLoadBadFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
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
	MustLoad("/nonexistent/path/config.yaml")
}

func TestConfig_StructMasking(t *testing.T) {
	cfg := Config{
		Database: DatabaseConfig{
			DSN: "postgres://dbuser:supersecretpass@localhost:5432/production_db?sslmode=disable",
		},
		Redis: RedisConfig{
			Addr:     "localhost:6379",
			Password: "redis-secret-token",
		},
		JWT: JWTConfig{
			Secret: "jwt-signing-key-very-secret",
		},
	}

	// 1. Test Redis JSON & String Masking
	redisJSON, err := json.Marshal(cfg.Redis)
	if err != nil {
		t.Fatalf("json.Marshal(cfg.Redis): %v", err)
	}
	if strings.Contains(string(redisJSON), "redis-secret-token") {
		t.Errorf("Redis password leaked in JSON: %s", redisJSON)
	}
	if !strings.Contains(string(redisJSON), "********") {
		t.Errorf("Redis password not redacted with ******** in JSON: %s", redisJSON)
	}
	if strings.Contains(cfg.Redis.String(), "redis-secret-token") {
		t.Errorf("Redis password leaked in String(): %s", cfg.Redis.String())
	}

	// 2. Test Database DSN Masking (URL format uses Go's standard net/url xxxxx)
	dbJSON, err := json.Marshal(cfg.Database)
	if err != nil {
		t.Fatalf("json.Marshal(cfg.Database): %v", err)
	}
	if strings.Contains(string(dbJSON), "supersecretpass") {
		t.Errorf("Database password leaked in JSON: %s", dbJSON)
	}
	if !strings.Contains(string(dbJSON), "dbuser:xxxxx@localhost:5432") {
		t.Errorf("Database URL password not redacted with xxxxx: %s", dbJSON)
	}

	// 3. Test Database DSN Masking (Key-Value format uses ********)
	kvDB := DatabaseConfig{
		DSN: "host=localhost user=admin password=kvsecret dbname=mydb",
	}
	kvJSON, _ := json.Marshal(kvDB)
	if strings.Contains(string(kvJSON), "kvsecret") {
		t.Errorf("Database KV password leaked in JSON: %s", kvJSON)
	}
	if !strings.Contains(string(kvJSON), "password=********") {
		t.Errorf("Database KV password not redacted with ******** in JSON: %s", kvJSON)
	}

	// 4. Test JWT Secret Masking
	jwtJSON, err := json.Marshal(cfg.JWT)
	if err != nil {
		t.Fatalf("json.Marshal(cfg.JWT): %v", err)
	}
	if strings.Contains(string(jwtJSON), "jwt-signing-key-very-secret") {
		t.Errorf("JWT secret leaked in JSON: %s", jwtJSON)
	}
	if !strings.Contains(string(jwtJSON), "********") {
		t.Errorf("JWT secret not redacted with ******** in JSON: %s", jwtJSON)
	}

	// 5. Test Top-Level Config Masked() method
	masked := cfg.Masked()
	if masked.Redis.Password != "********" {
		t.Errorf("Masked() failed for Redis password: got %s", masked.Redis.Password)
	}
	if masked.JWT.Secret != "********" {
		t.Errorf("Masked() failed for JWT secret: got %s", masked.JWT.Secret)
	}
	if strings.Contains(masked.Database.DSN, "supersecretpass") {
		t.Errorf("Masked() failed for Database DSN: got %s", masked.Database.DSN)
	}

	// 6. Test Top-Level Config String() & MarshalJSON()
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal(cfg): %v", err)
	}
	if strings.Contains(string(cfgJSON), "supersecretpass") ||
		strings.Contains(string(cfgJSON), "redis-secret-token") ||
		strings.Contains(string(cfgJSON), "jwt-signing-key-very-secret") {
		t.Errorf("Secrets leaked in top-level Config JSON: %s", cfgJSON)
	}
}

func TestSecret_Masking(t *testing.T) {
	s := Secret("super-secret-password")

	// Test Stringer implementation
	if s.String() != "********" {
		t.Errorf("Secret stringer failed: got %s, want ********", s)
	}

	// Test GoStringer implementation
	if fmt.Sprintf("%#v", s) != "config.Secret(\"********\")" {
		t.Errorf("Secret go-stringer failed: got %#v", s)
	}

	// Test JSON marshaling
	b, _ := json.Marshal(s)
	if string(b) != "\"********\"" {
		t.Errorf("Secret JSON marshal failed: got %s, want \"********\"", b)
	}

	// Test Text marshaling
	txt, err := s.MarshalText()
	if err != nil || string(txt) != "********" {
		t.Errorf("Secret Text marshal failed: got %s, err %v", txt, err)
	}

	// Test Expose() & Get()
	if s.Expose() != "super-secret-password" {
		t.Errorf("Secret Expose() failed: got %s, want super-secret-password", s.Expose())
	}
	if s.Get() != "super-secret-password" {
		t.Errorf("Secret Get() failed: got %s, want super-secret-password", s.Get())
	}
}

func TestManager_HotReload(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	initialYaml := `
server:
  addr: ":8080"
log:
  level: "info"
`
	if err := os.WriteFile(cfgPath, []byte(initialYaml), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := NewManager(cfgPath)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	if m.Get().Log.Level != "info" {
		t.Errorf("Initial log level mismatch: got %s", m.Get().Log.Level)
	}

	// Setup update hook
	updated := make(chan bool, 1)
	m.OnUpdate(func(cfg *Config) {
		if cfg.Log.Level == "debug" {
			updated <- true
		}
	})

	m.Watch()

	// Update the file
	newYaml := `
server:
  addr: ":8080"
log:
  level: "debug"
`
	if err := os.WriteFile(cfgPath, []byte(newYaml), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-updated:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Timed out waiting for config reload")
	}

	if m.Get().Log.Level != "debug" {
		t.Errorf("Updated log level mismatch: got %s", m.Get().Log.Level)
	}
}
