-- FMT-8 / BE-002: second migration — proves ALTER DEFAULT PRIVILEGES covers
-- objects created by later migrations (no explicit GRANT needed here).
CREATE TABLE user_profiles (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    display_name TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
