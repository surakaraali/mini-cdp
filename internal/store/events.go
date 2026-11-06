package store

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/surakaraali/mini-cdp/internal/events"
)

// upsertUserSQL creates the user, or updates it when it already exists ("upsert").
// - attributes: the new keys are merged into the old ones (|| on JSONB); a new value wins.
// - first/last seen: LEAST/GREATEST keep the right value even if events arrive out of order.
const upsertUserSQL = `
INSERT INTO users (id, attributes, first_seen_at, last_seen_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET
    attributes    = users.attributes || EXCLUDED.attributes,
    first_seen_at = LEAST(users.first_seen_at, EXCLUDED.first_seen_at),
    last_seen_at  = GREATEST(users.last_seen_at, EXCLUDED.last_seen_at),
    updated_at    = now()`

// insertEventSQL stores an event unless one with the same event_id exists.
// It then affects 0 rows, which is how we tell a duplicate from a new event.
const insertEventSQL = `
INSERT INTO events (event_id, user_id, name, properties, occurred_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (event_id) DO NOTHING`

// SaveEvents stores already validated events in one transaction: either all of them are saved or none.
//
// All statements go to the database in one pgx.Batch, so 500 events cost one network round trip,
// not 500. Users are written first because events reference them (foreign key).
// Users and events are both sorted by their key, so concurrent requests lock rows in the same order
// and cannot deadlock.
func (s *Store) SaveEvents(ctx context.Context, evs []events.Event) (events.SaveResult, error) {
	users := events.UserUpdates(evs) // one row per user, sorted by user id

	sorted := slices.Clone(evs)
	slices.SortFunc(sorted, func(a, b events.Event) int { return strings.Compare(a.EventID, b.EventID) })

	batch := &pgx.Batch{}
	for _, u := range users {
		batch.Queue(upsertUserSQL, u.UserID, u.Attributes, u.FirstSeen, u.LastSeen)
	}
	for _, e := range sorted {
		batch.Queue(insertEventSQL, e.EventID, e.UserID, e.Name, nonNil(e.Properties), e.OccurredAt)
	}

	var result events.SaveResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		results := tx.SendBatch(ctx, batch)
		defer results.Close()

		// Results come back in the order the statements were queued.
		for _, u := range users {
			if _, err := results.Exec(); err != nil {
				return fmt.Errorf("upsert user %q: %w", u.UserID, err)
			}
		}
		for _, e := range sorted {
			tag, err := results.Exec()
			if err != nil {
				return fmt.Errorf("insert event %s: %w", e.EventID, err)
			}
			if tag.RowsAffected() == 1 {
				result.Created++
			} else {
				result.DuplicateIDs = append(result.DuplicateIDs, e.EventID)
			}
		}
		// The batch must be closed before BeginFunc commits the transaction.
		return results.Close()
	})
	if err != nil {
		return events.SaveResult{}, fmt.Errorf("save events: %w", err)
	}
	return result, nil
}

// nonNil turns a missing map into an empty one, so the NOT NULL JSONB column gets {} instead of NULL.
func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
