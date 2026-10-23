-- FMT-8 / BE-002: initial User module schema.
-- Forward-only. Applied as the user_migrate role from cmd/migrate.
CREATE TABLE users (
    id         BIGSERIAL PRIMARY KEY,
    email      TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
