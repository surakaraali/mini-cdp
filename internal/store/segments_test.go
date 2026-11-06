package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/surakaraali/mini-cdp/internal/events"
	"github.com/surakaraali/mini-cdp/internal/segments"
	"github.com/surakaraali/mini-cdp/internal/testdb"
)

func intPtr(n int) *int { return &n }

// seedShop stores five users whose behavior covers the interesting cases:
//
//	u_a  Istanbul  3 page views, add_to_cart 2 days ago, never bought   -> a cart abandoner
//	u_b  Istanbul  add_to_cart 2 days ago, purchase 1 day ago           -> bought, not an abandoner
//	u_c  Istanbul  add_to_cart 10 days ago                              -> outside a 7-day window
//	u_d  Ankara    add_to_cart 1 day ago                                -> abandoner, other city
//	u_e  (no city) 1 page view, plan "pro"
func seedShop(t *testing.T, s *Store, now time.Time) {
	t.Helper()
	day := 24 * time.Hour
	ev := func(user, name string, ago time.Duration, attrs map[string]any) events.Event {
		e := newEvent(user, name, now.Add(-ago))
		e.UserAttributes = attrs
		return e
	}
	istanbul := map[string]any{"city": "Istanbul"}

	batch := []events.Event{
		ev("u_a", "page_view", 3*day, istanbul), ev("u_a", "page_view", 3*day, nil), ev("u_a", "page_view", 2*day, nil),
		ev("u_a", "add_to_cart", 2*day, nil),
		ev("u_b", "add_to_cart", 2*day, istanbul), ev("u_b", "purchase", 1*day, nil),
		ev("u_c", "add_to_cart", 10*day, istanbul),
		ev("u_d", "add_to_cart", 1*day, map[string]any{"city": "Ankara"}),
		ev("u_e", "page_view", 1*day, map[string]any{"plan": "pro"}),
	}
	if _, err := s.SaveEvents(context.Background(), batch); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestSegmentMatching(t *testing.T) {
	pool := testdb.New(t)
	s := New(pool)
	now := time.Now().UTC()
	seedShop(t, s, now)

	addedToCart7d := segments.Condition{Type: segments.TypeEvent, Event: "add_to_cart", WithinDays: intPtr(7)}
	noPurchase7d := segments.Condition{Type: segments.TypeEvent, Event: "purchase", WithinDays: intPtr(7), MaxCount: intPtr(0)}
	inIstanbul := segments.Condition{Type: segments.TypeAttribute, Attribute: "city", Op: segments.OpEquals, Value: "Istanbul"}

	tests := []struct {
		name  string
		rules segments.Rules
		want  []string
	}{
		{"cart abandoners in Istanbul", segments.Rules{Match: "all", Conditions: []segments.Condition{addedToCart7d, noPurchase7d, inIstanbul}}, []string{"u_a"}},
		{"cart abandoners anywhere", segments.Rules{Match: "all", Conditions: []segments.Condition{addedToCart7d, noPurchase7d}}, []string{"u_a", "u_d"}},
		{
			"never bought, including users with no events of that kind",
			segments.Rules{Match: "all", Conditions: []segments.Condition{{Type: "event", Event: "purchase", MaxCount: intPtr(0)}}},
			[]string{"u_a", "u_c", "u_d", "u_e"},
		},
		{
			"viewed pages at least twice",
			segments.Rules{Match: "all", Conditions: []segments.Condition{{Type: "event", Event: "page_view", MinCount: intPtr(2)}}},
			[]string{"u_a"},
		},
		{
			"city is not Istanbul (users without a city count too)",
			segments.Rules{Match: "all", Conditions: []segments.Condition{{Type: "attribute", Attribute: "city", Op: "neq", Value: "Istanbul"}}},
			[]string{"u_d", "u_e"},
		},
		{
			"any: in Ankara or has a plan",
			segments.Rules{Match: "any", Conditions: []segments.Condition{
				{Type: "attribute", Attribute: "city", Op: "eq", Value: "Ankara"},
				{Type: "attribute", Attribute: "plan", Op: "exists"},
			}},
			[]string{"u_d", "u_e"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			members, err := s.SegmentMembers(context.Background(), tt.rules, now, "", 100)
			if err != nil {
				t.Fatalf("SegmentMembers: %v", err)
			}
			got := []string{}
			for _, m := range members {
				got = append(got, m.UserID)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("members = %v, want %v", got, tt.want)
			}

			count, err := s.CountSegment(context.Background(), tt.rules, now)
			if err != nil {
				t.Fatalf("CountSegment: %v", err)
			}
			if count != int64(len(tt.want)) {
				t.Errorf("count = %d, want %d", count, len(tt.want))
			}
		})
	}
}

func TestSegmentMembersPaging(t *testing.T) {
	pool := testdb.New(t)
	s := New(pool)
	now := time.Now().UTC()
	seedShop(t, s, now)
	everyone := segments.Rules{Match: "all", Conditions: []segments.Condition{{Type: "event", Event: "purchase", MaxCount: intPtr(5)}}}

	var pages [][]string
	after := ""
	for {
		members, err := s.SegmentMembers(context.Background(), everyone, now, after, 2)
		if err != nil {
			t.Fatalf("SegmentMembers: %v", err)
		}
		if len(members) == 0 {
			break
		}
		var page []string
		for _, m := range members {
			page = append(page, m.UserID)
		}
		pages = append(pages, page)
		after = page[len(page)-1]
	}

	want := [][]string{{"u_a", "u_b"}, {"u_c", "u_d"}, {"u_e"}}
	if !reflect.DeepEqual(pages, want) {
		t.Errorf("pages = %v, want %v", pages, want)
	}
}

func TestSegmentCRUD(t *testing.T) {
	pool := testdb.New(t)
	s := New(pool)
	ctx := context.Background()

	in := segments.Input{
		Name:        "Adults",
		Description: "age is 30",
		Rules: segments.Rules{Match: "all", Conditions: []segments.Condition{
			{Type: "attribute", Attribute: "age", Op: "eq", Value: json.Number("30")},
		}},
	}

	created, err := s.CreateSegment(ctx, in)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if created.ID == 0 || created.CreatedAt.IsZero() {
		t.Errorf("created segment has no id or timestamp: %+v", created)
	}
	// The rules come back exactly as they went in, including the number type.
	if !reflect.DeepEqual(created.Rules, in.Rules) {
		t.Errorf("rules after save = %+v, want %+v", created.Rules, in.Rules)
	}

	if _, err := s.CreateSegment(ctx, in); !errors.Is(err, ErrConflict) {
		t.Errorf("second create with the same name: err = %v, want ErrConflict", err)
	}

	other, err := s.CreateSegment(ctx, segments.Input{Name: "Other", Rules: in.Rules})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	if _, err := s.UpdateSegment(ctx, other.ID, in); !errors.Is(err, ErrConflict) {
		t.Errorf("rename to a taken name: err = %v, want ErrConflict", err)
	}

	in.Description = "changed"
	updated, err := s.UpdateSegment(ctx, created.ID, in)
	if err != nil {
		t.Fatalf("UpdateSegment: %v", err)
	}
	if updated.Description != "changed" || !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("update did not change description or updated_at: %+v", updated)
	}

	list, err := s.ListSegments(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListSegments = %d segments, %v; want 2, nil", len(list), err)
	}

	if err := s.DeleteSegment(ctx, created.ID); err != nil {
		t.Fatalf("DeleteSegment: %v", err)
	}
	if _, err := s.GetSegment(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete: err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteSegment(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: err = %v, want ErrNotFound", err)
	}
	if _, err := s.UpdateSegment(ctx, created.ID, in); !errors.Is(err, ErrNotFound) {
		t.Errorf("update after delete: err = %v, want ErrNotFound", err)
	}
}
