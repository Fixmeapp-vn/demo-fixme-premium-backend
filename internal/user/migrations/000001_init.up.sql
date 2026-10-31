-- Initial User module schema.
--
-- Migrations run as the user_migrate role, which owns every object created
-- here. The user_runtime role is granted no privileges in this file: its DML
-- and sequence access arrive through ALTER DEFAULT PRIVILEGES FOR ROLE
-- user_migrate, provisioned once at database bootstrap (see
-- deployments/postgres). Forward-only: there is no down migration.

CREATE TABLE users (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email      text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);
