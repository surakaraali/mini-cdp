// Package db opens the PostgreSQL connection pool and applies schema migrations.
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// The SQL files are compiled into the binary, so the service needs no extra files at runtime.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// Connect opens a connection pool and checks that the database answers.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	// Return every timestamptz in UTC. By default pgx uses the server's local time zone,
	// so the same API would answer "+03:00" on a laptop in Istanbul and "Z" in Docker.
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{
			Name:  "timestamptz",
			OID:   pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies every migration that has not run yet and returns the versions it applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	migrator, err := newMigrator(pool)
	if err != nil {
		return nil, err
	}
	defer migrator.Close()

	results, err := migrator.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	applied := make([]int64, 0, len(results))
	for _, r := range results {
		applied = append(applied, r.Source.Version)
	}
	return applied, nil
}

// MigrateTo moves the schema up or down to exactly version. The benchmark uses it to measure
// the same data with and without the indexes of a later migration.
func MigrateTo(ctx context.Context, pool *pgxpool.Pool, version int64) error {
	migrator, err := newMigrator(pool)
	if err != nil {
		return err
	}
	defer migrator.Close()

	current, err := migrator.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version >= current {
		_, err = migrator.UpTo(ctx, version)
	} else {
		_, err = migrator.DownTo(ctx, version)
	}
	if err != nil {
		return fmt.Errorf("migrate to version %d: %w", version, err)
	}
	return nil
}

// newMigrator builds a goose provider that reads the embedded SQL files.
// goose works with database/sql, so the pgx pool is wrapped in a *sql.DB.
// Closing that wrapper does not close the pool.
func newMigrator(pool *pgxpool.Pool) (*goose.Provider, error) {
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migration files: %w", err)
	}

	migrator, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), files)
	if err != nil {
		return nil, fmt.Errorf("create migrator: %w", err)
	}
	return migrator, nil
}
