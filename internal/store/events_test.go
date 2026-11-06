package store

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/surakaraali/mini-cdp/internal/events"
	"github.com/surakaraali/mini-cdp/internal/testdb"
)

var base = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

func newEvent(userID, name string, at time.Time) events.Event {
	return events.Event{EventID: uuid.New().String(), UserID: userID, Name: name, OccurredAt: at}
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestSaveEventsSkipsDuplicates(t *testing.T) {
	pool := testdb.New(t)
	s := New(pool)
	ctx := context.Background()

	first := newEvent("u_1", "page_view", base)
	second := newEvent("u_1", "add_to_cart", base.Add(time.Minute))

	res, err := s.SaveEvents(ctx, []events.Event{first, second})
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	if res.Created != 2 || len(res.DuplicateIDs) != 0 {
		t.Errorf("first save = %+v, want 2 created and no duplicates", res)
	}

	// A retry of `second` plus one new event; `third` is also sent twice in the same request.
	third := newEvent("u_1", "purchase", base.Add(2*time.Minute))
	res, err = s.SaveEvents(ctx, []events.Event{second, third, third})
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if res.Created != 1 || len(res.DuplicateIDs) != 2 {
		t.Errorf("second save = %+v, want 1 created and 2 duplicates", res)
	}

	if got := countRows(t, pool, "events"); got != 3 {
		t.Errorf("events in table = %d, want 3", got)
	}
}

func TestSaveEventsUpdatesUser(t *testing.T) {
	pool := testdb.New(t)
	s := New(pool)
	ctx := context.Background()

	e1 := newEvent("u_1", "page_view", base.Add(time.Hour))
	e1.UserAttributes = map[string]any{"city": "Istanbul", "plan": "free"}
	// Arrives later but happened earlier (for example, a phone that was offline).
	e2 := newEvent("u_1", "page_view", base)
	e2.UserAttributes = map[string]any{"plan": "pro"}

	for _, e := range []events.Event{e1, e2} {
		if _, err := s.SaveEvents(ctx, []events.Event{e}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	var (
		attrs               map[string]any
		firstSeen, lastSeen time.Time
	)
	err := pool.QueryRow(ctx, "SELECT attributes, first_seen_at, last_seen_at FROM users WHERE id = 'u_1'").
		Scan(&attrs, &firstSeen, &lastSeen)
	if err != nil {
		t.Fatalf("read user: %v", err)
	}

	// Attributes: the last request wins per key, other keys are kept.
	if want := map[string]any{"city": "Istanbul", "plan": "pro"}; !reflect.DeepEqual(attrs, want) {
		t.Errorf("attributes = %v, want %v", attrs, want)
	}
	// Seen times follow when events happened, not when they arrived.
	if !firstSeen.Equal(base) || !lastSeen.Equal(base.Add(time.Hour)) {
		t.Errorf("first/last seen = %v / %v, want %v / %v", firstSeen, lastSeen, base, base.Add(time.Hour))
	}
}

func TestSaveEventsIsAllOrNothing(t *testing.T) {
	pool := testdb.New(t)
	s := New(pool)

	good := newEvent("u_1", "page_view", base)
	// Validation would normally stop this name; here it reaches the database CHECK constraint on purpose.
	bad := newEvent("u_2", "Not A Valid Name", base)

	if _, err := s.SaveEvents(context.Background(), []events.Event{good, bad}); err == nil {
		t.Fatal("SaveEvents succeeded, want an error from the CHECK constraint")
	}

	// The transaction was rolled back, so the good event and both users are gone too.
	if n := countRows(t, pool, "events"); n != 0 {
		t.Errorf("events = %d, want 0", n)
	}
	if n := countRows(t, pool, "users"); n != 0 {
		t.Errorf("users = %d, want 0", n)
	}
}

// TestSaveEventsConcurrentOverlappingBatches sends the same events from 8 goroutines at once,
// each in a different order, like several app servers retrying the same requests.
// Without sorting rows before writing, transactions lock rows in different orders and
// PostgreSQL aborts some of them with "deadlock detected".
func TestSaveEventsConcurrentOverlappingBatches(t *testing.T) {
	pool := testdb.New(t)
	s := New(pool)

	const workers, users, perUser = 8, 20, 10
	var all []events.Event
	for u := range users {
		for i := range perUser {
			all = append(all, newEvent(fmt.Sprintf("u_%02d", u), "page_view", base.Add(time.Duration(i)*time.Minute)))
		}
	}

	var (
		mu         sync.Mutex
		created    int
		duplicates int
		errs       []error
		wg         sync.WaitGroup
	)
	for range workers {
		batch := append([]events.Event(nil), all...)
		rand.Shuffle(len(batch), func(i, j int) { batch[i], batch[j] = batch[j], batch[i] })

		wg.Go(func() {
			res, err := s.SaveEvents(context.Background(), batch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			created += res.Created
			duplicates += len(res.DuplicateIDs)
		})
	}
	wg.Wait()

	for _, err := range errs {
		t.Errorf("SaveEvents: %v", err)
	}
	if created != len(all) {
		t.Errorf("created = %d, want %d (each event exactly once)", created, len(all))
	}
	if want := (workers - 1) * len(all); duplicates != want {
		t.Errorf("duplicates = %d, want %d", duplicates, want)
	}
	if n := countRows(t, pool, "events"); n != len(all) {
		t.Errorf("events in table = %d, want %d", n, len(all))
	}
}
