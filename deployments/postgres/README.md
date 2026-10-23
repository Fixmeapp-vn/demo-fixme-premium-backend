# deployments/postgres

Bootstrap and privilege model for the isolated **User module** database.
This directory is the single source of truth for how `user_db` and its roles
are provisioned.

## Database isolation

The User module owns a dedicated database, `user_db` — not a shared database
and not a schema inside another database. It has its own credentials and its
own migrations (`internal/user/migrations`).

## Roles (least privilege)

| Role           | Login | Capabilities                                             | Used by          |
| -------------- | ----- | -------------------------------------------------------- | ---------------- |
| `user_migrate` | yes   | Owns `user_db` and every object in it; full DDL          | `cmd/migrate` only |
| `user_runtime` | yes   | DML only (`SELECT/INSERT/UPDATE/DELETE`), sequence usage | `api`, `worker`  |

`user_runtime` has **no** `CREATE/ALTER/DROP` and owns nothing.

## Privilege model

`initdb/00-init-user-db.sh` runs once, as the superuser, on first start of the
official `postgres` image (mount it into `/docker-entrypoint-initdb.d/`). It:

1. Creates the two `LOGIN` roles, reading passwords from `USER_MIGRATE_PASSWORD`
   and `USER_RUNTIME_PASSWORD` (no secrets are hardcoded).
2. `CREATE DATABASE user_db OWNER user_migrate`.
3. Inside `user_db`:
   - `REVOKE ALL ON DATABASE user_db FROM PUBLIC` and grants `CONNECT` only to
     `user_migrate` and `user_runtime`.
   - `REVOKE ALL ON SCHEMA public FROM PUBLIC`; grants `USAGE, CREATE` to
     `user_migrate` and `USAGE` only to `user_runtime`.
   - `ALTER DEFAULT PRIVILEGES FOR ROLE user_migrate` so objects created by
     **future** migrations automatically grant DML (`SELECT/INSERT/UPDATE/
     DELETE` on tables) and sequence access (`USAGE, SELECT` on sequences) to
     `user_runtime` — with nothing granted to `PUBLIC`.

Because the default privileges are attached to `user_migrate`, a new table in
a later migration is immediately usable by `user_runtime` without any explicit
`GRANT` in that migration.

## Connection strings

| Variable                     | Role           | Read by          | Purpose              |
| ---------------------------- | -------------- | ---------------- | -------------------- |
| `USER_DATABASE_URL`          | `user_runtime` | `api`, `worker`  | Runtime DML          |
| `USER_MIGRATE_DATABASE_URL`  | `user_migrate` | `cmd/migrate`    | Forward-only DDL     |

Neither URL is ever logged, and `USER_MIGRATE_DATABASE_URL` is deliberately
absent from the shared `config` package.

## Out of scope

The full Docker Compose / hot-reload developer suite is **FMT-18**; the
cross-module CI import check is **FMT-17**. This directory covers only the
Postgres instance, the migrate step and the two-role boundary.
