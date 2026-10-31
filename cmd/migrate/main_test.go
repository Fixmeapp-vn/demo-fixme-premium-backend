package main

import (
	"bytes"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestRunMissingEnvVarFails(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"up"}, env(nil), &bytes.Buffer{}, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "USER_MIGRATE_DATABASE_URL") {
		t.Errorf("stderr = %q, want it to name the missing variable", stderr.String())
	}
}

func TestRunBlankEnvVarFails(t *testing.T) {
	var stderr bytes.Buffer
	code := run(nil, env(map[string]string{"USER_MIGRATE_DATABASE_URL": "   "}), &bytes.Buffer{}, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "USER_MIGRATE_DATABASE_URL") {
		t.Errorf("stderr = %q, want it to name the missing variable", stderr.String())
	}
}

func TestRunUnknownCommandFails(t *testing.T) {
	var stderr bytes.Buffer
	// An unknown command is rejected before any environment lookup, so no
	// database URL is needed.
	code := run([]string{"down"}, env(map[string]string{"USER_MIGRATE_DATABASE_URL": "postgres://x"}), &bytes.Buffer{}, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") || !strings.Contains(stderr.String(), "down") {
		t.Errorf("stderr = %q, want unknown-command error naming 'down'", stderr.String())
	}
}
