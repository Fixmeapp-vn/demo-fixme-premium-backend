#!/usr/bin/env bash
#
# Bootstraps the isolated User module database (user_db) and its two roles on
# first start of the official postgres image. Drop this file into the image's
# /docker-entrypoint-initdb.d/; it runs once, as the superuser, against a fresh
# data directory.
#
# This script is the single source of truth for the least-privilege model:
#
#   user_migrate  LOGIN, owns user_db and every object in it, has DDL.
#                 Used ONLY by cmd/migrate.
#   user_runtime  LOGIN, DML only (no CREATE/ALTER/DROP, no ownership).
#                 Used by the api and worker binaries.
#
# Role passwords come from the environment; no secrets are hardcoded. The full
# Docker Compose / hot-reload dev suite is FMT-18 and out of scope here.
set -euo pipefail

: "${USER_MIGRATE_PASSWORD:?USER_MIGRATE_PASSWORD must be set}"
: "${USER_RUNTIME_PASSWORD:?USER_RUNTIME_PASSWORD must be set}"

DB_NAME="user_db"
SUPER_DB="${POSTGRES_DB:-postgres}"

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

# Apply the privilege model inside user_db.
psql_super --dbname "$DB_NAME" <<-EOSQL
	-- Database: only the two named roles may connect.
	REVOKE ALL ON DATABASE ${DB_NAME} FROM PUBLIC;
	GRANT CONNECT ON DATABASE ${DB_NAME} TO user_migrate, user_runtime;

	-- Public schema: no implicit PUBLIC privileges. user_migrate may create
	-- objects (DDL); user_runtime may only use what already exists.
	REVOKE ALL ON SCHEMA public FROM PUBLIC;
	GRANT USAGE, CREATE ON SCHEMA public TO user_migrate;
	GRANT USAGE ON SCHEMA public TO user_runtime;

	-- Objects created by FUTURE migrations (run as user_migrate) automatically
	-- grant DML and sequence access to user_runtime, with nothing to PUBLIC.
	ALTER DEFAULT PRIVILEGES FOR ROLE user_migrate IN SCHEMA public
	  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO user_runtime;
	ALTER DEFAULT PRIVILEGES FOR ROLE user_migrate IN SCHEMA public
	  GRANT USAGE, SELECT ON SEQUENCES TO user_runtime;
EOSQL

echo "user_db bootstrap complete"
