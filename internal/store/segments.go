package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/surakaraali/mini-cdp/internal/segments"
)

const segmentColumns = `id, name, description, rules, created_at, updated_at`

// scanSegment reads one segment row. rules is scanned as raw bytes and decoded with exact numbers.
func scanSegment(row pgx.Row) (segments.Segment, error) {
	var (
		seg   segments.Segment
		rules []byte
	)
	err := row.Scan(&seg.ID, &seg.Name, &seg.Description, &rules, &seg.CreatedAt, &seg.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return segments.Segment{}, ErrNotFound
	}
	if err != nil {
		return segments.Segment{}, err
	}
	if err := decodeJSONB(rules, &seg.Rules); err != nil {
		return segments.Segment{}, fmt.Errorf("decode rules of segment %d: %w", seg.ID, err)
	}
	return seg, nil
}

// CreateSegment saves a new segment. A taken name returns ErrConflict.
func (s *Store) CreateSegment(ctx context.Context, in segments.Input) (segments.Segment, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO segments (name, description, rules) VALUES ($1, $2, $3) RETURNING `+segmentColumns,
		in.Name, in.Description, in.Rules)
	seg, err := scanSegment(row)
	if isUniqueViolation(err) {
		return segments.Segment{}, ErrConflict
	}
	if err != nil {
		return segments.Segment{}, fmt.Errorf("create segment: %w", err)
	}
	return seg, nil
}

// ListSegments returns every segment, oldest first.
func (s *Store) ListSegments(ctx context.Context) ([]segments.Segment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+segmentColumns+` FROM segments ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list segments: %w", err)
	}
	defer rows.Close()

	list := []segments.Segment{}
	for rows.Next() {
		seg, err := scanSegment(rows)
		if err != nil {
			return nil, fmt.Errorf("list segments: %w", err)
		}
		list = append(list, seg)
	}
	return list, rows.Err()
}

// GetSegment returns one segment, or ErrNotFound.
func (s *Store) GetSegment(ctx context.Context, id int64) (segments.Segment, error) {
	seg, err := scanSegment(s.pool.QueryRow(ctx, `SELECT `+segmentColumns+` FROM segments WHERE id = $1`, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return segments.Segment{}, fmt.Errorf("get segment: %w", err)
	}
	return seg, err
}

// UpdateSegment replaces a segment's name, description and rules.
func (s *Store) UpdateSegment(ctx context.Context, id int64, in segments.Input) (segments.Segment, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE segments SET name = $2, description = $3, rules = $4, updated_at = now()
		WHERE id = $1
		RETURNING `+segmentColumns,
		id, in.Name, in.Description, in.Rules)
	seg, err := scanSegment(row)
	switch {
	case isUniqueViolation(err):
		return segments.Segment{}, ErrConflict
	case errors.Is(err, ErrNotFound):
		return segments.Segment{}, ErrNotFound
	case err != nil:
		return segments.Segment{}, fmt.Errorf("update segment: %w", err)
	}
	return seg, nil
}

// DeleteSegment removes a segment, or returns ErrNotFound.
func (s *Store) DeleteSegment(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM segments WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete segment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SegmentMembers returns up to limit users that match rules, with ids greater than afterUserID.
func (s *Store) SegmentMembers(ctx context.Context, rules segments.Rules, now time.Time, afterUserID string, limit int) ([]segments.Member, error) {
	q := rules.MembersQuery(now, afterUserID, limit)
	rows, err := s.pool.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, fmt.Errorf("query segment members: %w", err)
	}
	defer rows.Close()

	members := []segments.Member{}
	for rows.Next() {
		var m segments.Member
		if err := rows.Scan(&m.UserID, &m.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan segment member: %w", err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// CountSegment returns how many users match rules.
func (s *Store) CountSegment(ctx context.Context, rules segments.Rules, now time.Time) (int64, error) {
	q := rules.CountQuery(now)
	var n int64
	if err := s.pool.QueryRow(ctx, q.SQL, q.Args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count segment: %w", err)
	}
	return n, nil
}
