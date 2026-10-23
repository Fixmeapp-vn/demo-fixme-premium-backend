// Package migrations holds the forward-only SQL migrations for the User
// module database (user_db) and the helpers that apply them.
//
// Migrations are applied only by cmd/migrate, running as the user_migrate
// role. The api and worker binaries never apply migrations; they connect as
// user_runtime, which has no DDL rights. Only *.up.sql files exist: the schema
// moves forward only, and reversing a migration is a deliberate future
// decision rather than an automatic down step.
package migrations

import (
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// FS holds the embedded migration SQL, so no files need shipping alongside
// the migrate binary.
//
//go:embed *.sql
var FS embed.FS

// New builds a forward-only migrator for dsn using the embedded SQL. The
// caller owns the returned *migrate.Migrate and must Close it. The DSN carries
// a password and is never logged.
func New(dsn string) (*migrate.Migrate, error) {
	src, err := iofs.New(FS, ".")
	if err != nil {
		return nil, fmt.Errorf("migrations: open embedded source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, pgxDSN(dsn))
	if err != nil {
		return nil, fmt.Errorf("migrations: init migrator: %w", err)
	}
	return m, nil
}

// Up applies all pending migrations to dsn, forward-only. It returns nil when
// the database is already at the latest version.
func Up(dsn string) error {
	m, err := New(dsn)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrations: apply: %w", err)
	}
	return nil
}

// Version returns the current migration version of dsn and whether the schema
// is in a dirty (failed-midway) state. A database with no migrations applied
// yet returns migrate.ErrNilVersion.
func Version(dsn string) (version uint, dirty bool, err error) {
	m, err := New(dsn)
	if err != nil {
		return 0, false, err
	}
	defer m.Close()
	v, d, err := m.Version()
	if err != nil {
		return 0, false, fmt.Errorf("migrations: version: %w", err)
	}
	return v, d, nil
}

// pgxDSN rewrites a postgres:// or postgresql:// URL to the pgx5:// scheme the
// embedded pgx/v5 migrate driver registers under. Any other scheme is passed
// through unchanged.
func pgxDSN(dsn string) string {
	for _, scheme := range []string{"postgresql://", "postgres://"} {
		if strings.HasPrefix(dsn, scheme) {
			return "pgx5://" + strings.TrimPrefix(dsn, scheme)
		}
	}
	return dsn
}
