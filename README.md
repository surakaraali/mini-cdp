# Mini CDP

[![CI](https://github.com/surakaraali/mini-cdp/actions/workflows/ci.yml/badge.svg)](https://github.com/surakaraali/mini-cdp/actions/workflows/ci.yml)

A small customer data platform (CDP) backend in Go and PostgreSQL.

An online shop sends what its users do (page views, add to cart, purchases). The service:

- **collects events** safely: retries never create duplicates;
- **builds a profile** per user: attributes, activity, purchase totals;
- **finds audiences**, e.g. *"added something to the cart in the last 7 days, did not buy, lives in Istanbul"*,
  by compiling JSON rules into one parameterized SQL query.

This is the core that customer engagement platforms build on before they send a single email or push message.

```mermaid
flowchart LR
    shop["Online shop (web, app)"] -->|"POST /v1/events"| http
    tool["Marketing tool"] -->|"profiles, segments"| http
    subgraph service["Go service"]
        http["httpapi: routing, strict JSON, middleware"] --> domain["events, segments: rules, validation, SQL compiler"]
        http --> store["store: all SQL"]
    end
    store --> pg[("PostgreSQL: users, events, segments")]
```

## Run it

With Docker only:

```bash
docker compose up --build        # API on :8080, PostgreSQL on :5544
curl localhost:8080/health       # {"status":"ok","database":"up"}
```

Track an event, then read the profile:

```bash
curl -X POST localhost:8080/v1/events -H 'Content-Type: application/json' -d '{
  "event_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "user_id": "u_42",
  "name": "add_to_cart",
  "occurred_at": "2026-11-06T06:30:00Z",
  "properties": {"product_id": "p_1001", "price": 1299.90},
  "user_attributes": {"city": "Istanbul"}
}'
curl localhost:8080/v1/users/u_42
```

For development (Go 1.27+, Docker, `make`): `make db-up` starts PostgreSQL, `make run` starts the API on the host,
`make test` runs every test. `make help` lists all commands.

## API

Full description: [api/openapi.yaml](api/openapi.yaml) (OpenAPI 3.1, also served at `GET /openapi.yaml`).

| Method and path | What it does |
|-----------------|--------------|
| `POST /v1/events` | track one event: `201 created`, or `200 duplicate` for an `event_id` seen before |
| `POST /v1/events/batch` | track up to 500 events in one transaction; any invalid event rejects the whole batch |
| `GET /v1/users/{id}` | profile: attributes, event counts, purchase stats, 10 latest events |
| `POST /v1/segments` | save an audience rule (`409` if the name is taken) |
| `GET /v1/segments`, `GET /v1/segments/{id}` | list, read |
| `PUT /v1/segments/{id}`, `DELETE /v1/segments/{id}` | replace, delete |
| `GET /v1/segments/{id}/users?limit=&cursor=` | matching users, one page at a time |
| `POST /v1/segments/preview` | how many users would these rules match, without saving |
| `GET /health` | `503` when the database is down |

A segment rule:

```json
{"match": "all", "conditions": [
  {"type": "event", "event": "add_to_cart", "within_days": 7},
  {"type": "event", "event": "purchase", "within_days": 7, "max_count": 0},
  {"type": "attribute", "attribute": "city", "op": "eq", "value": "Istanbul"}
]}
```

Every error has the same shape, and validation errors list every problem at once:

```json
{"error": {"code": "validation_failed", "message": "Some fields are invalid. See details.",
  "details": [{"field": "events[3].name", "problem": "must be snake_case ..."}]}}
```

## Design decisions

**Idempotent ingestion.** The client chooses `event_id` (a UUID). A unique constraint and
`INSERT ... ON CONFLICT DO NOTHING` store it once; a retry after a timeout gets `200 duplicate`.

**All or nothing.** A batch is validated completely before anything is written, and written in one transaction.
With partial success, clients would have to work out which events to resend.

**No deadlocks between concurrent batches.** Each batch updates many user rows. Rows are written in key order,
so two transactions never wait on each other in a circle. A test sends the same events from 8 goroutines in shuffled
order. It passes every time; with sorting removed it failed with `deadlock detected` 3 out of 3 runs.

**Exact numbers.** JSON is decoded with `UseNumber`, so an order id `9007199254740993` is not rounded by `float64`,
and revenue is summed as PostgreSQL `numeric`.

**Rules become one query.** [internal/segments/sql.go](internal/segments/sql.go) turns the rule above into:

```sql
WITH ec AS (                                   -- one row per user, one count per event condition
    SELECT user_id,
           count(*) FILTER (WHERE name = $1 AND occurred_at >= $2) AS c0,
           count(*) FILTER (WHERE name = $4 AND occurred_at >= $5) AS c1
    FROM events
    WHERE (name = $1 AND occurred_at >= $2) OR (name = $4 AND occurred_at >= $5)
    GROUP BY user_id
)
SELECT u.id, u.last_seen_at
FROM users u LEFT JOIN ec ON ec.user_id = u.id  -- users with no matching events count as 0
WHERE ((COALESCE(ec.c0, 0) >= $3) AND (COALESCE(ec.c1, 0) <= $6) AND (u.attributes @> $7::jsonb))
  AND u.id > $8                                 -- keyset pagination instead of OFFSET
ORDER BY u.id
LIMIT $9
```

The events table is read once however many conditions there are. Every value is a parameter, never part of the
SQL text; a test checks that hostile input such as `'; DROP TABLE users; --` stays out of it.

**Strict input.** Body size limits (64 KB, 1 MB for batches), unknown fields rejected (`occured_at` is reported,
not ignored), timestamps more than 5 minutes in the future rejected. Database errors are logged, never returned.

**Operations.** Structured JSON logs with a request id per request, `503` health check, graceful shutdown on `SIGTERM`,
timestamps always returned in UTC, a 22 MB container image that runs as a non-root user.

## Performance

Measured on 50,000 users and 1,000,000 generated events, before and after the indexes of migration 2
([full report](docs/performance.md)):

| | Before | After |
|---|---:|---:|
| User profile (4 queries, p50) | 89–111 ms | 1.7 ms |
| Segment count: cart abandoners in Istanbul (p50) | 37–42 ms | 15–16 ms |
| Delete 2,000 users (cascades to their events) | 112–115 s | 37–44 ms |

The report also covers an index that made a query slower until it became a covering index, a GIN index that was
measured and left out, and what the indexes cost. Reproduce with `make seed` and `make bench`.

## Tests

`make test` runs 38 test functions with 76 subtests: unit tests for validation and the SQL compiler,
and tests against a real PostgreSQL for storage, HTTP flows, migrations (up, down, up) and concurrency.

GitHub Actions runs `gofmt`, `go vet`, staticcheck and all tests with the race detector, then builds the image,
starts the stack with Docker Compose and sends a real event through it.
A test also checks that `api/openapi.yaml` documents exactly the routes the server has.

## Configuration

| Variable | Default |
|----------|---------|
| `HTTP_ADDR` | `:8080` |
| `DATABASE_URL` | `postgres://cdp:cdp@localhost:5544/cdp?sslmode=disable` |
| `LOG_LEVEL` | `info` (`debug`, `info`, `warn`, `error`) |
| `SHUTDOWN_TIMEOUT` | `10s` |

## Project layout

```
cmd/api/              entry point: config, database, HTTP server, graceful shutdown
cmd/seed/             loads generated data with COPY (make seed)
cmd/bench/            EXPLAIN ANALYZE and timings of the API's queries (make bench)
api/                  OpenAPI document, embedded in the binary
internal/httpapi/     routes, handlers, strict JSON decoding, middleware
internal/events/      event model, validation, per-user merge of a batch
internal/segments/    segment rules, validation, rules-to-SQL compiler
internal/store/       all SQL: idempotent inserts, profiles, segments
internal/db/          connection pool, embedded migrations (goose)
internal/config/      settings from environment variables
internal/validate/    field-level validation errors
internal/seed/        repeatable fake shop data
internal/testdb/      test helper: migrated, empty test database
docs/                 performance report and plans, AI-assisted development notes
```

## Tech choices

- **Go standard library** for HTTP routing (method and path patterns since Go 1.22), logging (`log/slog`) and UUIDs.
  No web framework.
- **pgx** for PostgreSQL: connection pool, batches, `COPY`.
- **goose** for versioned SQL migrations, embedded in the binary.
- Hand-written SQL instead of an ORM: the segment query is dynamic, and every query can be read and measured as it runs.

## Limitations

- No authentication or multi-tenancy: one shop, trusted callers.
- Segment membership is computed on every request. Large audiences would need precomputed membership.
- Events are written straight to PostgreSQL. At high volume a queue (for example Kafka) in front of the database
  would absorb traffic spikes.

## How this was built

I built this with an AI coding assistant and reviewed its output before each commit.
What it got wrong, how each mistake was caught, and which claims were checked by experiment is listed in
[docs/ai-assisted-development.md](docs/ai-assisted-development.md).

---

Sura Karaali · [LinkedIn](https://www.linkedin.com/in/surakaraali/)
