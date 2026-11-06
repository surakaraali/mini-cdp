package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/surakaraali/mini-cdp/internal/events"
)

// fakeSaver records what it was asked to save and returns a fixed answer.
type fakeSaver struct {
	result events.SaveResult
	err    error
	calls  int
}

func (f *fakeSaver) SaveEvents(_ context.Context, evs []events.Event) (events.SaveResult, error) {
	f.calls++
	return f.result, f.err
}

const validEventJSON = `{"event_id":"0f8b6c1e-7a4d-4c8e-9b1a-2d3e4f5a6b7c","user_id":"u_1","name":"add_to_cart","occurred_at":"2026-11-01T10:00:00Z"}`

// do sends one request to a server that uses saver and returns the recorded response.
func do(t *testing.T, saver EventSaver, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doWith(t, Deps{DB: fakePinger{}, Events: saver}, method, path, body)
}

// doWith is like do, with every dependency chosen by the test.
func doWith(t *testing.T, deps Deps, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	srv := NewServer(slog.New(slog.DiscardHandler), deps)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// decodeError reads the standard error body.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorDetail {
	t.Helper()
	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body.Error
}

func TestCreateEventRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantCode    string
		wantMessage string   // a part of the message, when it matters
		wantFields  []string // for validation errors
	}{
		{name: "empty body", body: "", wantStatus: 400, wantCode: "invalid_json", wantMessage: "empty"},
		{name: "malformed JSON", body: `{"user_id": }`, wantStatus: 400, wantCode: "invalid_json", wantMessage: "byte"},
		{name: "cut off JSON", body: `{"user_id": "u_1"`, wantStatus: 400, wantCode: "invalid_json"},
		{name: "typo in field name", body: `{"occured_at":"2026-11-01T10:00:00Z"}`, wantStatus: 400, wantCode: "unknown_field", wantMessage: "occured_at"},
		{name: "properties is not an object", body: `{"properties":[1,2]}`, wantStatus: 400, wantCode: "invalid_json", wantMessage: `"properties" must be an object`},
		{name: "user_id is a number", body: `{"user_id":42}`, wantStatus: 400, wantCode: "invalid_json", wantMessage: `"user_id" must be a string`},
		{name: "bad timestamp", body: `{"occurred_at":"yesterday"}`, wantStatus: 400, wantCode: "invalid_json", wantMessage: "RFC 3339"},
		{name: "two JSON objects", body: validEventJSON + validEventJSON, wantStatus: 400, wantCode: "invalid_json", wantMessage: "single"},
		{name: "body over 64 KB", body: `{"properties":{"x":"` + strings.Repeat("a", 70_000) + `"}}`, wantStatus: 413, wantCode: "body_too_large"},
		{
			name:       "fields break the rules",
			body:       `{"event_id":"nope","user_id":"","name":"Add To Cart","occurred_at":"2026-11-01T10:00:00Z"}`,
			wantStatus: 422, wantCode: "validation_failed",
			wantFields: []string{"event_id", "user_id", "name"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saver := &fakeSaver{}
			rec := do(t, saver, http.MethodPost, "/v1/events", tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", got.Code, tt.wantCode)
			}
			if !strings.Contains(got.Message, tt.wantMessage) {
				t.Errorf("message = %q, want it to contain %q", got.Message, tt.wantMessage)
			}
			var gotFields []string
			for _, d := range got.Details {
				gotFields = append(gotFields, d.Field)
			}
			if !reflect.DeepEqual(gotFields, tt.wantFields) {
				t.Errorf("detail fields = %v, want %v", gotFields, tt.wantFields)
			}
			if saver.calls != 0 {
				t.Errorf("SaveEvents was called %d times, want 0 for a bad request", saver.calls)
			}
		})
	}
}

func TestCreateEventStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		saver      *fakeSaver
		wantStatus int
		wantBody   string
	}{
		{"new event", &fakeSaver{result: events.SaveResult{Created: 1}}, 201, `"status":"created"`},
		{"same event again", &fakeSaver{result: events.SaveResult{DuplicateIDs: []string{"x"}}}, 200, `"status":"duplicate"`},
		{"database error is hidden", &fakeSaver{err: errors.New("pq: relation users does not exist")}, 500, `"code":"internal_error"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, tt.saver, http.MethodPost, "/v1/events", validEventJSON)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body = %s, want it to contain %s", rec.Body.String(), tt.wantBody)
			}
			if strings.Contains(rec.Body.String(), "relation") {
				t.Errorf("body leaks the database error: %s", rec.Body.String())
			}
		})
	}
}

// batchJSON builds a batch body with n copies of a valid event, each with its own event_id.
func batchJSON(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"event_id":"00000000-0000-4000-8000-%012d","user_id":"u_1","name":"page_view","occurred_at":"2026-11-01T10:00:00Z"}`, i)
	}
	return `{"events":[` + strings.Join(items, ",") + `]}`
}

func TestCreateEventBatchRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantFields []string
	}{
		{"no events", `{"events":[]}`, []string{"events"}},
		{"too many events", batchJSON(501), []string{"events"}},
		{
			"problems are reported per item",
			`{"events":[` + validEventJSON + `,{"event_id":"bad","user_id":"u_1","name":"x","occurred_at":"2026-11-01T10:00:00Z"}]}`,
			[]string{"events[1].event_id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saver := &fakeSaver{}
			rec := do(t, saver, http.MethodPost, "/v1/events/batch", tt.body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422 (body: %s)", rec.Code, rec.Body.String())
			}
			var gotFields []string
			for _, d := range decodeError(t, rec).Details {
				gotFields = append(gotFields, d.Field)
			}
			if !reflect.DeepEqual(gotFields, tt.wantFields) {
				t.Errorf("detail fields = %v, want %v", gotFields, tt.wantFields)
			}
			if saver.calls != 0 {
				t.Errorf("SaveEvents was called; a batch with any invalid event must store nothing")
			}
		})
	}
}

func TestCreateEventBatchAcceptsMaxSize(t *testing.T) {
	saver := &fakeSaver{result: events.SaveResult{Created: maxBatchSize}}
	rec := do(t, saver, http.MethodPost, "/v1/events/batch", batchJSON(maxBatchSize))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	want := `{"received":500,"created":500,"duplicates":0,"duplicate_event_ids":[]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}
