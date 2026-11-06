// Command seed fills a database with fake shop data for performance measurements.
//
//	DATABASE_URL=postgres://.../cdp_bench go run ./cmd/seed -reset
//
// It refuses to touch a database that already has events unless -reset is given.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/surakaraali/mini-cdp/internal/config"
	"github.com/surakaraali/mini-cdp/internal/db"
	"github.com/surakaraali/mini-cdp/internal/seed"
)

func main() {
	var cfg seed.Config
	flag.IntVar(&cfg.Users, "users", 50_000, "number of users")
	flag.IntVar(&cfg.Events, "events", 1_000_000, "number of events")
	flag.IntVar(&cfg.Days, "days", 90, "spread events over the last N days")
	flag.Uint64Var(&cfg.Seed, "seed", 42, "random seed (same seed, same data)")
	reset := flag.Bool("reset", false, "delete all existing users, events and segments first")
	flag.Parse()

	if err := run(cfg, *reset); err != nil {
		log.Fatal(err)
	}
}

func run(cfg seed.Config, reset bool) error {
	appCfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, appCfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	log.Printf("target database: %s", pool.Config().ConnConfig.Database)

	if _, err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	if err := prepare(ctx, pool, reset); err != nil {
		return err
	}

	start := time.Now()
	data := seed.Generate(cfg, time.Now())
	log.Printf("generated %d users and %d events in %s", len(data.Users), len(data.Events), time.Since(start).Round(time.Millisecond))

	start = time.Now()
	if err := load(ctx, pool, data); err != nil {
		return err
	}
	log.Printf("loaded with COPY in %s", time.Since(start).Round(time.Millisecond))

	// VACUUM updates the visibility map (needed for index-only scans), ANALYZE refreshes the
	// statistics the planner uses to choose between plans. Both matter for fair measurements.
	start = time.Now()
	if _, err := pool.Exec(ctx, "VACUUM (ANALYZE) users, events"); err != nil {
		return fmt.Errorf("vacuum analyze: %w", err)
	}
	log.Printf("vacuum analyze in %s", time.Since(start).Round(time.Millisecond))
	return nil
}

// prepare empties the tables when reset is set, and otherwise refuses to add to existing data.
func prepare(ctx context.Context, pool *pgxpool.Pool, reset bool) error {
	if reset {
		_, err := pool.Exec(ctx, "TRUNCATE users, events, segments RESTART IDENTITY CASCADE")
		return err
	}
	var hasEvents bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM events)").Scan(&hasEvents); err != nil {
		return err
	}
	if hasEvents {
		return errors.New("the database already has events; run with -reset to replace them")
	}
	return nil
}

// load writes users, then events, with COPY in one transaction.
// COPY streams rows in a compact format instead of sending one INSERT per row,
// which makes loading a million rows take seconds instead of minutes.
func load(ctx context.Context, pool *pgxpool.Pool, data seed.Dataset) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		i := 0
		users := pgx.CopyFromFunc(func() ([]any, error) {
			if i == len(data.Users) {
				return nil, nil // end of data
			}
			u := data.Users[i]
			i++
			return []any{u.ID, json.RawMessage(u.AttributesJSON()), u.FirstSeen, u.LastSeen}, nil
		})
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"users"},
			[]string{"id", "attributes", "first_seen_at", "last_seen_at"}, users); err != nil {
			return fmt.Errorf("copy users: %w", err)
		}

		j := 0
		events := pgx.CopyFromFunc(func() ([]any, error) {
			if j == len(data.Events) {
				return nil, nil
			}
			e := data.Events[j]
			row := []any{
				uuid.New(),
				data.Users[e.User].ID,
				seed.EventNames[e.Name],
				json.RawMessage(e.PropertiesJSON(j)),
				e.At,
			}
			j++
			return row, nil
		})
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"events"},
			[]string{"event_id", "user_id", "name", "properties", "occurred_at"}, events); err != nil {
			return fmt.Errorf("copy events: %w", err)
		}
		return nil
	})
}
