# cmd/migrate

Forward-only database migration entry point (FMT-8 / BE-002). It reads only
`USER_MIGRATE_DATABASE_URL` and applies the embedded migrations from
`internal/user/db/migrations` as the `user_migrate` role. It supports the `up`
and `version` subcommands and is the only place the migrate DSN is used. Run it
with `make migrate`.
