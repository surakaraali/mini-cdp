package events

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

// validEvent returns an event that passes validation; each test case changes one thing.
func validEvent() Event {
	return Event{
		EventID:    "0f8b6c1e-7a4d-4c8e-9b1a-2d3e4f5a6b7c",
		UserID:     "u_1",
		Name:       "add_to_cart",
		OccurredAt: testNow.Add(-time.Hour),
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name       string
		change     func(e *Event)
		wantFields []string // fields that should be reported, in order
	}{
		{"valid event", func(e *Event) {}, nil},
		{"everything missing", func(e *Event) { *e = Event{} }, []string{"event_id", "user_id", "name", "occurred_at"}},
		{"event_id not a uuid", func(e *Event) { e.EventID = "12345" }, []string{"event_id"}},
		{"user_id too long", func(e *Event) { e.UserID = strings.Repeat("a", 129) }, []string{"user_id"}},
		{"user_id of 128 multi-byte characters is fine", func(e *Event) { e.UserID = strings.Repeat("ş", 128) }, nil},
		{"name with capital letters", func(e *Event) { e.Name = "AddToCart" }, []string{"name"}},
		{"name with a dash", func(e *Event) { e.Name = "add-to-cart" }, []string{"name"}},
		{"name starting with a digit", func(e *Event) { e.Name = "1st_visit" }, []string{"name"}},
		{"name of 65 characters", func(e *Event) { e.Name = "a" + strings.Repeat("b", 64) }, []string{"name"}},
		{"occurred_at slightly ahead (clock skew) is fine", func(e *Event) { e.OccurredAt = testNow.Add(4 * time.Minute) }, nil},
		{"occurred_at in the future", func(e *Event) { e.OccurredAt = testNow.Add(time.Hour) }, []string{"occurred_at"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEvent()
			tt.change(&e)

			errs := e.Validate(testNow)

			var gotFields []string
			for _, fe := range errs {
				gotFields = append(gotFields, fe.Field)
			}
			if !reflect.DeepEqual(gotFields, tt.wantFields) {
				t.Errorf("reported fields = %v, want %v (errors: %+v)", gotFields, tt.wantFields, errs)
			}
		})
	}
}

func TestValidateNormalizesEventID(t *testing.T) {
	e := validEvent()
	e.EventID = "{0F8B6C1E-7A4D-4C8E-9B1A-2D3E4F5A6B7C}"

	if errs := e.Validate(testNow); len(errs) > 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	if want := "0f8b6c1e-7a4d-4c8e-9b1a-2d3e4f5a6b7c"; e.EventID != want {
		t.Errorf("EventID = %q, want %q", e.EventID, want)
	}
}

func TestUserUpdates(t *testing.T) {
	t1 := testNow.Add(-3 * time.Hour)
	t2 := testNow.Add(-2 * time.Hour)
	t3 := testNow.Add(-1 * time.Hour)

	// Deliberately out of order: the newest event for u_b comes first in the batch.
	batch := []Event{
		{UserID: "u_b", OccurredAt: t3, UserAttributes: map[string]any{"city": "Izmir"}},
		{UserID: "u_a", OccurredAt: t2},
		{UserID: "u_b", OccurredAt: t1, UserAttributes: map[string]any{"city": "Istanbul", "plan": "free"}},
	}

	got := UserUpdates(batch)

	want := []UserUpdate{
		{UserID: "u_a", Attributes: map[string]any{}, FirstSeen: t2, LastSeen: t2},
		// The newer city (Izmir) wins; plan from the older event is kept.
		{UserID: "u_b", Attributes: map[string]any{"city": "Izmir", "plan": "free"}, FirstSeen: t1, LastSeen: t3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UserUpdates() =\n%+v\nwant\n%+v", got, want)
	}
}
