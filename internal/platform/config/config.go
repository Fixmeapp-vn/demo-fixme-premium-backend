// Package config loads and validates application configuration from
// environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Env is the deployment environment the application runs in.
type Env string

const (
	EnvDevelopment Env = "development"
	EnvTest        Env = "test"
	EnvStaging     Env = "staging"
	EnvProduction  Env = "production"
)

var validEnvs = []Env{EnvDevelopment, EnvTest, EnvStaging, EnvProduction}

// Timeout bounds applied to every duration setting.
const (
	MinTimeout = 100 * time.Millisecond
	MaxTimeout = 10 * time.Minute
)

// Defaults for every supported variable.
const (
	DefaultAppEnv                = EnvDevelopment
	DefaultHTTPAddr              = ":8080"
	DefaultHTTPReadTimeout       = 15 * time.Second
	DefaultHTTPReadHeaderTimeout = 5 * time.Second
	DefaultHTTPWriteTimeout      = 30 * time.Second
	DefaultHTTPIdleTimeout       = 60 * time.Second
	DefaultShutdownTimeout       = 20 * time.Second
	DefaultMetricsAddr           = ":9090"
	DefaultLogLevel              = slog.LevelInfo
)

// Config is the typed application configuration shared by all binaries.
type Config struct {
	AppEnv          Env
	HTTP            HTTP
	ShutdownTimeout time.Duration
	MetricsAddr     string
	LogLevel        slog.Level
	// UserDatabaseURL is the runtime DSN for the User module (the user_runtime
	// role, DML only), read from USER_DATABASE_URL. It is optional; when empty
	// the API still starts but readiness reports not-ready. It is a secret and
	// is deliberately excluded from LogValue.
	UserDatabaseURL string
}

// HTTP holds the public HTTP server settings.
type HTTP struct {
	Addr              string
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

// LookupFunc matches the signature of os.LookupEnv.
type LookupFunc func(key string) (string, bool)

// FromEnv loads configuration from the process environment.
func FromEnv() (Config, error) {
	return Load(os.LookupEnv)
}

// Load builds a Config using lookup, applying defaults for unset variables.
// All invalid fields are reported together in the returned error.
func Load(lookup LookupFunc) (Config, error) {
	l := loader{lookup: lookup}

	cfg := Config{
		AppEnv: l.env("APP_ENV", DefaultAppEnv),
		HTTP: HTTP{
			Addr:              l.addr("HTTP_ADDR", DefaultHTTPAddr),
			ReadTimeout:       l.duration("HTTP_READ_TIMEOUT", DefaultHTTPReadTimeout),
			ReadHeaderTimeout: l.duration("HTTP_READ_HEADER_TIMEOUT", DefaultHTTPReadHeaderTimeout),
			WriteTimeout:      l.duration("HTTP_WRITE_TIMEOUT", DefaultHTTPWriteTimeout),
			IdleTimeout:       l.duration("HTTP_IDLE_TIMEOUT", DefaultHTTPIdleTimeout),
		},
		ShutdownTimeout: l.duration("SHUTDOWN_TIMEOUT", DefaultShutdownTimeout),
		MetricsAddr:     l.addr("METRICS_ADDR", DefaultMetricsAddr),
		LogLevel:        l.logLevel("LOG_LEVEL", DefaultLogLevel),
		UserDatabaseURL: l.databaseURL("USER_DATABASE_URL"),
	}

	if cfg.HTTP.ReadHeaderTimeout > cfg.HTTP.ReadTimeout && cfg.HTTP.ReadTimeout > 0 {
		l.fail("HTTP_READ_HEADER_TIMEOUT", "must not exceed HTTP_READ_TIMEOUT (%s)", cfg.HTTP.ReadTimeout)
	}
	if cfg.HTTP.Addr != "" && cfg.HTTP.Addr == cfg.MetricsAddr {
		l.fail("METRICS_ADDR", "must differ from HTTP_ADDR so metrics are not served on the public listener")
	}

	if len(l.errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration:\n%w", errors.Join(l.errs...))
	}
	return cfg, nil
}

// LogValue implements slog.LogValuer. Only non-sensitive fields are listed,
// so secrets added to Config later are not logged unless added here.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("app_env", string(c.AppEnv)),
		slog.String("http_addr", c.HTTP.Addr),
		slog.String("http_read_timeout", c.HTTP.ReadTimeout.String()),
		slog.String("http_read_header_timeout", c.HTTP.ReadHeaderTimeout.String()),
		slog.String("http_write_timeout", c.HTTP.WriteTimeout.String()),
		slog.String("http_idle_timeout", c.HTTP.IdleTimeout.String()),
		slog.String("shutdown_timeout", c.ShutdownTimeout.String()),
		slog.String("metrics_addr", c.MetricsAddr),
		slog.String("log_level", c.LogLevel.String()),
	)
}

type loader struct {
	lookup LookupFunc
	errs   []error
}

func (l *loader) fail(key, format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf("%s: "+format, append([]any{key}, args...)...))
}

func (l *loader) raw(key string) (string, bool) {
	v, ok := l.lookup(key)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(v), true
}

func (l *loader) env(key string, def Env) Env {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	for _, e := range validEnvs {
		if Env(v) == e {
			return e
		}
	}
	l.fail(key, "invalid value %q: must be one of development, test, staging, production", v)
	return ""
}

func (l *loader) addr(key, def string) string {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	if v == "" {
		l.fail(key, "is required and must not be empty (example: %q)", def)
		return ""
	}
	if !strings.Contains(v, ":") {
		l.fail(key, "invalid value %q: must be in host:port form (example: %q)", v, def)
		return ""
	}
	return v
}

func (l *loader) duration(key string, def time.Duration) time.Duration {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.fail(key, "invalid duration %q: use Go duration syntax such as \"15s\" or \"1m\"", v)
		return 0
	}
	if d < MinTimeout || d > MaxTimeout {
		l.fail(key, "%s is out of range: must be between %s and %s", d, MinTimeout, MaxTimeout)
		return 0
	}
	return d
}

// databaseURL reads an optional Postgres DSN. It returns "" when unset or
// empty, and otherwise lightly validates the scheme. The value is never logged.
func (l *loader) databaseURL(key string) string {
	v, ok := l.raw(key)
	if !ok || v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "postgres://") && !strings.HasPrefix(v, "postgresql://") {
		l.fail(key, "invalid value: must be a postgres:// or postgresql:// URL")
		return ""
	}
	return v
}

func (l *loader) logLevel(key string, def slog.Level) slog.Level {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(v)); err != nil {
		l.fail(key, "invalid value %q: must be one of debug, info, warn, error", v)
		return def
	}
	return level
}
