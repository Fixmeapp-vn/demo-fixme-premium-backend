# demo-fixme-premium-backend

Backend for FixMe Premium: a Go modular monolith with one public API binary
and one background worker binary.

This repository currently contains the **application foundation** (FMT-7 /
Backend Foundation epic): configuration, structured logging, the HTTP server,
middleware, error responses, liveness, metrics and graceful shutdown. Business
modules build on top of it in later stories.

## Prerequisites

- Go **1.22** (pinned in `go.mod`; `go version` must report 1.22 or newer)
- `make`
- A C toolchain (e.g. `gcc`) only for `make test-race`

PostgreSQL and Docker are **not** required yet.

## Quick start

```sh
make run-api        # public API on :8080, metrics on :9090
make run-worker     # background worker (no jobs yet)
```

```sh
curl -i localhost:8080/health/live          # 200 {"status":"ok"}
curl -i localhost:8080/nope                 # 404 application/problem+json
curl -i -X POST localhost:8080/health/live  # 405 with Allow: GET
curl -s localhost:9090/metrics | grep http_requests_total
```

Stop either process with `Ctrl+C` or `SIGTERM`; it shuts down gracefully and
exits with code 0.

## Configuration

All configuration comes from environment variables. Every variable has a
default, so none need to be set for local development. `make run-*` also loads
a `.env` file when present; copy [`.env.example`](.env.example) to get started.

| Variable                   | Default       | Description                                                   |
| -------------------------- | ------------- | ------------------------------------------------------------- |
| `APP_ENV`                  | `development` | One of `development`, `test`, `staging`, `production`         |
| `HTTP_ADDR`                | `:8080`       | Public API listen address (`host:port`)                       |
| `HTTP_READ_TIMEOUT`        | `15s`         | Max time to read a full request                               |
| `HTTP_READ_HEADER_TIMEOUT` | `5s`          | Max time to read request headers (≤ `HTTP_READ_TIMEOUT`)      |
| `HTTP_WRITE_TIMEOUT`       | `30s`         | Max time to write a response                                  |
| `HTTP_IDLE_TIMEOUT`        | `60s`         | Keep-alive idle timeout                                       |
| `SHUTDOWN_TIMEOUT`         | `20s`         | Graceful shutdown deadline for requests (API) or jobs (worker) |
| `METRICS_ADDR`             | `:9090`       | Internal metrics listen address; must differ from `HTTP_ADDR` |
| `LOG_LEVEL`                | `info`        | `debug`, `info`, `warn` or `error`                            |

Durations use Go syntax (`500ms`, `15s`, `1m`) and must be between 100ms and
10m. Invalid configuration stops startup with exit code 1, and every invalid
variable is reported in the same message:

```
api: invalid configuration:
APP_ENV: invalid value "prod": must be one of development, test, staging, production
SHUTDOWN_TIMEOUT: 1h0m0s is out of range: must be between 100ms and 10m0s
```

## Development commands

| Command           | Purpose                                |
| ----------------- | -------------------------------------- |
| `make run-api`    | Run the API locally                    |
| `make run-worker` | Run the background worker locally      |
| `make build`      | Build `bin/api` and `bin/worker`       |
| `make test`       | Run unit tests                         |
| `make test-race`  | Run unit tests with the race detector  |
| `make fmt`        | Format Go code                         |
| `make vet`        | Run `go vet`                           |

## Project structure

```
cmd/
  api/              API entry point: config, logger, API + metrics listeners
  worker/           Worker entry point and job runner
  migrate/          reserved (BE-002)
internal/
  platform/         shared technical packages; never import business modules
    config/         typed env configuration and validation
    logging/        slog JSON logger, request ID context propagation
    httpx/          router, middleware, Problem Details, server lifecycle
    observability/  Prometheus registry, HTTP and runtime metrics
  user/             reserved (BE-004+)
contracts/openapi/  reserved for API contracts
deployments/        reserved (BE-012)
```

Entry points stay thin: `main` only wires signals and calls `run`, which
returns an exit code and is covered by tests.

## Implemented capabilities

### HTTP API

- Chi router. Business modules implement `httpx.Module` and are mounted
  under `/api/v1` via `httpx.NewRouter(deps, modules...)`.
- `GET /health/live` returns `200 {"status":"ok"}` and has no external
  dependencies.
- Errors use [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) Problem
  Details with `Content-Type: application/problem+json` and a stable `code`:

  ```json
  {
    "type": "about:blank",
    "title": "Not Found",
    "status": 404,
    "detail": "The requested resource was not found.",
    "code": "http.not_found"
  }
  ```

  Platform codes are `http.not_found`, `http.method_not_allowed` (with an
  `Allow` header) and `http.internal_error`. Modules add their own codes with
  `httpx.WriteProblem(w, httpx.NewProblem(status, "user.not_found", detail))`.

### Middleware

Middleware runs in this order, outermost first:

1. **Request ID**: accepts an incoming `X-Request-ID` of 1–128 characters from
   `A-Z a-z 0-9 - _ . :`; otherwise generates a 32-character hex ID. The ID is
   stored in the request context (`httpx.RequestIDFrom(r)`), echoed in the
   response, and added to every log record written with that context.
2. **Access log**: one JSON record per request with method, route template
   (e.g. `/api/v1/users/{id}`, never the raw path or query), status, bytes,
   duration and request ID. `/health/*` requests are logged at debug level.
3. **Metrics**: records request count and duration.
4. **Panic recovery**: logs the panic and stack trace, and returns a 500 problem
   if the response has not been written yet. Internals are never returned to
   the client.

Recovery runs innermost so that access logs and metrics record the
recovered 500.

### Logging

JSON via `log/slog` on stdout. Every record has `time`, `level`, `msg`,
`service` (`api` or `worker`) and `env`. Loggers are passed explicitly; there
is no global logger. Configuration is logged through an allow-list
(`Config.LogValue`), so new sensitive fields are not logged by default.

### Metrics

Served only on the internal `METRICS_ADDR` listener at `GET /metrics`, never on
the public router:

- `http_requests_total{method,route,status}`
- `http_request_duration_seconds{method,route,status}` (histogram)
- Go runtime (`go_*`) and process (`process_*`) metrics

Labels are bounded: routes are templates, unmatched requests (404/405) use
`route="unmatched"`, and non-standard methods use `method="OTHER"`.

### Graceful shutdown

The API and worker handle `SIGINT` and `SIGTERM`. The API stops accepting
connections and gives in-flight requests up to `SHUTDOWN_TIMEOUT` to finish.
Requests still running at the deadline have their contexts cancelled and
their connections closed, and the process exits with code 1. A clean shutdown
exits with 0. If either listener fails to start, the other is shut down and the
process exits with code 1. Request contexts are also cancelled when the client
disconnects.

### Worker

`cmd/worker` loads the same configuration and logging, then runs registered
`Job`s (`Name()`, `Run(ctx)`) until shut down. Each job gets up to
`SHUTDOWN_TIMEOUT` to return after cancellation. No jobs are registered yet.

## Not yet implemented

| Capability                                      | Story   |
| ----------------------------------------------- | ------- |
| PostgreSQL, migrations (`cmd/migrate`), readiness checks | BE-002  |
| Authentication and authorization middleware     | BE-003  |
| User module (`internal/user`)                   | BE-004+ |
| Event processing and outbox                     | BE-010  |
| CI pipelines and architecture linting           | BE-011  |
| Dockerfile, Docker Compose, onboarding environment | BE-012  |
