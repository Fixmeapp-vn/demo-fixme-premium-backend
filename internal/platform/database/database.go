// Package database provides generic PostgreSQL helpers shared by all binaries:
// connection pooling and readiness checks. It is platform-level and must never
// import business modules such as internal/user.
package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool builds a pgx connection pool from a DSN. Connections are established
// lazily, so an unreachable database does not fail pool construction; that is
// surfaced later by readiness checks rather than at startup.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("database: invalid connection string")
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	return pool, nil
}

// Pinger is the subset of *pgxpool.Pool used by readiness checks.
type Pinger interface {
	Ping(ctx context.Context) error
}

// PingChecker reports a dependency ready when a ping succeeds.
type PingChecker struct {
	name string
	p    Pinger
}

// NewPingChecker returns a readiness check that pings p under the given name.
func NewPingChecker(name string, p Pinger) *PingChecker {
	return &PingChecker{name: name, p: p}
}

// Name returns the dependency name.
func (c *PingChecker) Name() string { return c.name }

// Check pings the dependency, reporting any error.
func (c *PingChecker) Check(ctx context.Context) error {
	if err := c.p.Ping(ctx); err != nil {
		return fmt.Errorf("ping %s: %w", c.name, err)
	}
	return nil
}

// UnavailableChecker is a readiness check that always reports not ready. It is
// used to stand in for a dependency that has not been configured, so readiness
// reports not-ready instead of silently passing.
type UnavailableChecker struct {
	name   string
	reason string
}

// NewUnavailableChecker returns a check that always fails with reason.
func NewUnavailableChecker(name, reason string) *UnavailableChecker {
	return &UnavailableChecker{name: name, reason: reason}
}

// Name returns the dependency name.
func (c *UnavailableChecker) Name() string { return c.name }

// Check always returns an error carrying the configured reason.
func (c *UnavailableChecker) Check(context.Context) error {
	return errors.New(c.reason)
}
