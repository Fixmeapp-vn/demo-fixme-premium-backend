// Package postgres provides a lazily-connected pgx connection pool and a
// readiness check for the platform. The DSN carries a password and is never
// logged; parse errors from pgx redact the password before this package wraps
// them.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultPingTimeout bounds a single readiness ping so a slow or unreachable
// database cannot stall the probe.
const DefaultPingTimeout = 2 * time.Second

// Open parses dsn and returns a connection pool. The pool is lazy: no
// connection is established until the first query, so a service can start
// while Postgres is still down and become ready once it is reachable. The
// caller owns the returned pool and must Close it on shutdown.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse connection config: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	return pool, nil
}

// Pinger is the subset of *pgxpool.Pool the readiness check needs. It keeps
// the check testable with a fake pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Check reports the database reachable by pinging a pool. It satisfies the
// httpx.HealthCheck interface without importing httpx, so readiness stays
// decoupled from the router.
type Check struct {
	name    string
	pool    Pinger
	timeout time.Duration
}

// NewCheck returns a readiness check for pool, reported under name (e.g.
// "user_db"). A non-positive timeout selects DefaultPingTimeout.
func NewCheck(name string, pool Pinger, timeout time.Duration) *Check {
	if timeout <= 0 {
		timeout = DefaultPingTimeout
	}
	return &Check{name: name, pool: pool, timeout: timeout}
}

// Name identifies the dependency in readiness responses.
func (c *Check) Name() string { return c.name }

// Check pings the pool under its own timeout, bounded further by ctx. It
// returns an error only when the database cannot be reached.
func (c *Check) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := c.pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: ping %s: %w", c.name, err)
	}
	return nil
}
