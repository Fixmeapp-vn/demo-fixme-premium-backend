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
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

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

	// Apply the same least-privilege model as deployments/postgres, using the
	// throwaway role names, connected to the new database as superuser.
	adminDB := e.adminDB(t, e.dbName)
	exec(ctx, t, adminDB,
		fmt.Sprintf("REVOKE ALL ON DATABASE %s FROM PUBLIC", e.dbName),
		fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s, %s", e.dbName, e.migrateRole, e.runtimeRole),
		"REVOKE ALL ON SCHEMA public FROM PUBLIC",
		fmt.Sprintf("GRANT USAGE, CREATE ON SCHEMA public TO %s", e.migrateRole),
		fmt.Sprintf("GRANT USAGE ON SCHEMA public TO %s", e.runtimeRole),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s", e.migrateRole, e.runtimeRole),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO %s", e.migrateRole, e.runtimeRole),
	)
	return e
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

func TestRoleWithoutConnectIsRefused(t *testing.T) {
	e := setup(t)
	if err := migrations.Up(e.migrateDSN(t)); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, e.noConnDSN(t))
	if err != nil {
		// A lazy pool may still parse fine; the failure should come on use.
		return
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err == nil {
		t.Error("role without CONNECT reached the database; it must be refused")
	} else if !strings.Contains(strings.ToLower(err.Error()), "permission") &&
		!strings.Contains(strings.ToLower(err.Error()), "denied") &&
		!strings.Contains(strings.ToLower(err.Error()), "not permitted") {
		// Any connection error is acceptable, but log for diagnostics.
		t.Logf("no-connect role refused with: %v", err)
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
