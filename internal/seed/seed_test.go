package seed

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func TestGenerateIsRepeatable(t *testing.T) {
	cfg := Config{Users: 200, Events: 5_000, Days: 30, Seed: 7}

	a, b := Generate(cfg, now), Generate(cfg, now)

	if !reflect.DeepEqual(a, b) {
		t.Fatal("two runs with the same config produced different data")
	}
	if c := Generate(Config{Users: 200, Events: 5_000, Days: 30, Seed: 8}, now); reflect.DeepEqual(a, c) {
		t.Fatal("a different seed produced the same data")
	}
}

func TestGenerateShape(t *testing.T) {
	cfg := Config{Users: 1_000, Events: 50_000, Days: 30, Seed: 1}
	d := Generate(cfg, now)

	if len(d.Users) != cfg.Users || len(d.Events) != cfg.Events {
		t.Fatalf("got %d users and %d events, want %d and %d", len(d.Users), len(d.Events), cfg.Users, cfg.Events)
	}

	start := now.Add(-30 * 24 * time.Hour)
	heavy := 0
	for i, e := range d.Events {
		if e.At.Before(start) || e.At.After(now) {
			t.Fatalf("event %d at %v is outside the window", i, e.At)
		}
		if i > 0 && e.At.Before(d.Events[i-1].At) {
			t.Fatalf("events are not sorted by time at %d", i)
		}
		if e.User < int32(cfg.Users/100) {
			heavy++
		}
	}
	// 1% of users should make roughly 20% of events (plus their share of the uniform part).
	if share := float64(heavy) / float64(cfg.Events); share < 0.18 || share > 0.24 {
		t.Errorf("heavy users made %.1f%% of events, want about 21%%", share*100)
	}

	for _, u := range d.Users {
		if u.LastSeen.Before(u.FirstSeen) {
			t.Fatalf("user %s: last seen %v is before first seen %v", u.ID, u.LastSeen, u.FirstSeen)
		}
	}
}

func TestJSONColumnsAreValid(t *testing.T) {
	d := Generate(Config{Users: 50, Events: 500, Days: 7, Seed: 3}, now)

	for _, u := range d.Users {
		if !json.Valid(u.AttributesJSON()) {
			t.Fatalf("invalid attributes JSON: %s", u.AttributesJSON())
		}
	}
	for i, e := range d.Events {
		if !json.Valid(e.PropertiesJSON(i)) {
			t.Fatalf("invalid properties JSON: %s", e.PropertiesJSON(i))
		}
	}
	if got := money(19990); got != "199.90" {
		t.Errorf("money(19990) = %s, want 199.90", got)
	}
	if got := money(5); got != "0.05" {
		t.Errorf("money(5) = %s, want 0.05", got)
	}
}
