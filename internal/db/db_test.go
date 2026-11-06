package db

import (
	"context"
	"os"
	"testing"

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
