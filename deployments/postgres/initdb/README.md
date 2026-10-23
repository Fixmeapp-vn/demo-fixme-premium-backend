# deployments/postgres/initdb

Scripts here run **once, in filename order, as the Postgres superuser** the
first time the `postgres` data directory is initialized (the standard
`docker-entrypoint-initdb.d` hook). They are not re-run on later container
starts.

`10-bootstrap-user-db.sh` provisions the isolated User module database for
FMT-8 / BE-002:

- a dedicated database `user_db`, owned by `user_migrate`;
- two least-privilege login roles:
  - `user_migrate` — owns the `public` schema and holds DDL; used only by
    `cmd/migrate`;
  - `user_runtime` — `USAGE` on the schema plus DML (`SELECT/INSERT/UPDATE/
    DELETE`) on tables and `USAGE, SELECT` on sequences, granted automatically
    to objects created by future migrations via `ALTER DEFAULT PRIVILEGES`;
    used by the api and worker;
- `CONNECT` on `user_db` revoked from `PUBLIC` and granted only to those two
  roles.

Role passwords are read from `USER_MIGRATE_PASSWORD` and
`USER_RUNTIME_PASSWORD`. These are **development** provisioning values, not
production secrets.
