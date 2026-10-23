// Package dbtest holds integration tests that exercise the real User module
// database against a live Postgres instance.
//
// The entire suite is gated at runtime on USER_IT_SUPERUSER_URL, a superuser
// DSN used to provision a throwaway database, roles and the least-privilege
// model mirrored from deployments/postgres. When the variable is unset the
// tests skip (they are NOT behind a build tag, so they always compile under
// `go test ./...`).
package dbtest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/postgres"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/user/migrations"
)

const superURLEnv = "USER_IT_SUPERUSER_URL"

const rolePassword = "integration-test-pw"

// env holds the throwaway database and the DSNs for each role.
type env struct {
	superURL    string
	dbName      string
	migrateRole string
	runtimeRole string
	noConnRole  string
}

func (e env) dsn(t *testing.T, user, dbname string) string {
	t.Helper()
	u, err := url.Parse(e.superURL)
	if err != nil {
		t.Fatalf("parse %s: %v", superURLEnv, err)
	}
	u.User = url.UserPassword(user, rolePassword)
	u.Path = "/" + dbname
	return u.String()
}

func (e env) migrateDSN(t *testing.T) string { return e.dsn(t, e.migrateRole, e.dbName) }
func (e env) runtimeDSN(t *testing.T) string { return e.dsn(t, e.runtimeRole, e.dbName) }
func (e env) noConnDSN(t *testing.T) string  { return e.dsn(t, e.noConnRole, e.dbName) }

// superDBName returns the database name from the superuser URL so admin
// statements (CREATE DATABASE / DROP DATABASE) connect somewhere other than
// the throwaway database.
func (e env) superAdminURL() string { return e.superURL }

func exec(ctx context.Context, t *testing.T, dsn string, stmts ...string) {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect %q: %v", dsn, err)
	}
	defer pool.Close()
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
}

// setup provisions a fresh database, roles and the privilege model, then
// registers cleanup. It returns the configured env.
func setup(t *testing.T) env {
	t.Helper()
	superURL := os.Getenv(superURLEnv)
	if superURL == "" {
		t.Skipf("%s not set; skipping database integration tests", superURLEnv)
	}

	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	e := env{
		superURL:    superURL,
		dbName:      "it_user_db_" + suffix,
		migrateRole: "it_migrate_" + suffix,
		runtimeRole: "it_runtime_" + suffix,
		noConnRole:  "it_noconn_" + suffix,
	}

	// Roles and database are created through the superuser connection. Each
	// statement runs on its own: CREATE DATABASE cannot share a transaction.
	admin, err := pgxpool.New(ctx, e.superAdminURL())
	if err != nil {
		t.Fatalf("connect superuser: %v", err)
	}
	for _, role := range []string{e.migrateRole, e.runtimeRole, e.noConnRole} {
		if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s'", role, rolePassword)); err != nil {
			admin.Close()
			t.Fatalf("create role %s: %v", role, err)
		}
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s", e.dbName, e.migrateRole)); err != nil {
		admin.Close()
		t.Fatalf("create database: %v", err)
	}
	admin.Close()

	t.Cleanup(func() {
		cctx := context.Background()
		a, err := pgxpool.New(cctx, e.superAdminURL())
		if err != nil {
			t.Logf("cleanup connect: %v", err)
			return
		}
		defer a.Close()
		// Force-disconnect any leftover sessions before dropping.
		_, _ = a.Exec(cctx, fmt.Sprintf(
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s'", e.dbName))
		for _, s := range []string{
			fmt.Sprintf("DROP DATABASE IF EXISTS %s", e.dbName),
			fmt.Sprintf("DROP ROLE IF EXISTS %s", e.runtimeRole),
			fmt.Sprintf("DROP ROLE IF EXISTS %s", e.migrateRole),
			fmt.Sprintf("DROP ROLE IF EXISTS %s", e.noConnRole),
		} {
			if _, err := a.Exec(cctx, s); err != nil {
				t.Logf("cleanup %q: %v", s, err)
			}
		}
	})

	// Apply the privilege model by executing the SAME canonical SQL file the
	// bootstrap script runs (deployments/postgres/initdb/sql), with the
	// throwaway role, schema and database names substituted in. Driving the
	// source of truth means a broken policy file fails these tests instead of
	// leaving them green against a stale copy.
	adminDB := e.adminDB(t, e.dbName)
	exec(ctx, t, adminDB, e.privilegesSQL(t))
	return e
}

// privilegesSQL loads the canonical privilege model and substitutes this
// environment's throwaway identifiers. The path is resolved relative to this
// test file via runtime.Caller, so the test executes the real file rather than
// a copy. The whole file is returned as one string: exec passes it to pgx with
// no arguments, which runs the statements under the simple query protocol.
func (e env) privilegesSQL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to locate the integration test file")
	}
	path := filepath.Join(filepath.Dir(thisFile),
		"..", "..", "..", "deployments", "postgres", "initdb", "sql", "user-db-privileges.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read canonical privilege SQL %s: %v", path, err)
	}
	return strings.NewReplacer(
		"__db_name__", e.dbName,
		"__migrate_role__", e.migrateRole,
		"__runtime_role__", e.runtimeRole,
		"__migrate_schema__", migrations.MigrationsSchema,
	).Replace(string(raw))
}

// adminDB builds a superuser DSN against dbname, preserving the superuser's
// own password from the source URL (dsn() would overwrite it).
func (e env) adminDB(t *testing.T, dbname string) string {
	t.Helper()
	u, err := url.Parse(e.superURL)
	if err != nil {
		t.Fatalf("parse %s: %v", superURLEnv, err)
	}
	u.Path = "/" + dbname
	return u.String()
}

func TestForwardOnlyBringUpReachesHead(t *testing.T) {
	e := setup(t)
	migrateDSN := e.migrateDSN(t)

	if err := migrations.Up(migrateDSN); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	// Idempotent: a second Up is a no-op, not an error.
	if err := migrations.Up(migrateDSN); err != nil {
		t.Fatalf("second Up() error = %v", err)
	}

	version, dirty, err := migrations.Version(migrateDSN)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != 2 || dirty {
		t.Errorf("version = %d dirty = %t, want 2 false", version, dirty)
	}
}

func TestRuntimeRoleHasDMLButNotDDL(t *testing.T) {
	e := setup(t)
	if err := migrations.Up(e.migrateDSN(t)); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, e.runtimeDSN(t))
	if err != nil {
		t.Fatalf("connect runtime: %v", err)
	}
	defer pool.Close()

	// DML: an identity-column insert and a read back.
	var id int64
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email) VALUES ($1) RETURNING id", "a@example.com").Scan(&id); err != nil {
		t.Fatalf("runtime INSERT into users: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil {
		t.Fatalf("runtime SELECT from users: %v", err)
	}
	if count != 1 {
		t.Errorf("users count = %d, want 1", count)
	}

	// The remaining DML verbs must also be allowed, not only INSERT/SELECT.
	if _, err := pool.Exec(ctx, "UPDATE users SET email = $1 WHERE id = $2", "b@example.com", id); err != nil {
		t.Fatalf("runtime UPDATE users: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", id); err != nil {
		t.Fatalf("runtime DELETE users: %v", err)
	}

	// DDL must be denied.
	if _, err := pool.Exec(ctx, "CREATE TABLE should_fail (id int)"); err == nil {
		t.Error("runtime CREATE TABLE succeeded; DDL must be denied")
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE users ADD COLUMN nope int"); err == nil {
		t.Error("runtime ALTER TABLE succeeded; DDL must be denied")
	}
}

func TestDefaultPrivilegesCoverSecondMigration(t *testing.T) {
	e := setup(t)
	if err := migrations.Up(e.migrateDSN(t)); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, e.runtimeDSN(t))
	if err != nil {
		t.Fatalf("connect runtime: %v", err)
	}
	defer pool.Close()

	var userID int64
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email) VALUES ($1) RETURNING id", "owner@example.com").Scan(&userID); err != nil {
		t.Fatalf("seed users row: %v", err)
	}

	// user_profiles is from the SECOND migration and uses a bigserial
	// sequence. No explicit grant exists for it; the INSERT (and its nextval)
	// must succeed purely from ALTER DEFAULT PRIVILEGES.
	var profileID int64
	if err := pool.QueryRow(ctx,
		"INSERT INTO user_profiles (user_id, display_name) VALUES ($1, $2) RETURNING id",
		userID, "Example").Scan(&profileID); err != nil {
		t.Fatalf("runtime INSERT into user_profiles via default privileges: %v", err)
	}
	if profileID <= 0 {
		t.Errorf("profile id = %d, want a sequence-generated positive value", profileID)
	}
}

// TestRuntimeRoleCannotTouchMigrateVersionTable proves the bookkeeping-table
// isolation: the migrate version table lives in migrations.MigrationsSchema,
// which user_runtime has no USAGE on, so it can neither read nor UPDATE/DELETE
// the version/dirty flags and desync migration state. DML on a business table
// in public must still work.
func TestRuntimeRoleCannotTouchMigrateVersionTable(t *testing.T) {
	e := setup(t)
	if err := migrations.Up(e.migrateDSN(t)); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, e.runtimeDSN(t))
	if err != nil {
		t.Fatalf("connect runtime: %v", err)
	}
	defer pool.Close()

	versionTable := fmt.Sprintf("%s.%s", migrations.MigrationsSchema, migrations.MigrationsTable)
	for _, q := range []string{
		"SELECT version, dirty FROM " + versionTable,
		"UPDATE " + versionTable + " SET dirty = true",
		"UPDATE " + versionTable + " SET version = 0",
		"DELETE FROM " + versionTable,
		"INSERT INTO " + versionTable + " (version, dirty) VALUES (999, false)",
	} {
		_, err := pool.Exec(ctx, q)
		if err == nil {
			t.Errorf("runtime %q succeeded; it must be denied on the migrate bookkeeping table", q)
			continue
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("runtime %q error = %v, want SQLSTATE 42501 (permission denied)", q, err)
		}
	}

	// Sanity: DML on a business table in public is unaffected.
	if _, err := pool.Exec(ctx, "INSERT INTO users (email) VALUES ($1)", "ok@example.com"); err != nil {
		t.Fatalf("runtime INSERT into business table users: %v", err)
	}
}

func TestRoleWithoutConnectIsRefused(t *testing.T) {
	e := setup(t)
	if err := migrations.Up(e.migrateDSN(t)); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, e.noConnDSN(t))
	if err != nil {
		// The pool is lazy, so construction must succeed; CONNECT is denied
		// only on first use. A construction error would hide that.
		t.Fatalf("pgxpool.New for no-connect role: %v", err)
	}
	defer pool.Close()

	// The refusal must be a real PostgreSQL permission denial on use, not a
	// parse failure or a bad-DSN error that merely looks like a pass.
	err = pool.Ping(ctx)
	if err == nil {
		t.Fatal("role without CONNECT reached the database; it must be refused")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("no-connect refusal = %v (%T), want a *pgconn.PgError permission denial", err, err)
	}
	if pgErr.Code != "42501" {
		t.Errorf("no-connect SQLSTATE = %s (%s), want 42501 (permission denied)", pgErr.Code, pgErr.Message)
	}
}

func TestReadinessFlipsToOKWhenRuntimeConnectable(t *testing.T) {
	e := setup(t)
	if err := migrations.Up(e.migrateDSN(t)); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	ctx := context.Background()

	// Unavailable: a pool pointed at a closed port fails its check.
	badPool, err := postgres.Open(ctx, "postgres://x:y@127.0.0.1:1/user_db?connect_timeout=1&sslmode=disable")
	if err != nil {
		t.Fatalf("open bad pool: %v", err)
	}
	defer badPool.Close()
	if err := postgres.NewCheck("user_db", badPool, time.Second).Check(ctx); err == nil {
		t.Error("check against unreachable database passed, want unavailable")
	}

	// OK: the real runtime pool becomes connectable.
	pool, err := postgres.Open(ctx, e.runtimeDSN(t))
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer pool.Close()
	if err := postgres.NewCheck("user_db", pool, 5*time.Second).Check(ctx); err != nil {
		t.Errorf("readiness check against runtime pool failed: %v", err)
	}
}
