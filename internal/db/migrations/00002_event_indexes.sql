-- +goose NO TRANSACTION

-- Indexes chosen from the measurements in docs/performance.md.
--
-- CONCURRENTLY builds an index without blocking inserts into a table that is in use.
-- PostgreSQL does not allow it inside a transaction, hence NO TRANSACTION above.
-- If a concurrent build fails, it leaves an INVALID index behind: drop it and run the migration again.

-- +goose Up

-- Profile queries: WHERE user_id = $1 [ORDER BY occurred_at DESC].
-- Also used when a user is deleted and PostgreSQL looks up their events (foreign key cascade):
-- foreign key columns get no index automatically.
CREATE INDEX CONCURRENTLY IF NOT EXISTS events_user_id_occurred_at_idx
    ON events (user_id, occurred_at DESC);

-- Segment queries: WHERE (name = $1 AND occurred_at >= $2) OR (name = $3 AND ...).
-- INCLUDE (user_id) stores the user id in the index too, so a query that only needs
-- name, occurred_at and user_id can be answered from the index alone (Index Only Scan).
-- Without it, "never purchased" got slower than with no index at all (see docs/performance.md).
CREATE INDEX CONCURRENTLY IF NOT EXISTS events_name_occurred_at_idx
    ON events (name, occurred_at) INCLUDE (user_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS events_name_occurred_at_idx;
DROP INDEX CONCURRENTLY IF EXISTS events_user_id_occurred_at_idx;
