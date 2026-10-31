-- Canonical least-privilege model for the User module database (user_db),
-- applied INSIDE that database. This file is the single source of truth: both
-- deployments/postgres/initdb/00-init-user-db.sh and the integration test
-- (internal/user/dbtest) execute it against the target database, so a broken
-- policy here fails the tests rather than silently diverging from production.
--
-- It contains plain SQL only (no psql meta-commands), so it runs equally under
-- psql and under a pgx connection. Identifiers are placeholders that each
-- caller substitutes with concrete names before execution:
--
--   __db_name__         the database (user_db)
--   __migrate_role__    the DDL-capable owner role (user_migrate)
--   __runtime_role__    the DML-only runtime role (user_runtime)
--   __migrate_schema__  the schema that holds golang-migrate's version table
--
-- Run it as a superuser (or the database owner) connected to __db_name__.

-- Database: only the two named roles may connect.
REVOKE ALL ON DATABASE __db_name__ FROM PUBLIC;
GRANT CONNECT ON DATABASE __db_name__ TO __migrate_role__, __runtime_role__;

-- Public schema: no implicit PUBLIC privileges. user_migrate may create objects
-- (DDL); user_runtime may only use what already exists. Business tables live
-- here, so the default privileges below cover them.
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO __migrate_role__;
GRANT USAGE ON SCHEMA public TO __runtime_role__;

-- Dedicated schema for golang-migrate's bookkeeping (version/dirty) table,
-- owned by user_migrate. user_runtime is granted nothing here and no USAGE, so
-- it can neither read nor UPDATE the migration version and desync the schema
-- state. PUBLIC gets nothing.
CREATE SCHEMA IF NOT EXISTS __migrate_schema__ AUTHORIZATION __migrate_role__;
REVOKE ALL ON SCHEMA __migrate_schema__ FROM PUBLIC;

-- Objects created by FUTURE migrations (run as user_migrate) in public
-- automatically grant DML and sequence access to user_runtime, with nothing to
-- PUBLIC. Scoped to public ONLY, so the bookkeeping table in __migrate_schema__
-- is never covered by these grants.
ALTER DEFAULT PRIVILEGES FOR ROLE __migrate_role__ IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO __runtime_role__;
ALTER DEFAULT PRIVILEGES FOR ROLE __migrate_role__ IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO __runtime_role__;
