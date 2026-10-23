package database_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/database"
)

type fakePinger struct {
	err error
}

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestPingCheckerHealthy(t *testing.T) {
	c := database.NewPingChecker("user_db", fakePinger{err: nil})
	if c.Name() != "user_db" {
		t.Errorf("Name() = %q, want user_db", c.Name())
	}
	if err := c.Check(context.Background()); err != nil {
		t.Errorf("Check() = %v, want nil", err)
	}
}

func TestPingCheckerPropagatesError(t *testing.T) {
	boom := errors.New("connection refused")
	c := database.NewPingChecker("user_db", fakePinger{err: boom})
	err := c.Check(context.Background())
	if err == nil || !errors.Is(err, boom) {
		t.Errorf("Check() = %v, want it to wrap %v", err, boom)
	}
}

func TestUnavailableChecker(t *testing.T) {
	c := database.NewUnavailableChecker("user_db", "USER_DATABASE_URL not configured")
	if c.Name() != "user_db" {
		t.Errorf("Name() = %q, want user_db", c.Name())
	}
	err := c.Check(context.Background())
	if err == nil {
		t.Fatal("Check() = nil, want error")
	}
	if err.Error() != "USER_DATABASE_URL not configured" {
		t.Errorf("Check() = %q, want the configured reason", err)
	}
}
