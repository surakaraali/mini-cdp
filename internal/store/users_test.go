package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/surakaraali/mini-cdp/internal/events"
	"github.com/surakaraali/mini-cdp/internal/testdb"
)

func TestGetProfile(t *testing.T) {
	pool := testdb.New(t)
	s := New(pool)
	ctx := context.Background()

	var batch []events.Event
	// 10 page views, then 3 purchases; one purchase has a broken revenue value.
	for i := range 10 {
		e := newEvent("u_1", "page_view", base.Add(time.Duration(i)*time.Minute))
		if i == 0 {
			e.UserAttributes = map[string]any{"city": "Izmir"}
		}
		batch = append(batch, e)
	}
	// json.Number, as the API decodes it: a Go float64 would already turn 100.10 into 100.1.
	for i, revenue := range []any{json.Number("100.10"), json.Number("200.00"), "abc"} {
		e := newEvent("u_1", "purchase", base.Add(time.Hour+time.Duration(i)*time.Minute))
		e.Properties = map[string]any{"revenue": revenue}
		batch = append(batch, e)
	}
	if _, err := s.SaveEvents(ctx, batch); err != nil {
		t.Fatalf("seed: %v", err)
	}

	p, err := s.GetProfile(ctx, "u_1")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}

	checks := []struct {
		name      string
		got, want any
	}{
		{"attributes", string(p.Attributes), `{"city": "Izmir"}`},
		{"total events", p.TotalEvents, int64(13)},
		{"page views", p.EventCounts["page_view"], int64(10)},
		{"purchases", p.Purchases.Count, int64(3)},
		// 100.10 + 200.00; the "abc" revenue is skipped instead of breaking the query.
		{"revenue", p.Purchases.TotalRevenue.String(), "300.10"},
		{"last purchase", fmt.Sprint(p.Purchases.LastAt.UTC()), fmt.Sprint(base.Add(time.Hour + 2*time.Minute))},
		{"recent events are capped", len(p.RecentEvents), recentEventsLimit},
		{"newest event first", p.RecentEvents[0].Name, "purchase"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestGetProfileUnknownUser(t *testing.T) {
	s := New(testdb.New(t))

	if _, err := s.GetProfile(context.Background(), "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestGetProfileWithoutPurchases(t *testing.T) {
	s := New(testdb.New(t))
	ctx := context.Background()
	if _, err := s.SaveEvents(ctx, []events.Event{newEvent("u_1", "page_view", base)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	p, err := s.GetProfile(ctx, "u_1")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.Purchases.Count != 0 || p.Purchases.TotalRevenue != "0" || p.Purchases.LastAt != nil {
		t.Errorf("purchases = %+v, want count 0, revenue 0, last_at nil", p.Purchases)
	}
}
