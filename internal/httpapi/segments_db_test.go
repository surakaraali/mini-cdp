package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/surakaraali/mini-cdp/internal/store"
	"github.com/surakaraali/mini-cdp/internal/testdb"
)

// TestUsersAndSegmentsWithDatabase walks through the API the way a marketer's tool would use it.
func TestUsersAndSegmentsWithDatabase(t *testing.T) {
	st := store.New(testdb.New(t))
	deps := Deps{DB: fakePinger{}, Events: st, Profiles: st, Segments: st}
	call := func(method, path, body string) (int, string) {
		rec := doWith(t, deps, method, path, body)
		return rec.Code, rec.Body.String()
	}

	// Three shoppers: u_1 and u_2 left items in the cart, u_3 bought.
	ago := func(h int) string { return time.Now().UTC().Add(-time.Duration(h) * time.Hour).Format(time.RFC3339) }
	ev := func(n int, user, name string, hoursAgo int, extra string) string {
		return fmt.Sprintf(`{"event_id":"00000000-0000-4000-8000-%012d","user_id":%q,"name":%q,"occurred_at":%q%s}`,
			n, user, name, ago(hoursAgo), extra)
	}
	batch := `{"events":[` + strings.Join([]string{
		ev(1, "u_1", "add_to_cart", 30, `,"user_attributes":{"city":"Istanbul"}`),
		ev(2, "u_2", "add_to_cart", 20, `,"user_attributes":{"city":"Istanbul"}`),
		ev(3, "u_3", "add_to_cart", 10, `,"user_attributes":{"city":"Istanbul"}`),
		ev(4, "u_3", "purchase", 5, `,"properties":{"revenue":1299.90}`),
	}, ",") + `]}`
	if code, body := call("POST", "/v1/events/batch", batch); code != http.StatusOK {
		t.Fatalf("seed batch: %d %s", code, body)
	}

	t.Run("user profile", func(t *testing.T) {
		code, body := call("GET", "/v1/users/u_3", "")
		if code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", code, body)
		}
		for _, want := range []string{`"user_id":"u_3"`, `"total_events":2`, `"total_revenue":1299.90`, `"city":"Istanbul"`} {
			if !strings.Contains(body, want) {
				t.Errorf("body does not contain %s: %s", want, body)
			}
		}
		if code, _ := call("GET", "/v1/users/nobody", ""); code != http.StatusNotFound {
			t.Errorf("unknown user: status = %d, want 404", code)
		}
	})

	t.Run("invalid rules are listed with their path", func(t *testing.T) {
		code, body := call("POST", "/v1/segments", `{"name":"x","rules":{"conditions":[{"type":"event","event":"Bad Name","within_days":0}]}}`)
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", code)
		}
		for _, want := range []string{`"rules.conditions[0].event"`, `"rules.conditions[0].within_days"`} {
			if !strings.Contains(body, want) {
				t.Errorf("body does not contain %s: %s", want, body)
			}
		}
	})

	abandoners := `{"name":"Cart abandoners","description":"Added to cart in 7 days, no purchase",
		"rules":{"match":"all","conditions":[
			{"type":"event","event":"add_to_cart","within_days":7},
			{"type":"event","event":"purchase","within_days":7,"max_count":0}]}}`

	var segmentPath string
	t.Run("create, then the same name again", func(t *testing.T) {
		rec := doWith(t, deps, "POST", "/v1/segments", abandoners)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		segmentPath = rec.Header().Get("Location")
		if !strings.HasPrefix(segmentPath, "/v1/segments/") {
			t.Fatalf("Location = %q", segmentPath)
		}
		if code, body := call("POST", "/v1/segments", abandoners); code != http.StatusConflict || !strings.Contains(body, "name_taken") {
			t.Errorf("duplicate name: %d %s, want 409 name_taken", code, body)
		}
	})

	t.Run("members, one per page", func(t *testing.T) {
		var got []string
		path := segmentPath + "/users?limit=1"
		for page := 0; page < 5; page++ {
			code, body := call("GET", path, "")
			if code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", code, body)
			}
			var resp membersResponse
			if err := json.Unmarshal([]byte(body), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			for _, u := range resp.Users {
				got = append(got, u.UserID)
			}
			if resp.NextCursor == nil {
				break
			}
			path = segmentPath + "/users?limit=1&cursor=" + *resp.NextCursor
		}
		if strings.Join(got, ",") != "u_1,u_2" {
			t.Errorf("members = %v, want [u_1 u_2]", got)
		}
	})

	t.Run("preview without saving", func(t *testing.T) {
		code, body := call("POST", "/v1/segments/preview",
			`{"rules":{"conditions":[{"type":"attribute","attribute":"city","op":"eq","value":"Istanbul"}]}}`)
		want := `{"user_count":3,"sample_user_ids":["u_1","u_2","u_3"]}`
		if code != http.StatusOK || strings.TrimSpace(body) != want {
			t.Errorf("preview = %d %s, want 200 %s", code, body, want)
		}
	})

	t.Run("update, delete and bad ids", func(t *testing.T) {
		updated := strings.Replace(abandoners, "Cart abandoners", "Cart abandoners (7d)", 1)
		if code, body := call("PUT", segmentPath, updated); code != http.StatusOK || !strings.Contains(body, "(7d)") {
			t.Errorf("update = %d %s", code, body)
		}
		if code, _ := call("GET", "/v1/segments", ""); code != http.StatusOK {
			t.Errorf("list status = %d", code)
		}
		if code, _ := call("DELETE", segmentPath, ""); code != http.StatusNoContent {
			t.Errorf("delete status = %d, want 204", code)
		}
		if code, _ := call("GET", segmentPath, ""); code != http.StatusNotFound {
			t.Errorf("get after delete = %d, want 404", code)
		}
		if code, _ := call("GET", "/v1/segments/abc", ""); code != http.StatusBadRequest {
			t.Errorf("non-numeric id = %d, want 400", code)
		}
		if code, _ := call("GET", "/v1/segments/1/users?limit=0", ""); code != http.StatusBadRequest {
			t.Errorf("limit=0 = %d, want 400", code)
		}
	})
}
