// Package config provides application configuration loading via environment
// variables (APP_* prefix) and an optional YAML file using Viper.
package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"github.com/subosito/gotenv"
)

// Manager handles the lifecycle of the application configuration,
// including initial loading and dynamic hot-reloading.
type Manager struct {
	v   *viper.Viper
	mu  sync.RWMutex
	cfg *Config

	onUpdate []func(*Config)
}

// NewManager creates a Manager that reads configuration from environment
// variables (APP_* prefix) and optionally from cfgFile (YAML or .env file).
// It supports hot-reloading via Watch() and update callbacks via OnUpdate().
func NewManager(cfgFile string) (*Manager, error) {
	v := viper.New()

	// Defaults
	setDefaults(v)

	// Environment variables
	v.SetEnvPrefix("APP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Optional config file
	if cfgFile != "" {
		if strings.HasSuffix(cfgFile, ".env") || filepath.Base(cfgFile) == ".env" {
			if err := gotenv.Load(cfgFile); err != nil {
				return nil, fmt.Errorf("config: load env file: %w", err)
			}
		} else {
			v.SetConfigFile(cfgFile)
			if err := v.ReadInConfig(); err != nil {
				return nil, fmt.Errorf("config: read config file: %w", err)
			}
		}
	}

	m := &Manager{v: v}
	if err := m.reload(); err != nil {
		return nil, err
	}

	return m, nil
}

// MustNewManager is like NewManager but panics on error.
func MustNewManager(cfgFile string) *Manager {
	m, err := NewManager(cfgFile)
	if err != nil {
		panic(fmt.Sprintf("config: MustNewManager: %v", err))
	}
	return m
}

// Load reads configuration from environment variables (APP_* prefix) and
// optionally from cfgFile (a YAML or .env file path). Pass an empty string to skip
// file-based configuration.
func Load(cfgFile string) (*Config, error) {
	m, err := NewManager(cfgFile)
	if err != nil {
		return nil, err
	}
	return m.Get(), nil
}

// MustLoad is like Load but panics on error.
func MustLoad(cfgFile string) *Config {
	cfg, err := Load(cfgFile)
	if err != nil {
		panic(fmt.Sprintf("config: MustLoad: %v", err))
	}
	return cfg
}

// Get returns a thread-safe snapshot of the current configuration.
func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// OnUpdate registers a callback that will be executed whenever the
// configuration is reloaded.
func (m *Manager) OnUpdate(f func(*Config)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUpdate = append(m.onUpdate, f)
}

// Watch starts watching the config file for changes and reloads automatically.
func (m *Manager) Watch() {
	m.v.OnConfigChange(func(e fsnotify.Event) {
		_ = m.reload()
	})
	m.v.WatchConfig()
}

func (m *Manager) reload() error {
	var cfg Config
	if err := m.v.Unmarshal(&cfg); err != nil {
		return fmt.Errorf("config: unmarshal: %w", err)
	}

	m.mu.Lock()
	m.cfg = &cfg
	handlers := m.onUpdate
	m.mu.Unlock()

	// Run handlers outside of lock
	for _, h := range handlers {
		h(&cfg)
	}

	return nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.addr", ":8080")
	v.SetDefault("server.read_timeout", 30*time.Second)
	v.SetDefault("server.write_timeout", 30*time.Second)
	v.SetDefault("server.idle_timeout", 120*time.Second)
	v.SetDefault("server.shutdown_timeout", 30*time.Second)

	v.SetDefault("database.dsn", "")
	v.SetDefault("database.migrations_path", "")
	v.SetDefault("database.max_open_conns", 25)
	v.SetDefault("database.max_idle_conns", 5)
	v.SetDefault("database.conn_max_lifetime", 5*time.Minute)

	v.SetDefault("redis.addr", "")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.dial_timeout", 5*time.Second)
	v.SetDefault("redis.read_timeout", 3*time.Second)
	v.SetDefault("redis.write_timeout", 3*time.Second)
	v.SetDefault("redis.pool_size", 10)
	v.SetDefault("redis.pool_timeout", 4*time.Second)

	v.SetDefault("jwt.secret", "")
	v.SetDefault("jwt.expiry", 15*time.Minute)
	v.SetDefault("jwt.issuer", "")

	v.SetDefault("log.level", "info")
	v.SetDefault("log.development", false)

	v.SetDefault("telemetry.enabled", false)
	v.SetDefault("telemetry.otlp_endpoint", "")
	v.SetDefault("telemetry.service_name", "")
}
