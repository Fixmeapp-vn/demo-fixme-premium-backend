// Command migrate applies the User module's forward-only database migrations.
//
// It reads the migration DSN only from USER_MIGRATE_DATABASE_URL (the
// user_migrate role, which owns the schema and holds DDL rights). That DSN is
// intentionally never routed through the shared config package, so the api and
// worker binaries cannot read it. The DSN value is never logged.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/logging"
	userdb "github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/user/db"
)

const serviceName = "migrate"

// envMigrateURL is the only source of the migration DSN.
const envMigrateURL = "USER_MIGRATE_DATABASE_URL"

func main() {
	os.Exit(run(os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr))
}

// run executes a migration subcommand and returns the process exit code.
func run(args []string, lookup func(string) (string, bool), stdout, stderr io.Writer) int {
	dsn, ok := lookup(envMigrateURL)
	if !ok || dsn == "" {
		fmt.Fprintf(stderr, "%s: %s is not set\n", serviceName, envMigrateURL)
		return 1
	}

	logger := newLogger(stdout, lookup)

	command := "up"
	if len(args) > 0 {
		command = args[0]
	}

	switch command {
	case "up":
		logger.Info("applying migrations")
		if err := userdb.Up(dsn); err != nil {
			logger.Error("migration failed", slog.Any("error", err))
			return 1
		}
		version, dirty, err := userdb.Version(dsn)
		if err != nil {
			logger.Error("could not read migration version", slog.Any("error", err))
			return 1
		}
		logger.Info("migrations applied", slog.Uint64("version", uint64(version)), slog.Bool("dirty", dirty))
		return 0
	case "version":
		version, dirty, err := userdb.Version(dsn)
		if err != nil {
			logger.Error("could not read migration version", slog.Any("error", err))
			return 1
		}
		fmt.Fprintf(stdout, "version=%d dirty=%t\n", version, dirty)
		return 0
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q (usage: migrate up|version)\n", serviceName, command)
		return 1
	}
}

func newLogger(w io.Writer, lookup func(string) (string, bool)) *slog.Logger {
	env := "development"
	if v, ok := lookup("APP_ENV"); ok && v != "" {
		env = v
	}
	level := slog.LevelInfo
	if v, ok := lookup("LOG_LEVEL"); ok && v != "" {
		_ = level.UnmarshalText([]byte(v))
	}
	return logging.New(w, logging.Options{Service: serviceName, Env: env, Level: level})
}
