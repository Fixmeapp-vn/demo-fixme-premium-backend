#!/bin/sh
# FMT-8 / BE-002: provision the isolated User module database and its two
# least-privilege roles. Runs once as the Postgres superuser at container init
# (docker-entrypoint-initdb.d). DEV provisioning only — not production secrets.
set -eu
: "${USER_MIGRATE_PASSWORD:?USER_MIGRATE_PASSWORD must be set}"
: "${USER_RUNTIME_PASSWORD:?USER_RUNTIME_PASSWORD must be set}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<SQL
-- Roles: user_migrate owns the schema and has DDL; user_runtime is DML-only.
CREATE ROLE user_migrate LOGIN PASSWORD '${USER_MIGRATE_PASSWORD}';
CREATE ROLE user_runtime LOGIN PASSWORD '${USER_RUNTIME_PASSWORD}';

-- Dedicated database owned by the migrate role.
CREATE DATABASE user_db OWNER user_migrate;

-- Least-privilege CONNECT: revoke from PUBLIC, grant only to the two roles.
REVOKE ALL ON DATABASE user_db FROM PUBLIC;
GRANT CONNECT ON DATABASE user_db TO user_migrate;
GRANT CONNECT ON DATABASE user_db TO user_runtime;
SQL

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname user_db <<SQL
-- public schema: migrate owns DDL; runtime gets USAGE only, never CREATE.
REVOKE ALL ON SCHEMA public FROM PUBLIC;
ALTER SCHEMA public OWNER TO user_migrate;
GRANT USAGE ON SCHEMA public TO user_runtime;

-- Default privileges so objects created by FUTURE migrations (as user_migrate)
-- automatically grant DML to user_runtime — no per-migration GRANT needed.
ALTER DEFAULT PRIVILEGES FOR ROLE user_migrate IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO user_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE user_migrate IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO user_runtime;
SQL
