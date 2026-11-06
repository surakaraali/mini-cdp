# Mini CDP

A small customer data platform (CDP) backend written in Go.
An online shop sends user events (page views, add to cart, purchases) to the API;
the service stores them in PostgreSQL, builds a profile per user and answers
questions like *"which users added something to their cart in the last 7 days but did not buy?"*

> Work in progress. The full README (API reference, design notes, performance results) comes with the last step.

## Status

- [x] **1. Skeleton**: project layout, PostgreSQL in Docker, schema migrations, `/health`, middleware, tests
- [x] **2. Event ingestion**: `POST /v1/events` and `POST /v1/events/batch`, idempotent by `event_id`
- [x] **3. Profiles and segments**: user profile, segment rules (JSON to parameterized SQL), segment members
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

## API (so far)

### `POST /v1/events`: track one event

```bash
curl -X POST localhost:8080/v1/events -H 'Content-Type: application/json' -d '{
  "event_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "user_id": "u_42",
  "name": "add_to_cart",
  "occurred_at": "2026-11-06T06:30:00Z",
  "properties": {"product_id": "p_1001", "price": 1299.90},
  "user_attributes": {"city": "Istanbul"}
}'
```

| Status | When | Body |
|--------|------|------|
| `201` | new event stored | `{"event_id": "...", "status": "created"}` |
| `200` | same `event_id` sent before (safe retry) | `{"event_id": "...", "status": "duplicate"}` |
| `400` | body is not valid JSON, or has an unknown field | `{"error": {"code": "invalid_json" \| "unknown_field", ...}}` |
| `413` | body over 64 KB | `{"error": {"code": "body_too_large", ...}}` |
| `422` | fields break the rules (every problem is listed) | `{"error": {"code": "validation_failed", "details": [{"field": "name", "problem": "..."}]}}` |

Rules: `event_id` is a UUID chosen by the client; `name` is snake_case (max 64 characters);
`occurred_at` is RFC 3339 and not in the future (5 minutes of clock skew allowed);
`user_attributes` are merged into the user's profile.

### `POST /v1/events/batch`: track up to 500 events

Body: `{"events": [ ...same objects as above... ]}`, at most 1 MB.
Either every event is valid and the batch is stored in one transaction, or nothing is stored and
`422` lists the problems by position (`events[3].name`). Response:

```json
{"received": 2, "created": 1, "duplicates": 1, "duplicate_event_ids": ["7c9e6679-7425-40de-944b-e07fc1f90ae7"]}
```

### `GET /v1/users/{id}`: user profile

Attributes, first and last seen, event counts by name, purchase stats (`revenue` summed from
`purchase` events, exact decimals) and the 10 latest events. `404` if the user is unknown.
All timestamps are returned in UTC.

### Segments

A segment is a saved audience rule. Example: *added to cart in the last 7 days, did not buy, lives in Istanbul*:

```json
{
  "name": "Cart abandoners",
  "rules": {
    "match": "all",
    "conditions": [
      {"type": "event", "event": "add_to_cart", "within_days": 7, "min_count": 1},
      {"type": "event", "event": "purchase", "within_days": 7, "max_count": 0},
      {"type": "attribute", "attribute": "city", "op": "eq", "value": "Istanbul"}
    ]
  }
}
```

- `match`: `all` (AND) or `any` (OR), up to 10 conditions.
- Event condition: `event`, optional `within_days` (1-365), `min_count` / `max_count`. Without counts it means "at least once".
- Attribute condition: `attribute` with `op` `eq`, `neq` or `exists`; `value` is a string, number or boolean.

| Method and path | What it does |
|-----------------|--------------|
| `POST /v1/segments` | create (`201` + `Location`, `409 name_taken` if the name exists) |
| `GET /v1/segments` | list all |
| `GET /v1/segments/{id}` | get one |
| `PUT /v1/segments/{id}` | replace name, description and rules |
| `DELETE /v1/segments/{id}` | delete (`204`) |
| `GET /v1/segments/{id}/users?limit=50&cursor=...` | matching users, one page at a time; pass `next_cursor` to get the next page |
| `POST /v1/segments/preview` | `{"rules": ...}` returns `user_count` and up to 10 sample ids, without saving |

Membership is computed when it is requested, so it always reflects the latest events.

## How segment rules become SQL

`internal/segments/sql.go` compiles the rules into one query. Every value goes in as a parameter (`$1`, `$2`, ...),
never into the SQL text, so rule values cannot inject SQL. The example above becomes:

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
FROM users u LEFT JOIN ec ON ec.user_id = u.id  -- LEFT JOIN: users with no events count as 0
WHERE ((COALESCE(ec.c0, 0) >= $3) AND (COALESCE(ec.c1, 0) <= $6) AND (u.attributes @> $7::jsonb))
  AND u.id > $8                                 -- keyset pagination instead of OFFSET
ORDER BY u.id
LIMIT $9
```

The events table is read once, however many event conditions there are. `max_count: 0` works for users
who have no purchase rows at all, thanks to the `LEFT JOIN` and `COALESCE`.

## Tests

```bash
make test        # all tests; the database tests use the cdp_test database (packages run one at a time)
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
internal/events/      event model, validation rules, per-user merge of a batch
internal/segments/    segment rules, validation, rules-to-SQL compiler
internal/store/       all SQL: idempotent event insert, profiles, segment CRUD and members
internal/httpapi/     routes, handlers, JSON decoding, middleware (request id, logging, panic recovery)
internal/validate/    field-level validation errors
internal/testdb/      test helper: migrated, empty test database
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
