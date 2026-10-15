package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/logging"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("log line %q is not JSON: %v", b, err)
	}
	return m
}

func TestNewEmitsJSONWithStandardFields(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, logging.Options{Service: "api", Env: "test", Level: slog.LevelInfo})

	logger.Info("hello")

	rec := decode(t, buf.Bytes())
	for key, want := range map[string]any{"msg": "hello", "level": "INFO", "service": "api", "env": "test"} {
		if rec[key] != want {
			t.Errorf("%s = %v, want %v", key, rec[key], want)
		}
	}
	if _, ok := rec["time"]; !ok {
		t.Error("record has no time field")
	}
	if _, ok := rec["request_id"]; ok {
		t.Error("record without request context has request_id")
	}
}

func TestNewRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, logging.Options{Service: "api", Env: "test", Level: slog.LevelWarn})

	logger.Info("dropped")
	if buf.Len() != 0 {
		t.Fatalf("info record written at warn level: %s", buf.String())
	}
	logger.Warn("kept")
	if buf.Len() == 0 {
		t.Fatal("warn record not written at warn level")
	}
}

func TestRequestIDFromContextIsLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, logging.Options{Service: "api", Env: "test", Level: slog.LevelInfo}).
		With(slog.String("component", "x")).WithGroup("g")

	ctx := logging.WithRequestID(context.Background(), "req-123")
	logger.InfoContext(ctx, "handled")

	rec := decode(t, buf.Bytes())
	group, _ := rec["g"].(map[string]any)
	if group["request_id"] != "req-123" {
		t.Errorf("record = %v, want request_id req-123 inside group", rec)
	}
}

func TestRequestIDFromContext(t *testing.T) {
	if _, ok := logging.RequestIDFromContext(context.Background()); ok {
		t.Error("empty context reported a request ID")
	}
	id, ok := logging.RequestIDFromContext(logging.WithRequestID(context.Background(), "abc"))
	if !ok || id != "abc" {
		t.Errorf("RequestIDFromContext() = %q, %v; want abc, true", id, ok)
	}
}
