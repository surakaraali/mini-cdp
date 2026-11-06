package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testDatabaseURL returns the test database address, or skips the test when it is not set.
// Other packages use internal/testdb instead; this package cannot, because testdb imports db.
// `make test` sets it; `make test-unit` leaves it empty so only the fast tests run.
// A skipped test still prints "ok", so in CI (where CI=true) a missing URL fails instead.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL must be set in CI")
		}
		t.Skip("TEST_DATABASE_URL is not set; skipping database test")
	}
	return url
}

// TestMigrationsUpDownUp checks that every migration can be applied, rolled back and applied again.
// A broken Down section would otherwise only be found during a real rollback.
func TestMigrationsUpDownUp(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, testDatabaseURL(t))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	assertTables(t, pool, true)

	migrator, err := newMigrator(pool)
	if err != nil {
		t.Fatalf("newMigrator: %v", err)
	}
	defer migrator.Close()
	if _, err := migrator.DownTo(ctx, 0); err != nil {
		t.Fatalf("DownTo(0): %v", err)
	}
	assertTables(t, pool, false)

	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	assertTables(t, pool, true)
}

// assertTables checks whether the three tables exist.
func assertTables(t *testing.T, pool *pgxpool.Pool, wantExist bool) {
	t.Helper()
	for _, table := range []string{"users", "events", "segments"} {
		var exists bool
		err := pool.QueryRow(context.Background(), "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if exists != wantExist {
			t.Errorf("table %s exists = %v, want %v", table, exists, wantExist)
		}
	}
}

// TestTimestampsComeBackInUTC makes API responses look the same on every server:
// without it, timestamps follow the server's time zone (+03:00 on a laptop in Istanbul, Z in Docker).
//
// The check compares the *Location pointer with time.UTC. Without the fix pgx returns time.Local,
// which is a different pointer even on a machine whose zone is UTC, so the test fails there too.
// (An earlier version also overrode time.Local; the race detector flagged that as a data race
// with pgx's background goroutines, and an experiment showed the override was not needed.)
func TestTimestampsComeBackInUTC(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, testDatabaseURL(t))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	var got time.Time
	if err := pool.QueryRow(ctx, "SELECT now()").Scan(&got); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got.Location() != time.UTC {
		t.Errorf("timestamps come back in the server's local zone (%q), want time.UTC", got.Location())
	}
}
