// Package testdb gives tests a migrated, empty PostgreSQL database.
//
// Tests that use it share one database, so `make test` runs packages one at a time (go test -p 1).
package testdb

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/surakaraali/mini-cdp/internal/db"
)

// New connects to TEST_DATABASE_URL, applies the migrations and empties every table.
// Without the variable the test is skipped locally, but fails in CI, where a skip would hide missing coverage.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL must be set in CI")
		}
		t.Skip("TEST_DATABASE_URL is not set; skipping database test")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE users, events, segments RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("empty test database: %v", err)
	}
	return pool
}
