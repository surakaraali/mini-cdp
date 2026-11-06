# Mini CDP

A small customer data platform (CDP) backend written in Go.
An online shop sends user events (page views, add to cart, purchases) to the API;
the service stores them in PostgreSQL, builds a profile per user and answers
questions like *"which users added something to their cart in the last 7 days but did not buy?"*

> Work in progress. The full README (API reference, design notes, performance results) comes with the last step.

## Status

- [x] **1. Skeleton**: project layout, PostgreSQL in Docker, schema migrations, `/health`, middleware, tests
- [ ] **2. Event ingestion**: `POST /v1/events` and `POST /v1/events/batch`, idempotent by `event_id`
- [ ] **3. Profiles and segments**: user profile, segment rules (JSON to parameterized SQL), segment members
- [ ] **4. Performance**: 1M generated events, `EXPLAIN ANALYZE` before and after indexing
- [ ] **5. Packaging**: Dockerfile, GitHub Actions CI, OpenAPI spec, final docs

## Quick start

Requirements: Go 1.27+, Docker with Compose, `make`.

```bash
make db-up     # start PostgreSQL on localhost:5544
make run       # start the API on :8080 (applies migrations on startup)
curl localhost:8080/health
# {"status":"ok","database":"up"}
```

`make help` lists every command.

## Tests

```bash
make test        # all tests; the database tests use the cdp_test database
make test-unit   # only the tests that need no database
```

## Configuration

| Variable           | Default                                                  |
|--------------------|----------------------------------------------------------|
| `HTTP_ADDR`        | `:8080`                                                  |
| `DATABASE_URL`     | `postgres://cdp:cdp@localhost:5544/cdp?sslmode=disable`  |
| `LOG_LEVEL`        | `info` (`debug`, `info`, `warn`, `error`)                |
| `SHUTDOWN_TIMEOUT` | `10s`                                                    |

## Project layout

```
cmd/api/              entry point: config, database, HTTP server, graceful shutdown
internal/config/      settings from environment variables
internal/db/          connection pool and embedded SQL migrations (goose)
internal/httpapi/     routes, handlers, middleware (request id, logging, panic recovery)
deploy/initdb/        SQL run once when the Postgres volume is created
docs/                 design and process notes
```

## Tech choices

- **Go standard library `net/http`** for routing (method + path patterns since Go 1.22), no web framework.
- **pgx** as the PostgreSQL driver and connection pool.
- **goose** for versioned SQL migrations, embedded in the binary.
- **log/slog** for structured JSON logs.

## How this was built

I used an AI coding assistant during development and reviewed its output before each commit.
What it got wrong and how it was fixed is listed in [docs/ai-assisted-development.md](docs/ai-assisted-development.md).
