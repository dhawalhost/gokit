package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MaskedSecretPlaceholder is the standard placeholder used to redact sensitive fields.
const MaskedSecretPlaceholder = "********"

var dsnPasswordRegex = regexp.MustCompile(`(?i)(password=)[^\s]+`)

// maskDSN redacts credentials in connection strings (both URL format and key-value format).
// For URLs, it leverages Go's net/url Redacted() format (e.g., postgres://user:xxxxx@host:5432/db).
// For key-value pairs, it replaces the password with ********.
func maskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	// Try parsing as URL (e.g. postgres://user:secret@host:5432/db)
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		return u.Redacted()
	}
	// Key-value pair format (e.g. host=localhost user=postgres password=secret dbname=db)
	if strings.Contains(strings.ToLower(dsn), "password=") {
		return dsnPasswordRegex.ReplaceAllString(dsn, "${1}"+MaskedSecretPlaceholder)
	}
	return MaskedSecretPlaceholder
}

// Config is the top-level application configuration struct.
type Config struct {
	Server    ServerConfig    `mapstructure:"server" json:"server"`
	Database  DatabaseConfig  `mapstructure:"database" json:"database"`
	Redis     RedisConfig     `mapstructure:"redis" json:"redis"`
	JWT       JWTConfig       `mapstructure:"jwt" json:"jwt"`
	Log       LogConfig       `mapstructure:"log" json:"log"`
	Telemetry TelemetryConfig `mapstructure:"telemetry" json:"telemetry"`
}

// Masked returns a deep copy of Config with all sensitive fields redacted.
func (c Config) Masked() Config {
	clone := c
	if clone.Database.DSN != "" {
		clone.Database.DSN = maskDSN(clone.Database.DSN)
	}
	if clone.Redis.Password != "" {
		clone.Redis.Password = MaskedSecretPlaceholder
	}
	if clone.JWT.Secret != "" {
		clone.JWT.Secret = MaskedSecretPlaceholder
	}
	return clone
}

// String implements fmt.Stringer to ensure sensitive data is not leaked when printing.
func (c Config) String() string {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Sprintf("Config{Server:%+v}", c.Server)
	}
	return string(b)
}

// GoString implements fmt.GoStringer.
func (c Config) GoString() string {
	return c.String()
}

// LogValue implements slog.LogValuer for safe structured logging.
func (c Config) LogValue() slog.Value {
	return slog.StringValue(c.String())
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Addr            string        `mapstructure:"addr" json:"addr"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout" json:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout" json:"write_timeout"`
	IdleTimeout     time.Duration `mapstructure:"idle_timeout" json:"idle_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout" json:"shutdown_timeout"`
}

// DatabaseConfig holds PostgreSQL database configuration.
type DatabaseConfig struct {
	DSN             string        `mapstructure:"dsn" json:"dsn"`
	MaxOpenConns    int           `mapstructure:"max_open_conns" json:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns" json:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime" json:"conn_max_lifetime"`
	MigrationsPath  string        `mapstructure:"migrations_path" json:"migrations_path"`
}

// MarshalJSON redacts sensitive credentials in the DSN when serializing to JSON.
func (c DatabaseConfig) MarshalJSON() ([]byte, error) {
	type Alias DatabaseConfig
	masked := Alias(c)
	if masked.DSN != "" {
		masked.DSN = maskDSN(masked.DSN)
	}
	return json.Marshal(masked)
}

// String implements fmt.Stringer to redact DSN credentials when printing.
func (c DatabaseConfig) String() string {
	b, _ := json.Marshal(c)
	return string(b)
}

// GoString implements fmt.GoStringer.
func (c DatabaseConfig) GoString() string {
	return c.String()
}

// LogValue implements slog.LogValuer.
func (c DatabaseConfig) LogValue() slog.Value {
	return slog.StringValue(c.String())
}

// RedisConfig holds Redis connection configuration for standalone, cluster, or sentinel deployments.
type RedisConfig struct {
	Addr         string        `mapstructure:"addr" json:"addr"`
	Addrs        []string      `mapstructure:"addrs" json:"addrs"`
	Cluster      bool          `mapstructure:"cluster" json:"cluster"`
	MasterName   string        `mapstructure:"master_name" json:"master_name"`
	Password     string        `mapstructure:"password" json:"password"`
	DB           int           `mapstructure:"db" json:"db"`
	DialTimeout  time.Duration `mapstructure:"dial_timeout" json:"dial_timeout"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout" json:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout" json:"write_timeout"`
	PoolSize     int           `mapstructure:"pool_size" json:"pool_size"`
	PoolTimeout  time.Duration `mapstructure:"pool_timeout" json:"pool_timeout"`
}

// MarshalJSON redacts the Redis password when serializing to JSON.
func (c RedisConfig) MarshalJSON() ([]byte, error) {
	type Alias RedisConfig
	masked := Alias(c)
	if masked.Password != "" {
		masked.Password = MaskedSecretPlaceholder
	}
	return json.Marshal(masked)
}

// String implements fmt.Stringer to redact password when printing.
func (c RedisConfig) String() string {
	b, _ := json.Marshal(c)
	return string(b)
}

// GoString implements fmt.GoStringer.
func (c RedisConfig) GoString() string {
	return c.String()
}

// LogValue implements slog.LogValuer.
func (c RedisConfig) LogValue() slog.Value {
	return slog.StringValue(c.String())
}

// JWTConfig holds JSON Web Token configuration.
type JWTConfig struct {
	Secret string        `mapstructure:"secret" json:"secret"`
	Expiry time.Duration `mapstructure:"expiry" json:"expiry"`
	Issuer string        `mapstructure:"issuer" json:"issuer"`
}

// MarshalJSON redacts the JWT secret when serializing to JSON.
func (c JWTConfig) MarshalJSON() ([]byte, error) {
	type Alias JWTConfig
	masked := Alias(c)
	if masked.Secret != "" {
		masked.Secret = MaskedSecretPlaceholder
	}
	return json.Marshal(masked)
}

// String implements fmt.Stringer to redact secret when printing.
func (c JWTConfig) String() string {
	b, _ := json.Marshal(c)
	return string(b)
}

// GoString implements fmt.GoStringer.
func (c JWTConfig) GoString() string {
	return c.String()
}

// LogValue implements slog.LogValuer.
func (c JWTConfig) LogValue() slog.Value {
	return slog.StringValue(c.String())
}

// LogConfig holds logging configuration.
type LogConfig struct {
	Level       string `mapstructure:"level" json:"level"`
	Development bool   `mapstructure:"development" json:"development"`
}

// TelemetryConfig holds OpenTelemetry configuration.
type TelemetryConfig struct {
	Enabled      bool   `mapstructure:"enabled" json:"enabled"`
	OTLPEndpoint string `mapstructure:"otlp_endpoint" json:"otlp_endpoint"`
	ServiceName  string `mapstructure:"service_name" json:"service_name"`
}
