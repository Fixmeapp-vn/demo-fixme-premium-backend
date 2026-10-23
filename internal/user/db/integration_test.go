//go:build integration

package db

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Test role passwords. These exist only for the ephemeral test database.
const (
	testMigratePassword  = "migratepw"
	testRuntimePassword  = "runtimepw"
	testNoRightsPassword = "norightspw"
)

// deriveDSN rewrites the admin DSN with new credentials and database name,
// keeping host, port and query parameters (e.g. sslmode).
func deriveDSN(t *testing.T, adminURL, user, password, dbname string) string {
	t.Helper()
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse admin URL: %v", err)
	}
	u.User = url.UserPassword(user, password)
	u.Path = "/" + dbname
	return u.String()
}

func connect(t *testing.T, ctx context.Context, dsn string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return conn
}

func mustExec(t *testing.T, ctx context.Context, conn *pgx.Conn, sql string) {
	t.Helper()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", firstLine(sql), err)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// teardown drops the test database and roles so the test can run repeatedly.
// Failures are ignored: the objects may not exist yet.
func teardown(ctx context.Context, admin *pgx.Conn) {
	_, _ = admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = 'user_db' AND pid <> pg_backend_pid()`)
	for _, stmt := range []string{
		`DROP DATABASE IF EXISTS user_db`,
		`DROP ROLE IF EXISTS user_runtime`,
		`DROP ROLE IF EXISTS user_migrate`,
		`DROP ROLE IF EXISTS user_norights`,
	} {
		_, _ = admin.Exec(ctx, stmt)
	}
}

func TestIntegrationTwoRoleBoundary(t *testing.T) {
	adminURL := os.Getenv("FMT8_TEST_ADMIN_URL")
	if adminURL == "" {
		t.Skip("set FMT8_TEST_ADMIN_URL to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin := connect(t, ctx, adminURL)
	defer admin.Close(ctx)

	// Clean slate, then re-run the same bootstrap the init script performs.
	teardown(ctx, admin)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		c, err := pgx.Connect(cleanupCtx, adminURL)
		if err != nil {
			return
		}
		defer c.Close(cleanupCtx)
		teardown(cleanupCtx, c)
	})

	mustExec(t, ctx, admin, `CREATE ROLE user_migrate LOGIN PASSWORD '`+testMigratePassword+`'`)
	mustExec(t, ctx, admin, `CREATE ROLE user_runtime LOGIN PASSWORD '`+testRuntimePassword+`'`)
	// user_norights logs in but is never granted CONNECT on user_db.
	mustExec(t, ctx, admin, `CREATE ROLE user_norights LOGIN PASSWORD '`+testNoRightsPassword+`'`)
	mustExec(t, ctx, admin, `CREATE DATABASE user_db OWNER user_migrate`)
	mustExec(t, ctx, admin, `REVOKE ALL ON DATABASE user_db FROM PUBLIC`)
	mustExec(t, ctx, admin, `GRANT CONNECT ON DATABASE user_db TO user_migrate`)
	mustExec(t, ctx, admin, `GRANT CONNECT ON DATABASE user_db TO user_runtime`)

	// Schema-level privileges run inside user_db.
	adminUserDB := connect(t, ctx, deriveDSN(t, adminURL, urlUser(t, adminURL), urlPassword(t, adminURL), "user_db"))
	defer adminUserDB.Close(ctx)
	mustExec(t, ctx, adminUserDB, `REVOKE ALL ON SCHEMA public FROM PUBLIC`)
	mustExec(t, ctx, adminUserDB, `ALTER SCHEMA public OWNER TO user_migrate`)
	mustExec(t, ctx, adminUserDB, `GRANT USAGE ON SCHEMA public TO user_runtime`)
	mustExec(t, ctx, adminUserDB, `ALTER DEFAULT PRIVILEGES FOR ROLE user_migrate IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO user_runtime`)
	mustExec(t, ctx, adminUserDB, `ALTER DEFAULT PRIVILEGES FOR ROLE user_migrate IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO user_runtime`)

	migrateDSN := deriveDSN(t, adminURL, "user_migrate", testMigratePassword, "user_db")
	runtimeDSN := deriveDSN(t, adminURL, "user_runtime", testRuntimePassword, "user_db")
	noRightsDSN := deriveDSN(t, adminURL, "user_norights", testNoRightsPassword, "user_db")

	// Migrations run as user_migrate through the production code path.
	if err := Up(migrateDSN); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	version, dirty, err := Version(migrateDSN)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != 2 || dirty {
		t.Fatalf("Version() = (%d, dirty=%t), want (2, false)", version, dirty)
	}
	// Second Up is an idempotent no-op.
	if err := Up(migrateDSN); err != nil {
		t.Fatalf("second Up() error = %v", err)
	}

	// Exercise the runtime role's privileges.
	runtime := connect(t, ctx, runtimeDSN)
	defer runtime.Close(ctx)

	if _, err := runtime.Exec(ctx, `CREATE TABLE should_fail (id BIGINT)`); err == nil {
		t.Error("user_runtime created a table, want permission denied")
	}

	var userID int64
	if err := runtime.QueryRow(ctx, `INSERT INTO users (email) VALUES ('a@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("user_runtime INSERT users: %v (sequence USAGE missing?)", err)
	}
	if userID <= 0 {
		t.Fatalf("inserted user id = %d, want > 0", userID)
	}

	var profileID int64
	if err := runtime.QueryRow(ctx,
		`INSERT INTO user_profiles (user_id, display_name) VALUES ($1, 'n') RETURNING id`, userID,
	).Scan(&profileID); err != nil {
		t.Fatalf("user_runtime INSERT user_profiles: %v (default privileges missing on 2nd migration?)", err)
	}

	var count int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("user_runtime SELECT users: %v", err)
	}
	if count != 1 {
		t.Errorf("users count = %d, want 1", count)
	}

	// A role without CONNECT cannot reach user_db (PUBLIC connect is revoked).
	if noRights, err := pgx.Connect(ctx, noRightsDSN); err == nil {
		noRights.Close(ctx)
		t.Error("user_norights connected to user_db, want permission denied")
	}

	// Readiness-style ping over a pool as the runtime role.
	pool, err := pgxpool.New(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("runtime pool Ping: %v", err)
	}
}

func urlUser(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse admin URL: %v", err)
	}
	if u.User == nil {
		t.Fatal("admin URL has no user")
	}
	return u.User.Username()
}

func urlPassword(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse admin URL: %v", err)
	}
	if u.User == nil {
		return ""
	}
	pw, _ := u.User.Password()
	return pw
}
