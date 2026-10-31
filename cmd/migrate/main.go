// Command migrate applies the User module database migrations.
//
// It is the only entry point that connects as the privileged user_migrate
// role. Its DSN is read directly from USER_MIGRATE_DATABASE_URL and never
// through the shared config package, so the api and worker binaries cannot
// reach the DDL-capable role. Migrations are forward-only: there is no down
// command. The DSN is never logged.
//
// Usage:
//
//	migrate [up|version]
//
// "up" (the default) applies all pending migrations. "version" prints the
// current schema version.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/golang-migrate/migrate/v4"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/user/migrations"
)

const (
	serviceName   = "migrate"
	migrateURLEnv = "USER_MIGRATE_DATABASE_URL"
)

func main() {
	os.Exit(run(os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr))
}

// run parses args, reads the migrate DSN from the environment, and runs the
// requested command. It returns the process exit code.
func run(args []string, lookup func(string) (string, bool), stdout, stderr io.Writer) int {
	cmd := "up"
	if len(args) > 0 {
		cmd = args[0]
	}
	if cmd != "up" && cmd != "version" {
		fmt.Fprintf(stderr, "%s: unknown command %q (want: up, version)\n", serviceName, cmd)
		return 2
	}

	dsn, ok := lookup(migrateURLEnv)
	if dsn = strings.TrimSpace(dsn); !ok || dsn == "" {
		fmt.Fprintf(stderr, "%s: %s is not set\n", serviceName, migrateURLEnv)
		return 1
	}

	switch cmd {
	case "up":
		if err := migrations.Up(dsn); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", serviceName, err)
			return 1
		}
		fmt.Fprintln(stdout, "migrations up to date")
		return 0
	default: // "version"
		v, dirty, err := migrations.Version(dsn)
		if errors.Is(err, migrate.ErrNilVersion) {
			fmt.Fprintln(stdout, "no migrations applied")
			return 0
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", serviceName, err)
			return 1
		}
		fmt.Fprintf(stdout, "version %d (dirty=%t)\n", v, dirty)
		return 0
	}
}
