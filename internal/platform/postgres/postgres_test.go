package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/postgres"
)

func TestOpenRejectsMalformedDSN(t *testing.T) {
	if _, err := postgres.Open(context.Background(), "not-a-valid-dsn"); err == nil {
		t.Fatal("Open() error = nil, want a parse error")
	}
}

func TestOpenParseErrorDoesNotLeakPassword(t *testing.T) {
	_, err := postgres.Open(context.Background(), "postgres://user:supersecret@::::bad")
	if err == nil {
		t.Fatal("Open() error = nil, want a parse error")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Errorf("Open() error leaks the password: %v", err)
	}
}

type fakePinger struct {
	err    error
	called bool
}

func (f *fakePinger) Ping(context.Context) error {
	f.called = true
	return f.err
}

func TestCheckName(t *testing.T) {
	c := postgres.NewCheck("user_db", &fakePinger{}, 0)
	if c.Name() != "user_db" {
		t.Errorf("Name() = %q, want user_db", c.Name())
	}
}

func TestCheckPassesWhenPingSucceeds(t *testing.T) {
	p := &fakePinger{}
	c := postgres.NewCheck("user_db", p, time.Second)
	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}
	if !p.called {
		t.Error("Check() did not ping the pool")
	}
}

func TestCheckFailsWhenPingFails(t *testing.T) {
	p := &fakePinger{err: errors.New("boom")}
	c := postgres.NewCheck("user_db", p, time.Second)
	err := c.Check(context.Background())
	if err == nil {
		t.Fatal("Check() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "user_db") {
		t.Errorf("Check() error = %q, want it to name the dependency", err)
	}
}

func TestCheckFailsAgainstUnreachableDatabase(t *testing.T) {
	// A lazy pool pointed at a closed local port: Open succeeds (no eager
	// connection) but the readiness ping must fail promptly.
	pool, err := postgres.Open(context.Background(),
		"postgres://user:pass@127.0.0.1:1/user_db?connect_timeout=1&sslmode=disable")
	if err != nil {
		t.Fatalf("Open() error = %v, want a lazy pool", err)
	}
	defer pool.Close()

	c := postgres.NewCheck("user_db", pool, time.Second)
	if err := c.Check(context.Background()); err == nil {
		t.Error("Check() error = nil, want failure against an unreachable database")
	}
}
