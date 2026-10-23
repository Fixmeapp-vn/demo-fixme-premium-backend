package main

import (
	"bytes"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestRunMissingMigrateURL(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"up"}, env(nil), &bytes.Buffer{}, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "USER_MIGRATE_DATABASE_URL is not set") {
		t.Errorf("stderr = %q, want it to report the missing variable", stderr.String())
	}
}

func TestRunEmptyMigrateURL(t *testing.T) {
	var stderr bytes.Buffer
	code := run(nil, env(map[string]string{"USER_MIGRATE_DATABASE_URL": ""}), &bytes.Buffer{}, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "USER_MIGRATE_DATABASE_URL is not set") {
		t.Errorf("stderr = %q, want it to report the missing variable", stderr.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"sideways"}, env(map[string]string{
		"USER_MIGRATE_DATABASE_URL": "postgres://user_migrate:pw@localhost:5432/user_db",
	}), &bytes.Buffer{}, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("stderr = %q, want it to report an unknown command", stderr.String())
	}
}
