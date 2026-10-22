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

func TestConfig_Load_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T) (string, func())
		wantErr   bool
		checkConf func(t *testing.T, cfg *Config)
	}{
		{
			name: "default configuration without file or env overrides",
			setup: func(_ *testing.T) (string, func()) {
				return "", func() {}
			},
			wantErr: false,
			checkConf: func(t *testing.T, cfg *Config) {
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
			},
		},
		{
			name: "environment variable overrides apply correctly",
			setup: func(t *testing.T) (string, func()) {
				t.Setenv("APP_SERVER_ADDR", ":9090")
				t.Setenv("APP_LOG_LEVEL", "debug")
				return "", func() {}
			},
			wantErr: false,
			checkConf: func(t *testing.T, cfg *Config) {
				if cfg.Server.Addr != ":9090" {
					t.Errorf("expected :9090, got %q", cfg.Server.Addr)
				}
				if cfg.Log.Level != "debug" {
					t.Errorf("expected 'debug', got %q", cfg.Log.Level)
				}
			},
		},
		{
			name: "loads settings from valid YAML file",
			setup: func(t *testing.T) (string, func()) {
				f, err := os.CreateTemp(t.TempDir(), "*.yaml")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.WriteString("server:\n  addr: \":7070\"\n")
				_ = f.Close()
				return f.Name(), func() { _ = os.Remove(f.Name()) }
			},
			wantErr: false,
			checkConf: func(t *testing.T, cfg *Config) {
				if cfg.Server.Addr != ":7070" {
					t.Errorf("expected :7070, got %q", cfg.Server.Addr)
				}
			},
		},
		{
			name: "nonexistent file path returns error",
			setup: func(_ *testing.T) (string, func()) {
				return "/nonexistent/path/config.yaml", func() {}
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, cleanup := tc.setup(t)
			defer cleanup()

			cfg, err := Load(path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
			if !tc.wantErr && tc.checkConf != nil {
				tc.checkConf(t, cfg)
			}
		})
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

func TestConfig_StructMasking_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "redis config masks password in JSON and String",
			run: func(t *testing.T) {
				r := RedisConfig{Addr: "localhost:6379", Password: "redis-secret-token"}
				redisJSON, err := json.Marshal(r)
				if err != nil {
					t.Fatalf("json.Marshal(r): %v", err)
				}
				if strings.Contains(string(redisJSON), "redis-secret-token") {
					t.Errorf("Redis password leaked in JSON: %s", redisJSON)
				}
				if !strings.Contains(string(redisJSON), "********") {
					t.Errorf("Redis password not redacted with ******** in JSON: %s", redisJSON)
				}
				if strings.Contains(r.String(), "redis-secret-token") {
					t.Errorf("Redis password leaked in String(): %s", r.String())
				}
			},
		},
		{
			name: "database DSN masks URL format password",
			run: func(t *testing.T) {
				db := DatabaseConfig{
					DSN: "postgres://dbuser:supersecretpass@localhost:5432/production_db?sslmode=disable",
				}
				dbJSON, err := json.Marshal(db)
				if err != nil {
					t.Fatalf("json.Marshal(db): %v", err)
				}
				if strings.Contains(string(dbJSON), "supersecretpass") {
					t.Errorf("Database password leaked in JSON: %s", dbJSON)
				}
				if !strings.Contains(string(dbJSON), "dbuser:xxxxx@localhost:5432") {
					t.Errorf("Database URL password not redacted with xxxxx: %s", dbJSON)
				}
			},
		},
		{
			name: "database DSN masks key-value format password",
			run: func(t *testing.T) {
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
			},
		},
		{
			name: "JWT secret masked in JSON",
			run: func(t *testing.T) {
				j := JWTConfig{Secret: "jwt-signing-key-very-secret"}
				jwtJSON, err := json.Marshal(j)
				if err != nil {
					t.Fatalf("json.Marshal(j): %v", err)
				}
				if strings.Contains(string(jwtJSON), "jwt-signing-key-very-secret") {
					t.Errorf("JWT secret leaked in JSON: %s", jwtJSON)
				}
				if !strings.Contains(string(jwtJSON), "********") {
					t.Errorf("JWT secret not redacted with ******** in JSON: %s", jwtJSON)
				}
			},
		},
		{
			name: "top-level Config Masked method redacts all secrets",
			run: func(t *testing.T) {
				cfg := Config{
					Database: DatabaseConfig{DSN: "postgres://dbuser:supersecretpass@localhost:5432/db"},
					Redis:    RedisConfig{Password: "redis-secret-token"},
					JWT:      JWTConfig{Secret: "jwt-signing-key"},
				}
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
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestSecret_Masking_TableDriven(t *testing.T) {
	s := Secret("super-secret-password")

	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "Stringer prints masked asterisks",
			test: func(t *testing.T) {
				if s.String() != "********" {
					t.Errorf("Secret stringer failed: got %s, want ********", s)
				}
			},
		},
		{
			name: "GoStringer prints type and asterisks",
			test: func(t *testing.T) {
				if fmt.Sprintf("%#v", s) != "config.Secret(\"********\")" {
					t.Errorf("Secret go-stringer failed: got %#v", s)
				}
			},
		},
		{
			name: "JSON marshal produces quoted asterisks",
			test: func(t *testing.T) {
				b, _ := json.Marshal(s)
				if string(b) != "\"********\"" {
					t.Errorf("Secret JSON marshal failed: got %s, want \"********\"", b)
				}
			},
		},
		{
			name: "Text marshal produces plain asterisks",
			test: func(t *testing.T) {
				txt, err := s.MarshalText()
				if err != nil || string(txt) != "********" {
					t.Errorf("Secret Text marshal failed: got %s, err %v", txt, err)
				}
			},
		},
		{
			name: "Expose and Get reveal original plaintext",
			test: func(t *testing.T) {
				if s.Expose() != "super-secret-password" {
					t.Errorf("Secret Expose() failed: got %s, want super-secret-password", s.Expose())
				}
				if s.Get() != "super-secret-password" {
					t.Errorf("Secret Get() failed: got %s, want super-secret-password", s.Get())
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.test)
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

	updated := make(chan bool, 1)
	m.OnUpdate(func(cfg *Config) {
		if cfg.Log.Level == "debug" {
			updated <- true
		}
	})

	m.Watch()

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

func TestConfigTypes_StringAndLogValue_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "Config, DatabaseConfig, RedisConfig, and TLSConfig safe stringers and slog values",
			run: func(t *testing.T) {
				cfg := Config{
					Server: ServerConfig{Addr: ":8080"},
					Database: DatabaseConfig{
						DSN: "postgres://user:secretpass@localhost:5432/db",
					},
					Redis: RedisConfig{
						Addr:     "localhost:6379",
						Password: "redis-secret-pass",
					},
					JWT: JWTConfig{
						Secret: "super-secret-jwt-key",
					},
				}

				// Check Config Stringer and LogValue
				if strings.Contains(cfg.String(), "secretpass") {
					t.Errorf("Config.String() leaked DSN password: %s", cfg.String())
				}
				if cfg.GoString() != cfg.String() {
					t.Errorf("Config.GoString mismatch")
				}
				if cfg.LogValue().Kind().String() != "String" {
					t.Errorf("expected string slog value")
				}

				// DatabaseConfig
				if strings.Contains(cfg.Database.String(), "secretpass") {
					t.Errorf("DatabaseConfig.String leaked credentials")
				}
				if cfg.Database.GoString() != cfg.Database.String() {
					t.Errorf("DatabaseConfig.GoString mismatch")
				}
				_ = cfg.Database.LogValue()

				// RedisConfig
				if strings.Contains(cfg.Redis.String(), "redis-secret-pass") {
					t.Errorf("RedisConfig.String leaked credentials")
				}
				if cfg.Redis.GoString() != cfg.Redis.String() {
					t.Errorf("RedisConfig.GoString mismatch")
				}
				_ = cfg.Redis.LogValue()

				// JWTConfig
				if strings.Contains(cfg.JWT.String(), "super-secret-jwt-key") {
					t.Errorf("JWTConfig.String leaked secret")
				}
				if cfg.JWT.GoString() != cfg.JWT.String() {
					t.Errorf("JWTConfig.GoString mismatch")
				}
				_ = cfg.JWT.LogValue()
			},
		},
		{
			name: "Secret LogValue and UnmarshalJSON",
			run: func(t *testing.T) {
				var s Secret
				err := json.Unmarshal([]byte(`"my-super-secret-api-token"`), &s)
				if err != nil {
					t.Fatalf("Secret UnmarshalJSON error: %v", err)
				}
				if s.Expose() != "my-super-secret-api-token" {
					t.Errorf("expected my-super-secret-api-token, got %s", s.Expose())
				}
				if s.String() != MaskedSecretPlaceholder {
					t.Errorf("expected masked placeholder in String(), got %s", s.String())
				}
				if s.LogValue().String() != MaskedSecretPlaceholder {
					t.Errorf("expected masked in LogValue")
				}
			},
		},
		{
			name: "MustNewManager succeeds for valid and panics for invalid",
			run: func(t *testing.T) {
				m := MustNewManager("")
				if m == nil {
					t.Fatal("expected non-nil manager from MustNewManager")
				}

				loaded := MustLoad("")
				if loaded == nil {
					t.Fatal("expected non-nil config from MustLoad")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
