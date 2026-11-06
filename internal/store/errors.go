package store

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Errors the HTTP layer turns into status codes, so it never has to inspect database errors itself.
var (
	ErrNotFound = errors.New("not found") // 404
	ErrConflict = errors.New("conflict")  // 409, e.g. a segment name that is already taken
)

// isUniqueViolation reports whether err is PostgreSQL error 23505 (unique_violation).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// decodeJSONB reads a JSONB column (scanned as raw bytes) into dst, keeping exact numbers,
// the same way the API decodes request bodies.
func decodeJSONB(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(dst)
}
