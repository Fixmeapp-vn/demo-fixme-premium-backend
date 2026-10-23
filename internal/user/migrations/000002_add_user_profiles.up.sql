-- Second migration: a sequence-backed table added by a FUTURE migration.
--
-- user_runtime is granted nothing here. The INSERT privilege and the USAGE on
-- the bigserial sequence it needs to insert come solely from the default
-- privileges attached to the user_migrate role at bootstrap. An integration
-- test relies on that to prove the default-privilege boundary holds for
-- objects created after the grants were set up. Forward-only: no down file.

CREATE TABLE user_profiles (
    id           bigserial PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users (id),
    display_name text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
