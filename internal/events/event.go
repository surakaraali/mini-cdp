// Package events defines a tracked event, how it is validated,
// and how a batch of events changes the users who sent them.
package events

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/surakaraali/mini-cdp/internal/validate"
)

const (
	maxUserIDLength = 128 // same limit as the CHECK constraint on users.id
	// A phone's clock can be a little ahead of ours, so "the future" starts 5 minutes from now.
	maxClockSkew = 5 * time.Minute
)

// namePattern matches the CHECK constraint on events.name: snake_case, starts with a letter, at most 64 characters.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Event is one thing a user did on the shop's site or app.
type Event struct {
	// EventID is created by the client. Sending the same id again is safe: the event is stored once.
	EventID    string         `json:"event_id"`
	UserID     string         `json:"user_id"`
	Name       string         `json:"name"` // page_view, product_view, add_to_cart, purchase, ...
	OccurredAt time.Time      `json:"occurred_at"`
	Properties map[string]any `json:"properties,omitempty"` // product_id, price, category, ...
	// UserAttributes are merged into the user's profile, e.g. {"email": "...", "city": "Istanbul"}.
	UserAttributes map[string]any `json:"user_attributes,omitempty"`
}

// Validate checks the event and returns every problem it finds (not just the first one),
// so a client can fix them all at once. now is passed in so tests can control the clock.
// It also rewrites EventID to the standard lowercase form, so "ABC..." and "abc..." count as the same event.
func (e *Event) Validate(now time.Time) validate.Errors {
	var errs validate.Errors

	if e.EventID == "" {
		errs.Add("event_id", "is required")
	} else if id, err := uuid.Parse(e.EventID); err != nil {
		errs.Add("event_id", "must be a UUID, e.g. 0f8b6c1e-7a4d-4c8e-9b1a-2d3e4f5a6b7c")
	} else {
		e.EventID = id.String()
	}

	switch {
	case e.UserID == "":
		errs.Add("user_id", "is required")
	case utf8.RuneCountInString(e.UserID) > maxUserIDLength:
		errs.Add("user_id", "must be at most 128 characters")
	}

	switch {
	case e.Name == "":
		errs.Add("name", "is required")
	case !namePattern.MatchString(e.Name):
		errs.Add("name", "must be snake_case (lowercase letters, digits, _), start with a letter and be at most 64 characters")
	}

	switch {
	case e.OccurredAt.IsZero():
		errs.Add("occurred_at", "is required, e.g. 2026-11-06T10:00:00Z")
	case e.OccurredAt.After(now.Add(maxClockSkew)):
		errs.Add("occurred_at", "must not be in the future")
	}

	return errs
}

// SaveResult reports what happened when a list of events was stored.
type SaveResult struct {
	Created      int      // events stored now
	DuplicateIDs []string // events that were already stored, so they were skipped
}

// UserUpdate is the change one batch makes to one user's row.
type UserUpdate struct {
	UserID     string
	Attributes map[string]any
	FirstSeen  time.Time
	LastSeen   time.Time
}

// UserUpdates folds a batch into one update per user, sorted by user id.
//
// Events are applied oldest first, so a newer attribute value overwrites an older one.
// The result is sorted because the database locks each user row it updates:
// when every transaction locks rows in the same order, two transactions cannot deadlock
// waiting for each other.
func UserUpdates(evs []Event) []UserUpdate {
	oldestFirst := slices.Clone(evs)
	slices.SortStableFunc(oldestFirst, func(a, b Event) int { return a.OccurredAt.Compare(b.OccurredAt) })

	byUser := make(map[string]*UserUpdate)
	for _, e := range oldestFirst {
		u, seen := byUser[e.UserID]
		if !seen {
			u = &UserUpdate{UserID: e.UserID, Attributes: map[string]any{}, FirstSeen: e.OccurredAt}
			byUser[e.UserID] = u
		}
		maps.Copy(u.Attributes, e.UserAttributes)
		u.LastSeen = e.OccurredAt
	}

	out := make([]UserUpdate, 0, len(byUser))
	for _, u := range byUser {
		out = append(out, *u)
	}
	slices.SortFunc(out, func(a, b UserUpdate) int { return strings.Compare(a.UserID, b.UserID) })
	return out
}
