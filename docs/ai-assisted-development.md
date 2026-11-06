# AI-assisted development

I built this project with an AI coding assistant (Claude Code). This page records how I worked with it
and the mistakes I found in its output. Only real cases are listed, in the order they happened.

## How I work with it

1. I describe one small step (for example "event ingestion with idempotency") and the constraints:
   standard library first, few dependencies, code a junior engineer can explain.
2. The assistant proposes code and tests. Before using a library, it checks the real API in the
   downloaded module source instead of guessing from memory.
3. I review every file, run `go vet`, the unit tests and the database tests, and try the API with `curl`.
4. Anything wrong, unclear or more complex than needed goes back for a change before the commit.

## Mistakes caught

| Step | What the AI did | How it was caught | Fix |
|------|-----------------|-------------------|-----|
| 1 | Wrote a test helper with Go generics and a function parameter, where passing the connection pool was enough. It worked, but was hard to read. | Code review before commit | Rewrote the helper to take `*pgxpool.Pool`. |
| 1 | Tried to change the global Docker config (`~/.docker/config.json`) so `docker compose` would work. A project should not change machine-wide settings. | The tool's permission check blocked the edit | The Makefile now uses `docker compose` when available and falls back to `docker-compose`. |
| 1 | Database tests skip when `TEST_DATABASE_URL` is missing, and `go test` still prints `ok` for a skipped test. A CI run without a database would have looked green while testing nothing. | Running the test with `-v` showed `SKIP` | When `CI` is set, a missing URL now fails the test instead of skipping it. |
