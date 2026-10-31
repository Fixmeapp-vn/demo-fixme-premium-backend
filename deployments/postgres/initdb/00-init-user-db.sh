#!/usr/bin/env bash
#
# Bootstraps the isolated User module database (user_db) and its two roles on
# first start of the official postgres image. Drop this file into the image's
# /docker-entrypoint-initdb.d/; it runs once, as the superuser, against a fresh
# data directory.
#
# The roles it provisions:
#
#   user_migrate  LOGIN, owns user_db and every object in it, has DDL.
#                 Used ONLY by cmd/migrate.
#   user_runtime  LOGIN, DML only (no CREATE/ALTER/DROP, no ownership).
#                 Used by the api and worker binaries.
#
# The in-database privilege model is NOT hardcoded here: it lives in the
# canonical sql/user-db-privileges.sql, which the integration test also
# executes, so the two cannot drift. This script creates the roles and database
# and then applies that file.
#
# Role passwords come from the environment; no secrets are hardcoded. The full
# Docker Compose / hot-reload dev suite is FMT-18 and out of scope here.
set -euo pipefail

: "${USER_MIGRATE_PASSWORD:?USER_MIGRATE_PASSWORD must be set}"
: "${USER_RUNTIME_PASSWORD:?USER_RUNTIME_PASSWORD must be set}"

DB_NAME="user_db"
SUPER_DB="${POSTGRES_DB:-postgres}"
# Schema that isolates golang-migrate's bookkeeping table from user_runtime.
# Must match migrations.MigrationsSchema in internal/user/migrations.
MIGRATE_SCHEMA="migrate_meta"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PRIVILEGES_SQL="${SCRIPT_DIR}/sql/user-db-privileges.sql"

psql_super() {
	psql -v ON_ERROR_STOP=1 --no-psqlrc --username "$POSTGRES_USER" "$@"
}

# create_role makes a LOGIN role if it is absent (idempotent). The role name
# and password are passed as psql variables and quoted with :"..." (identifier)
# and :'...' (string literal). The SQL is fed on stdin, not via -c, because
# psql performs variable interpolation only when scanning stdin. Special
# characters are handled safely and the password never appears in SQL this
# script builds by hand.
create_role() {
	local role="$1" pw="$2"
	if [ "$(psql_super --dbname "$SUPER_DB" -tAc "SELECT 1 FROM pg_roles WHERE rolname = '${role}'")" != "1" ]; then
		psql_super --dbname "$SUPER_DB" -v role="$role" -v pw="$pw" <<-'EOSQL'
			CREATE ROLE :"role" LOGIN PASSWORD :'pw';
		EOSQL
	fi
}

create_role user_migrate "$USER_MIGRATE_PASSWORD"
create_role user_runtime "$USER_RUNTIME_PASSWORD"

# CREATE DATABASE cannot run in a transaction or a DO block, so it is guarded
# on its own for idempotency.
if [ "$(psql_super --dbname "$SUPER_DB" -tAc "SELECT 1 FROM pg_database WHERE datname = '${DB_NAME}'")" != "1" ]; then
	psql_super --dbname "$SUPER_DB" -c "CREATE DATABASE ${DB_NAME} OWNER user_migrate"
fi

# Apply the privilege model inside user_db from the canonical SQL file (the
# single source of truth the integration test also runs). Placeholders are
# substituted with the concrete database, role and schema names; the result is
# piped to psql, which stops on the first error (ON_ERROR_STOP via psql_super).
sed \
	-e "s/__db_name__/${DB_NAME}/g" \
	-e "s/__migrate_role__/user_migrate/g" \
	-e "s/__runtime_role__/user_runtime/g" \
	-e "s/__migrate_schema__/${MIGRATE_SCHEMA}/g" \
	"$PRIVILEGES_SQL" | psql_super --dbname "$DB_NAME" -f -

echo "user_db bootstrap complete"
