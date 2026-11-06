package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/surakaraali/mini-cdp/internal/store"
	"github.com/surakaraali/mini-cdp/internal/testdb"
)

// These tests go through the real HTTP handlers and the real database.

func TestEventEndpointsWithDatabase(t *testing.T) {
	pool := testdb.New(t)
	saver := store.New(pool)

	t.Run("large numbers in properties keep every digit", func(t *testing.T) {
		// 2^53 + 1: the first integer a float64 cannot hold exactly.
		body := `{"event_id":"33333333-3333-4333-8333-333333333333","user_id":"u_3","name":"purchase",
			"occurred_at":"2026-11-01T10:00:00Z","properties":{"order_id":9007199254740993,"revenue":499.90}}`
		if rec := do(t, saver, http.MethodPost, "/v1/events", body); rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
		}

		var orderID, revenue string
		err := pool.QueryRow(context.Background(),
			`SELECT properties->>'order_id', properties->>'revenue' FROM events WHERE event_id = '33333333-3333-4333-8333-333333333333'`).
			Scan(&orderID, &revenue)
		if err != nil {
			t.Fatalf("read event: %v", err)
		}
		if orderID != "9007199254740993" {
			t.Errorf("order_id stored as %s, want 9007199254740993", orderID)
		}
		if revenue != "499.90" {
			t.Errorf("revenue stored as %s, want 499.90", revenue)
		}
	})

	t.Run("first send creates, retry is a duplicate", func(t *testing.T) {
		rec := do(t, saver, http.MethodPost, "/v1/events", validEventJSON)
		if rec.Code != http.StatusCreated {
			t.Fatalf("first send: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
		}

		rec = do(t, saver, http.MethodPost, "/v1/events", validEventJSON)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"duplicate"`) {
			t.Fatalf("retry: status = %d, body = %s; want 200 duplicate", rec.Code, rec.Body.String())
		}
	})

	t.Run("batch counts new and repeated events", func(t *testing.T) {
		// validEventJSON was stored by the subtest above; the other two are new.
		body := `{"events":[` + validEventJSON + `,
			{"event_id":"11111111-1111-4111-8111-111111111111","user_id":"u_2","name":"page_view","occurred_at":"2026-11-01T10:00:00Z"},
			{"event_id":"22222222-2222-4222-8222-222222222222","user_id":"u_2","name":"purchase","occurred_at":"2026-11-01T10:05:00Z","properties":{"revenue":499.9}}
		]}`
		rec := do(t, saver, http.MethodPost, "/v1/events/batch", body)

		want := `{"received":3,"created":2,"duplicates":1,"duplicate_event_ids":["0f8b6c1e-7a4d-4c8e-9b1a-2d3e4f5a6b7c"]}`
		if got := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || got != want {
			t.Errorf("status = %d, body = %s; want 200 %s", rec.Code, got, want)
		}
	})
}
