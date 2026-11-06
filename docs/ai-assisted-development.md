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
| 2 | Decoded request bodies with Go's default JSON settings, which turn every number inside `properties` into a `float64`. An order id `9007199254740993` was stored as `9007199254740992`, and `499.90` as `499.9`. All tests were green. | Code review raised the doubt; a new database test reproduced it | `json.Decoder.UseNumber()` keeps the exact digits; the test stays as a guard. |
| 3 | Checked "is this value a number?" by accepting any type with a `String()` method. That is far wider than JSON numbers and hides intent. | Code review right after writing it | Check for `json.Number` (and `float64`) by name. |
| 3 | API timestamps followed the server's time zone: `+03:00` on a laptop in Istanbul, `Z` in Docker. Same instant, but responses differed per machine. Tests passed because they compare instants. | Trying the API with `curl` | pgx now scans `timestamptz` in UTC (`TimestamptzCodec.ScanLocation`), with a test that failed before the fix. |
| 3 | The first version of that test depended on the machine's time zone, so on a CI runner in UTC it would pass even without the fix: a guard that guards nothing. The AI's own summary claimed the test "runs with TZ=Europe/Istanbul", but only the manual command did. | Checking the claim in this file against the test code | The test sets `time.Local` to UTC+3 itself; verified that it fails on a UTC machine when the fix is removed. |

## Verified, not assumed

Some design choices are easy to claim and hard to prove. These were checked by experiment:

- **Sorted writes prevent deadlocks (step 2).** The store sorts users and events by key before writing.
  `TestSaveEventsConcurrentOverlappingBatches` sends the same 200 events from 8 goroutines in shuffled order.
  With sorting it passes every time; with the two sort lines removed (in a throwaway copy) it failed
  3 out of 3 runs with `ERROR: deadlock detected (SQLSTATE 40P01)`.
