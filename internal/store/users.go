package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const recentEventsLimit = 10

// The profile queries are exported so cmd/bench measures exactly the SQL the API runs.
const (
	QueryEventCounts = `SELECT name, count(*) FROM events WHERE user_id = $1 GROUP BY name`

	// CASE skips revenue values that are not numbers (e.g. "abc"), instead of failing the whole query on the cast.
	QueryPurchaseStats = `
		SELECT count(*),
		       COALESCE(sum(CASE WHEN jsonb_typeof(properties->'revenue') = 'number'
		                         THEN (properties->>'revenue')::numeric END), 0)::text,
		       max(occurred_at)
		FROM events
		WHERE user_id = $1 AND name = 'purchase'`

	QueryRecentEvents = `
		SELECT event_id::text, name, occurred_at, properties
		FROM events
		WHERE user_id = $1
		ORDER BY occurred_at DESC, id DESC
		LIMIT $2`
)

// Profile is everything we know about one user, ready to be sent as JSON.
// Attributes and properties stay as raw JSON: they pass through untouched, with exact numbers.
type Profile struct {
	UserID       string           `json:"user_id"`
	Attributes   json.RawMessage  `json:"attributes"`
	FirstSeenAt  time.Time        `json:"first_seen_at"`
	LastSeenAt   time.Time        `json:"last_seen_at"`
	TotalEvents  int64            `json:"total_events"`
	EventCounts  map[string]int64 `json:"event_counts"`
	Purchases    PurchaseStats    `json:"purchases"`
	RecentEvents []RecentEvent    `json:"recent_events"`
}

// PurchaseStats summarizes the user's "purchase" events. Revenue is read from properties.revenue.
type PurchaseStats struct {
	Count int64 `json:"count"`
	// json.Number keeps the exact decimal from PostgreSQL's numeric type; money must not go through float64.
	TotalRevenue json.Number `json:"total_revenue"`
	LastAt       *time.Time  `json:"last_at"` // null when the user never bought anything
}

// RecentEvent is one of the user's latest events.
type RecentEvent struct {
	EventID    string          `json:"event_id"`
	Name       string          `json:"name"`
	OccurredAt time.Time       `json:"occurred_at"`
	Properties json.RawMessage `json:"properties"`
}

// GetProfile returns the user's profile, or ErrNotFound.
func (s *Store) GetProfile(ctx context.Context, userID string) (Profile, error) {
	p := Profile{UserID: userID, EventCounts: map[string]int64{}, RecentEvents: []RecentEvent{}}

	err := s.pool.QueryRow(ctx,
		`SELECT attributes, first_seen_at, last_seen_at FROM users WHERE id = $1`, userID).
		Scan(&p.Attributes, &p.FirstSeenAt, &p.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("read user: %w", err)
	}

	if err := s.readEventCounts(ctx, &p); err != nil {
		return Profile{}, err
	}

	err = s.pool.QueryRow(ctx, QueryPurchaseStats, userID).
		Scan(&p.Purchases.Count, &p.Purchases.TotalRevenue, &p.Purchases.LastAt)
	if err != nil {
		return Profile{}, fmt.Errorf("read purchase stats: %w", err)
	}

	if err := s.readRecentEvents(ctx, &p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func (s *Store) readEventCounts(ctx context.Context, p *Profile) error {
	rows, err := s.pool.Query(ctx, QueryEventCounts, p.UserID)
	if err != nil {
		return fmt.Errorf("read event counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			name  string
			count int64
		)
		if err := rows.Scan(&name, &count); err != nil {
			return fmt.Errorf("scan event count: %w", err)
		}
		p.EventCounts[name] = count
		p.TotalEvents += count
	}
	return rows.Err() // an error that happened while reading rows shows up here, not in Next
}

func (s *Store) readRecentEvents(ctx context.Context, p *Profile) error {
	rows, err := s.pool.Query(ctx, QueryRecentEvents, p.UserID, recentEventsLimit)
	if err != nil {
		return fmt.Errorf("read recent events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var e RecentEvent
		if err := rows.Scan(&e.EventID, &e.Name, &e.OccurredAt, &e.Properties); err != nil {
			return fmt.Errorf("scan recent event: %w", err)
		}
		p.RecentEvents = append(p.RecentEvents, e)
	}
	return rows.Err()
}
