package config_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/config"
)

func lookupFrom(m map[string]string) config.LookupFunc {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(lookupFrom(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := config.Config{
		AppEnv: config.EnvDevelopment,
		HTTP: config.HTTP{
			Addr:              ":8080",
			ReadTimeout:       15 * time.Second,
			ReadHeaderTimeout: 5 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		ShutdownTimeout: 20 * time.Second,
		MetricsAddr:     ":9090",
		LogLevel:        slog.LevelInfo,
	}
	if cfg != want {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadValidValues(t *testing.T) {
	cfg, err := config.Load(lookupFrom(map[string]string{
		"APP_ENV":                  "production",
		"HTTP_ADDR":                "127.0.0.1:8000",
		"HTTP_READ_TIMEOUT":        "10s",
		"HTTP_READ_HEADER_TIMEOUT": " 2s ",
		"HTTP_WRITE_TIMEOUT":       "1m",
		"HTTP_IDLE_TIMEOUT":        "90s",
		"SHUTDOWN_TIMEOUT":         "500ms",
		"METRICS_ADDR":             "127.0.0.1:9100",
		"LOG_LEVEL":                "DEBUG",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := config.Config{
		AppEnv: config.EnvProduction,
		HTTP: config.HTTP{
			Addr:              "127.0.0.1:8000",
			ReadTimeout:       10 * time.Second,
			ReadHeaderTimeout: 2 * time.Second,
			WriteTimeout:      time.Minute,
			IdleTimeout:       90 * time.Second,
		},
		ShutdownTimeout: 500 * time.Millisecond,
		MetricsAddr:     "127.0.0.1:9100",
		LogLevel:        slog.LevelDebug,
	}
	if cfg != want {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadAcceptsEveryAppEnv(t *testing.T) {
	for _, env := range []string{"development", "test", "staging", "production"} {
		t.Run(env, func(t *testing.T) {
			cfg, err := config.Load(lookupFrom(map[string]string{"APP_ENV": env}))
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if string(cfg.AppEnv) != env {
				t.Errorf("AppEnv = %q, want %q", cfg.AppEnv, env)
			}
		})
	}
}

func TestLoadInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"unknown app env", map[string]string{"APP_ENV": "prod"}, "APP_ENV: invalid value \"prod\""},
		{"empty app env", map[string]string{"APP_ENV": ""}, "APP_ENV: invalid value \"\""},
		{"missing http addr", map[string]string{"HTTP_ADDR": ""}, "HTTP_ADDR: is required"},
		{"malformed http addr", map[string]string{"HTTP_ADDR": "8080"}, "HTTP_ADDR: invalid value \"8080\""},
		{"missing metrics addr", map[string]string{"METRICS_ADDR": "  "}, "METRICS_ADDR: is required"},
		{"same addresses", map[string]string{"HTTP_ADDR": ":7000", "METRICS_ADDR": ":7000"}, "METRICS_ADDR: must differ from HTTP_ADDR"},
		{"unparseable duration", map[string]string{"HTTP_READ_TIMEOUT": "ten"}, "HTTP_READ_TIMEOUT: invalid duration \"ten\""},
		{"duration without unit", map[string]string{"HTTP_WRITE_TIMEOUT": "30"}, "HTTP_WRITE_TIMEOUT: invalid duration"},
		{"duration too short", map[string]string{"HTTP_IDLE_TIMEOUT": "1ms"}, "HTTP_IDLE_TIMEOUT: 1ms is out of range"},
		{"duration too long", map[string]string{"SHUTDOWN_TIMEOUT": "1h"}, "SHUTDOWN_TIMEOUT: 1h0m0s is out of range"},
		{"header timeout above read timeout", map[string]string{"HTTP_READ_TIMEOUT": "1s", "HTTP_READ_HEADER_TIMEOUT": "2s"}, "HTTP_READ_HEADER_TIMEOUT: must not exceed HTTP_READ_TIMEOUT"},
		{"unknown log level", map[string]string{"LOG_LEVEL": "verbose"}, "LOG_LEVEL: invalid value \"verbose\""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(lookupFrom(tt.env))
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadReportsAllInvalidFieldsTogether(t *testing.T) {
	_, err := config.Load(lookupFrom(map[string]string{
		"APP_ENV":          "qa",
		"HTTP_ADDR":        "",
		"SHUTDOWN_TIMEOUT": "forever",
		"LOG_LEVEL":        "loud",
	}))
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	for _, key := range []string{"APP_ENV", "HTTP_ADDR", "SHUTDOWN_TIMEOUT", "LOG_LEVEL"} {
		if !strings.Contains(err.Error(), key+":") {
			t.Errorf("error %q does not mention %s", err, key)
		}
	}
}

func TestConfigLogValue(t *testing.T) {
	cfg, err := config.Load(lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config", slog.Any("config", cfg))

	for _, want := range []string{`"app_env":"development"`, `"http_addr":":8080"`, `"metrics_addr":":9090"`, `"log_level":"INFO"`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log output %s does not contain %s", buf.String(), want)
		}
	}
}

func TestLoadUserDatabaseURLUnset(t *testing.T) {
	cfg, err := config.Load(lookupFrom(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.UserDatabaseURL != "" {
		t.Errorf("UserDatabaseURL = %q, want empty", cfg.UserDatabaseURL)
	}
}

func TestLoadUserDatabaseURLValid(t *testing.T) {
	for _, dsn := range []string{
		"postgres://user_runtime:secret@localhost:5432/user_db?sslmode=disable",
		"postgresql://user_runtime:secret@localhost:5432/user_db",
	} {
		cfg, err := config.Load(lookupFrom(map[string]string{"USER_DATABASE_URL": dsn}))
		if err != nil {
			t.Fatalf("Load(%q) error = %v", dsn, err)
		}
		if cfg.UserDatabaseURL != dsn {
			t.Errorf("UserDatabaseURL = %q, want %q", cfg.UserDatabaseURL, dsn)
		}
	}
}

func TestLoadUserDatabaseURLInvalidScheme(t *testing.T) {
	_, err := config.Load(lookupFrom(map[string]string{"USER_DATABASE_URL": "mysql://x"}))
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "USER_DATABASE_URL") {
		t.Errorf("error %q does not mention USER_DATABASE_URL", err)
	}
}

func TestConfigLogValueExcludesDatabaseURL(t *testing.T) {
	const dsn = "postgres://user_runtime:topsecret@localhost:5432/user_db"
	cfg, err := config.Load(lookupFrom(map[string]string{"USER_DATABASE_URL": dsn}))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config", slog.Any("config", cfg))

	for _, leaked := range []string{dsn, "topsecret", "user_database_url", "UserDatabaseURL"} {
		if strings.Contains(buf.String(), leaked) {
			t.Errorf("log output leaks %q:\n%s", leaked, buf.String())
		}
	}
}
