# Performance

The first schema (migration 1) had no indexes besides the primary keys and the unique `event_id`.
I loaded 1 million events, measured every query the API runs, read the plans, added indexes
(migration 2) and measured again.

**Summary**

- A user profile went from **89–111 ms to 1.7 ms** (p50 of the whole store call, 4 queries).
- Segment counts got **2–3× faster** (37–42 ms to 15–16 ms).
- Deleting 2,000 users went from **112–115 seconds to 37–44 ms**, because PostgreSQL does not index foreign key columns on its own.
- My first index made one segment query **slower** than having no index (63 ms to 121 ms). Adding `INCLUDE (user_id)` fixed it (41 ms).
- A GIN index on user attributes was measured and **left out**: about 2–5 ms gain on 50k users.

## Setup

| | |
|---|---|
| Machine | MacBook Pro, Apple M1, 8 GB RAM, macOS 15.6 |
| Database | PostgreSQL 17.11 (`postgres:17-alpine`) in Docker, via a Colima VM with 4 CPUs and 6 GB RAM |
| Settings | defaults: `shared_buffers` 128 MB, `work_mem` 4 MB, `random_page_cost` 4, 2 parallel workers per query |
| Data | 50,000 users, 1,000,000 events over the last 90 days (`make seed`, seed 42) |
| Sizes | `events` table 141 MB; the whole dataset fits in memory, so these are warm-cache numbers |

The generator (`internal/seed`) makes the data uneven, like real traffic:

- 1% of users produce 20% of events.
- Events are 55% `page_view`, 25% `product_view`, 13% `add_to_cart` and 7% `purchase`.
- 40% of users live in Istanbul and 10% have no city.
- Rows are loaded in time order with `COPY`, about 30 s for 1M rows. Then `VACUUM (ANALYZE)` runs.

## Method

- `cmd/bench` builds each query with the **same code the API uses** (`store.Query*` constants and the segment compiler),
  so the measured SQL is the SQL that runs in production.
- For each query: one warm-up run, then 7 runs of `EXPLAIN (ANALYZE, BUFFERS)`. The table shows the median
  "Execution Time", which is server time only, without the network.
- Store calls (`GetProfile`, `CountSegment`, `SaveEvents`) are timed from Go: 50 calls (10 for writes),
  p50 and p95, including the round trips to Docker.
- `before` moves the schema down to migration 1, `after` applies migration 2. I ran **two rounds**
  (before, after, before, after) and report both values as a range.
- Noise is about ±5 ms on this laptop. Query 8 uses no index and the same plan in both runs, yet moved from 13 ms to 7 ms.
  Treat small differences as noise. The numbers are for comparing before and after, not for predicting production latency.
- Full plans: [perf/before](perf/before) and [perf/after](perf/after) (round 2).

## Results

| # | Query | Before | After | Change | Plan before → after |
|---|-------|-------:|------:|-------:|---------------------|
| 1 | Profile: event counts | 29–30 ms | 0.04–0.06 ms | ~600× | Parallel Seq Scan → Bitmap Index Scan on `events_user_id_occurred_at_idx` |
| 2 | Profile: purchase stats | 30–35 ms | 0.03–0.04 ms | ~900× | Parallel Seq Scan → Bitmap Index Scan on `events_user_id_occurred_at_idx` |
| 3 | Profile: 10 latest events | 30–36 ms | 0.03–0.04 ms | ~900× | Parallel Seq Scan → Index Scan on `events_user_id_occurred_at_idx` (already in order, stops after 10 rows) |
| 4 | Segment count: cart abandoners, 7 days | 46–63 ms | 18 ms | ~3× | Parallel Seq Scan → Bitmap Index Scans on `events_name_occurred_at_idx` |
| 5 | Segment count: cart abandoners, 7 days, Istanbul | 41–50 ms | 18 ms | ~2.5× | same as 4 |
| 6 | Segment members: cart abandoners, first page | 31–39 ms | 16–17 ms | ~2× | same as 4 |
| 7 | Segment count: never purchased (all time) | 97–107 ms | 41 ms | ~2.5× | Seq Scan → **Index Only Scan** on `events_name_occurred_at_idx` |
| 8 | Segment count: city is Istanbul | 7–13 ms | 7–9 ms | none | Seq Scan on `users` both times (no index involved, the difference is noise) |

| Store call | Before p50 | After p50 | After p95 |
|------------|-----------:|----------:|----------:|
| `GetProfile` (4 queries) | 89–111 ms | 1.7 ms | 2.0–3.9 ms |
| `CountSegment` (cart abandoners, Istanbul) | 37–42 ms | 15–16 ms | 18–20 ms |
| `SaveEvents` (batch of 500) | 35–42 ms | 42–71 ms | 64–112 ms |
| Delete 2,000 users (cascades to 5,000 events) | 112–115 s | 37–44 ms | |

## What the plans showed

**Profile queries read the whole table to find about 20 rows.** Before, each one was a parallel sequential scan:

```
Parallel Seq Scan on events  (actual time=2.090..24.902 rows=7 loops=3)
  Filter: (user_id = 'u_025000'::text)
  Rows Removed by Filter: 333326
```

(Three workers each threw away about 333,000 rows to find 7 matches, 21 in total.)

An index on `(user_id, occurred_at DESC)` serves all three profile queries. "10 latest events" can read the index
in order and stop after 10 entries, with no sort.

**Deleting users was the biggest surprise.** `events.user_id` references `users(id)` with `ON DELETE CASCADE`.
PostgreSQL indexes the referenced primary key, not the referencing column. So for every deleted user it scanned
the whole events table to find that user's events: 2,000 users took close to two minutes.
The same `user_id` index brings it down to about 40 ms.

**Segments need a time range per event name.** The CTE filters on `name = ... AND occurred_at >= ...`.
An index on `(name, occurred_at)` turns that into index range scans. The two conditions are joined with `OR`,
so PostgreSQL combines two bitmap index scans (`BitmapOr`) and then visits the table.

## The index that made a query slower

My first version of migration 2 used `(name, occurred_at)` without `INCLUDE`. Queries 1–6 improved,
but query 7 ("never purchased, all time") went **from 63 ms to 121 ms** in the same run.

The plan explains why. Purchases are 7% of the rows (70,091), spread over the whole time range.
The planner picked the new index, then fetched the matching rows from the table
(excerpt from that experiment run; its plan file was replaced by the final runs):

```
Bitmap Heap Scan on events  (actual time=4.937..129.397 rows=70091 loops=1)
  Heap Blocks: exact=17292
  ->  Bitmap Index Scan on events_name_occurred_at_idx  (rows=70091)
```

It read 17,292 table pages, about 96% of the table, in a single process. Before, PostgreSQL read the same pages
with a sequential scan, which is faster.

The query only needs `name`, `occurred_at` and `user_id`. With `INCLUDE (user_id)` the index holds all three,
so PostgreSQL answers from the index alone (`Index Only Scan`) and does not touch the table: **41 ms**.
Index-only scans depend on the visibility map, which `VACUUM` (or autovacuum) keeps up to date.

**Lesson: an index is not automatically an improvement. Measure every query it might change, not only the one it was made for.**

## Considered and not added

**GIN index on `users.attributes`** (`jsonb_path_ops`, serves `@>`):

| Query | Without | With GIN |
|-------|--------:|---------:|
| city is Istanbul (40% of users) | 7.7 ms | 5.7 ms |
| city is Konya (5% of users) | 6.2 ms | 1.4 ms |

The gain is a few milliseconds on a 50,000-row table. Mixed segments spend most of their time in the events CTE.
Every user row is updated on every incoming event, and an index on `attributes` adds work to those updates.
My write benchmark only creates new users, so it could not show that cost; I did not want to claim numbers I had not measured.
Decision: leave it out, and add it when the users table is large enough for the sequential scan to matter.

## Costs

- **Disk:** the two indexes take 96 MB (39 MB + 57 MB) next to a 141 MB table. `INCLUDE (user_id)` makes the second one bigger.
- **Writes:** every insert updates two more indexes. The `SaveEvents` timings (35–42 ms before, 42–71 ms after)
  vary too much between runs to put a number on it. A longer write benchmark is needed.
- **Building:** migration 2 uses `CREATE INDEX CONCURRENTLY`, so building it on a live table does not block inserts.
  It cannot run in a transaction (`-- +goose NO TRANSACTION`).

## Next steps

- **Segment queries with `OR`** use `BitmapOr` and still visit the table (18 ms). Rewriting the CTE as a `UNION ALL`
  of one range scan per condition could allow index-only scans there too.
- **Precompute membership** for large audiences instead of computing it on every request.
- **Partition `events` by month** once there is a retention policy: old months can be dropped instantly,
  and time-range queries skip whole partitions.
- **Tune for SSDs:** `random_page_cost` is still the default of 4, which was chosen for spinning disks.

## Reproduce

```bash
make db-up
make seed    # 50k users, 1M events into cdp_bench (~30 s)
make bench   # before (about 2 minutes, mostly the slow deletes) and after; plans go to docs/perf/
```
