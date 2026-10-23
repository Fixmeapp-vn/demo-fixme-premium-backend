# deployments

`deployments/docker-compose.yml` provides the minimal Postgres plus a one-shot
migrate step for FMT-8 / BE-002. It provisions `user_db` and the
`user_migrate` / `user_runtime` roles via `postgres/initdb/`. The full
dev/hot-reload suite remains FMT-18.

Reserved for broader container and deployment assets (additional Dockerfiles,
deployment manifests) in BE-012.
