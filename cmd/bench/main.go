// Command bench measures the API's queries on the seeded database (see cmd/seed).
//
//	DATABASE_URL=postgres://.../cdp_bench go run ./cmd/bench -schema before -out docs/perf/before
//	DATABASE_URL=postgres://.../cdp_bench go run ./cmd/bench -schema after  -out docs/perf/after
//
// "before" moves the schema to migration 1 (no extra indexes), "after" applies every migration.
// For each query it runs EXPLAIN (ANALYZE, BUFFERS) several times, prints the median execution
// time and saves the full plan. It also times real store calls and a batch write.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/surakaraali/mini-cdp/internal/config"
	"github.com/surakaraali/mini-cdp/internal/db"
	"github.com/surakaraali/mini-cdp/internal/events"
	"github.com/surakaraali/mini-cdp/internal/segments"
	"github.com/surakaraali/mini-cdp/internal/store"
)

type query struct {
	label string
	sql   string
	args  []any
}

func intPtr(n int) *int { return &n }

func main() {
	schema := flag.String("schema", "", `"before" (no extra indexes) or "after" (all migrations)`)
	runs := flag.Int("runs", 7, "measured runs per query (after one warm-up run)")
	out := flag.String("out", "", "directory for the query plans (optional)")
	user := flag.String("user", "u_025000", "user for the profile queries (a typical, non-heavy user)")
	flag.Parse()

	if err := run(*schema, *runs, *out, *user); err != nil {
		log.Fatal(err)
	}
}

func run(schema string, runs int, out, userID string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch schema {
	case "before":
		err = db.MigrateTo(ctx, pool, 1)
	case "after":
		_, err = db.Migrate(ctx, pool)
	default:
		return fmt.Errorf(`-schema must be "before" or "after", got %q`, schema)
	}
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, "ANALYZE users, events"); err != nil {
		return err
	}

	now := time.Now()
	queries := buildQueries(now, userID)

	fmt.Printf("database %s, schema %q, median of %d runs\n\n", pool.Config().ConnConfig.Database, schema, runs)
	fmt.Println("| # | Query | Median | Plan |")
	fmt.Println("|---|-------|-------:|------|")
	for i, q := range queries {
		median, plan, err := explain(ctx, pool, q, runs)
		if err != nil {
			return fmt.Errorf("%s: %w", q.label, err)
		}
		fmt.Printf("| %d | %s | %s | %s |\n", i+1, q.label, formatMs(median), summarize(plan))
		if out != "" {
			if err := savePlan(out, i+1, q, plan); err != nil {
				return err
			}
		}
	}

	fmt.Println()
	return storeTimings(ctx, pool, now, userID)
}

// buildQueries uses the same code as the API to produce the SQL, so we measure what really runs.
func buildQueries(now time.Time, userID string) []query {
	addedToCart := segments.Condition{Type: "event", Event: "add_to_cart", WithinDays: intPtr(7)}
	noPurchase := segments.Condition{Type: "event", Event: "purchase", WithinDays: intPtr(7), MaxCount: intPtr(0)}
	istanbul := segments.Condition{Type: "attribute", Attribute: "city", Op: "eq", Value: "Istanbul"}

	abandoners := segments.Rules{Match: "all", Conditions: []segments.Condition{addedToCart, noPurchase}}
	abandonersIstanbul := segments.Rules{Match: "all", Conditions: []segments.Condition{addedToCart, noPurchase, istanbul}}
	neverBought := segments.Rules{Match: "all", Conditions: []segments.Condition{{Type: "event", Event: "purchase", MaxCount: intPtr(0)}}}
	inIstanbul := segments.Rules{Match: "all", Conditions: []segments.Condition{istanbul}}

	fromQuery := func(label string, q segments.Query) query { return query{label, q.SQL, q.Args} }
	return []query{
		{"Profile: event counts", store.QueryEventCounts, []any{userID}},
		{"Profile: purchase stats", store.QueryPurchaseStats, []any{userID}},
		{"Profile: 10 latest events", store.QueryRecentEvents, []any{userID, 10}},
		fromQuery("Segment count: cart abandoners, 7 days", abandoners.CountQuery(now)),
		fromQuery("Segment count: cart abandoners, 7 days, Istanbul", abandonersIstanbul.CountQuery(now)),
		fromQuery("Segment members: cart abandoners, first page of 50", abandoners.MembersQuery(now, "", 51)),
		fromQuery("Segment count: never purchased (all time)", neverBought.CountQuery(now)),
		fromQuery("Segment count: city is Istanbul", inIstanbul.CountQuery(now)),
	}
}

var executionTime = regexp.MustCompile(`Execution Time: ([0-9.]+) ms`)

// explain runs EXPLAIN ANALYZE once to warm the cache, then runs times, and returns the median
// execution time reported by PostgreSQL (server time only, no network) and the last plan.
func explain(ctx context.Context, pool *pgxpool.Pool, q query, runs int) (time.Duration, string, error) {
	var (
		times []time.Duration
		plan  string
	)
	for i := 0; i <= runs; i++ {
		rows, err := pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+q.sql, q.args...)
		if err != nil {
			return 0, "", err
		}
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				return 0, "", err
			}
			lines = append(lines, line)
		}
		if err := rows.Err(); err != nil {
			return 0, "", err
		}
		plan = strings.Join(lines, "\n")

		m := executionTime.FindStringSubmatch(plan)
		if m == nil {
			return 0, "", fmt.Errorf("no execution time in plan:\n%s", plan)
		}
		ms, _ := strconv.ParseFloat(m[1], 64)
		if i > 0 { // run 0 is the warm-up
			times = append(times, time.Duration(ms*float64(time.Millisecond)))
		}
	}
	return median(times), plan, nil
}

var scanNode = regexp.MustCompile(`((?:Parallel )?(?:Seq Scan|Index Only Scan|Index Scan|Bitmap Index Scan|Bitmap Heap Scan)(?: using \S+)? on \S+|Bitmap Index Scan on \S+)`)

// summarize lists the distinct table and index scans of a plan, e.g. "Seq Scan on events".
func summarize(plan string) string {
	var found []string
	for _, m := range scanNode.FindAllString(plan, -1) {
		if !slices.Contains(found, m) {
			found = append(found, m)
		}
	}
	return strings.Join(found, "; ")
}

func savePlan(dir string, n int, q query, plan string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	slug := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(q.label), "-"), "-")
	var b strings.Builder
	fmt.Fprintf(&b, "-- %s\n-- args: %v\n\n%s\n\n%s\n", q.label, formatArgs(q.args), strings.TrimSpace(q.sql), plan)
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d-%s.txt", n, slug)), []byte(b.String()), 0o644)
}

// formatArgs prints times as dates only, because they change with every run.
func formatArgs(args []any) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if t, ok := a.(time.Time); ok {
			out[i] = "now - " + time.Since(t).Round(24*time.Hour).String()
			continue
		}
		out[i] = fmt.Sprint(a)
	}
	return out
}

// storeTimings measures whole store calls (several queries plus the network round trips to Docker)
// and a 500-event batch write, which every new index makes a little slower.
func storeTimings(ctx context.Context, pool *pgxpool.Pool, now time.Time, userID string) error {
	st := store.New(pool)
	const calls = 50

	profile, err := timeCalls(calls, func() error { _, err := st.GetProfile(ctx, userID); return err })
	if err != nil {
		return err
	}
	rules := segments.Rules{Match: "all", Conditions: []segments.Condition{
		{Type: "event", Event: "add_to_cart", WithinDays: intPtr(7)},
		{Type: "event", Event: "purchase", WithinDays: intPtr(7), MaxCount: intPtr(0)},
		{Type: "attribute", Attribute: "city", Op: "eq", Value: "Istanbul"},
	}}
	segment, err := timeCalls(calls, func() error { _, err := st.CountSegment(ctx, rules, now); return err })
	if err != nil {
		return err
	}

	// Writes go to new users that are deleted afterwards, so the dataset stays the same.
	const batches, batchSize = 10, 500
	n := 0
	write, err := timeCalls(batches, func() error {
		batch := make([]events.Event, batchSize)
		for i := range batch {
			batch[i] = events.Event{
				EventID:    uuid.New().String(),
				UserID:     fmt.Sprintf("bench_w_%04d", (n*batchSize+i)%2000),
				Name:       "page_view",
				OccurredAt: time.Now(),
			}
		}
		n++
		_, err := st.SaveEvents(ctx, batch)
		return err
	})
	if err != nil {
		return err
	}

	// Deleting a user cascades to their events (ON DELETE CASCADE). PostgreSQL does not index
	// foreign key columns automatically, so this shows the cost of finding each user's events.
	start := time.Now()
	tag, err := pool.Exec(ctx, "DELETE FROM users WHERE id LIKE 'bench_w_%'")
	if err != nil {
		return err
	}
	deleteTime := time.Since(start)

	fmt.Println("| Store call | p50 | p95 |")
	fmt.Println("|------------|----:|----:|")
	fmt.Printf("| GetProfile (4 queries) | %s | %s |\n", formatMs(percentile(profile, 50)), formatMs(percentile(profile, 95)))
	fmt.Printf("| CountSegment (cart abandoners, Istanbul) | %s | %s |\n", formatMs(percentile(segment, 50)), formatMs(percentile(segment, 95)))
	fmt.Printf("| SaveEvents (batch of %d) | %s | %s |\n", batchSize, formatMs(percentile(write, 50)), formatMs(percentile(write, 95)))
	fmt.Printf("\nDeleting %d users (cascades to their events): %s\n", tag.RowsAffected(), formatMs(deleteTime))
	return nil
}

func timeCalls(n int, f func() error) ([]time.Duration, error) {
	if err := f(); err != nil { // warm-up
		return nil, err
	}
	times := make([]time.Duration, n)
	for i := range times {
		start := time.Now()
		if err := f(); err != nil {
			return nil, err
		}
		times[i] = time.Since(start)
	}
	return times, nil
}

func median(d []time.Duration) time.Duration { return percentile(d, 50) }

// percentile uses the nearest-rank method on a sorted copy.
func percentile(d []time.Duration, p int) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	idx := (len(s)*p + 99) / 100
	return s[max(0, idx-1)]
}

func formatMs(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)
	if ms < 10 {
		return fmt.Sprintf("%.2f ms", ms)
	}
	return fmt.Sprintf("%.0f ms", ms)
}
