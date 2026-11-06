// Package store reads and writes the service's data in PostgreSQL.
package store

import "github.com/jackc/pgx/v5/pgxpool"

// Store wraps the connection pool. All SQL lives in this package.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a Store.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}
