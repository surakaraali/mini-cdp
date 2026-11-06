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
| 3 → 5 | **A wrong correction.** In step 3 the AI claimed the new UTC test would pass on a CI runner in UTC even without the fix, called it "a guard that guards nothing", and changed the test to override the global `time.Local`. That claim was never tested. | In step 5 the race detector (`go test -race`) flagged the override as a data race with pgx's background goroutines. An experiment then showed the original test already failed on a UTC machine without the fix: Go's `time.Local` is a different `*Location` than `time.UTC` even when the zone is UTC. | Removed the override and kept the original check, with a clearer failure message. Lesson: a claim about a test is checked by experiment, including the AI's own corrections. |
| 4 | Wrote a shell experiment that stored a command in a variable. zsh does not split such a variable into words, so every `psql` call failed, the GIN index was never created, and the script still printed "dropped" and a write timing labelled "with GIN". | Reading the output: empty timings and "command not found" lines | Rewrote it with a shell function and added checks that print whether the index exists before and after. |
| 4 | The performance report quoted a plan excerpt from an earlier run, while saying the linked plan files come from the final run. The numbers were close, but not the ones in the files. | Checking the report against the plan files | Replaced the excerpt with the one from the linked file and labelled the one that comes from an experiment run. |
| 5 | Wrote the number of tests into the README ("35 test functions, 80 cases") without counting them. The real numbers were 38 and 76. | Counting the passing tests before publishing the README | Corrected the README; numbers in docs are now taken from a test run. |

## Verified, not assumed

Some design choices are easy to claim and hard to prove. These were checked by experiment:

- **Sorted writes prevent deadlocks (step 2).** The store sorts users and events by key before writing.
  `TestSaveEventsConcurrentOverlappingBatches` sends the same 200 events from 8 goroutines in shuffled order.
  With sorting it passes every time; with the two sort lines removed (in a throwaway copy) it failed
  3 out of 3 runs with `ERROR: deadlock detected (SQLSTATE 40P01)`.
- **Indexes were chosen from measurements (step 4).** Before writing the index migration, `cmd/bench` measured
  every API query on 1M events. The first index version made one query slower (63 ms to 121 ms); the plan showed why,
  and a covering index fixed it (41 ms). A GIN index was measured and left out. Details in [performance.md](performance.md).
- **Missing foreign key index (step 4).** The suspicion that cascading deletes scan the events table was turned into
  a number before acting on it: deleting 2,000 users took 112 s without an index on `events.user_id` and 37 ms with it.
- **The OpenAPI drift test catches drift (step 5).** `TestSpecMatchesRoutes` compares the server's routes with
  `api/openapi.yaml`. In a throwaway copy, renaming the `delete` operation in the spec made it fail with
  `DELETE /v1/segments/{id} is served but not documented`. The spec also renders in Swagger UI with 12 operations and no errors.
