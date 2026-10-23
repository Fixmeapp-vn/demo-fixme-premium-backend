// Package db embeds the User module's SQL migrations and runs them
// forward-only, so that both cmd/migrate and the integration tests apply
// schema changes through exactly the same code path.
package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxv5 "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	// stdlib registers the "pgx" database/sql driver used to open the
	// connection migrate runs its statements over.
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// newMigrator builds a forward-only migrator bound to dbURL. The returned
// *migrate.Migrate owns both the embedded source and the opened sql.DB; call
// Close on it (which closes both) rather than closing the sql.DB separately.
func newMigrator(dbURL string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("load embedded migrations: %w", err)
	}

	sqldb, err := sql.Open("pgx", dbURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	driver, err := pgxv5.WithInstance(sqldb, &pgxv5.Config{})
	if err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("build migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("build migrator: %w", err)
	}
	return m, nil
}

// Up applies all pending migrations. It is idempotent: when the schema is
// already current, migrate.ErrNoChange is treated as success.
func Up(dbURL string) error {
	m, err := newMigrator(dbURL)
	if err != nil {
		return err
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// Version reports the currently applied migration version. When no migration
// has been applied yet, it returns version 0 without an error.
func Version(dbURL string) (version uint, dirty bool, err error) {
	m, err := newMigrator(dbURL)
	if err != nil {
		return 0, false, err
	}
	defer m.Close()

	version, dirty, err = m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read migration version: %w", err)
	}
	return version, dirty, nil
}
