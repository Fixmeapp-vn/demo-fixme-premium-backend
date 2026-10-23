# cmd/migrate

Applies the User module database migrations (`internal/user/migrations`),
forward-only.

This is the **only** entry point that connects as the privileged
`user_migrate` role. It reads its DSN directly from
`USER_MIGRATE_DATABASE_URL` — never through the shared `config` package — so
the api and worker binaries cannot reach the DDL-capable role. The DSN is
never logged.

```sh
USER_MIGRATE_DATABASE_URL=postgres://user_migrate:...@host:5432/user_db \
  go run ./cmd/migrate up        # apply all pending migrations (default)

USER_MIGRATE_DATABASE_URL=... go run ./cmd/migrate version   # print current version
```

`make migrate` runs `up`. There is intentionally no `down` command: migrations
are forward-only, and reversing one is a deliberate future decision.
