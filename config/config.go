// Package config provides application configuration loading via environment
// variables (APP_* prefix) and an optional YAML file using Viper.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Load reads configuration from environment variables (APP_* prefix) and
// optionally from cfgFile (a YAML file path). Pass an empty string to skip
// file-based configuration.
func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	// Defaults
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

	// Environment variables
	v.SetEnvPrefix("APP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Optional config file
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("config: read config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}
	return &cfg, nil
}

// MustLoad is like Load but panics on error.
func MustLoad(cfgFile string) *Config {
	cfg, err := Load(cfgFile)
	if err != nil {
		panic(fmt.Sprintf("config: MustLoad: %v", err))
	}
	return cfg
}
