package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakePinger stands in for the database in tests.
type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func newTestServer(db Pinger) *Server {
	return NewServer(slog.New(slog.DiscardHandler), Deps{DB: db})
}

func TestHealth(t *testing.T) {
	tests := []struct {
		name       string
		pingErr    error
		wantStatus int
		wantBody   healthResponse
	}{
		{"database up", nil, http.StatusOK, healthResponse{Status: "ok", Database: "up"}},
		{"database down", errors.New("connection refused"), http.StatusServiceUnavailable, healthResponse{Status: "unavailable", Database: "down"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newTestServer(fakePinger{err: tt.pingErr}).Handler()
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var got healthResponse
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got != tt.wantBody {
				t.Errorf("body = %+v, want %+v", got, tt.wantBody)
			}
		})
	}
}

func TestHealthRejectsOtherMethods(t *testing.T) {
	handler := newTestServer(fakePinger{}).Handler()
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
		wantSame bool // should the response reuse the incoming id?
	}{
		{"no id sent: a new one is created", "", false},
		{"safe id is kept", "abc-123_XYZ", true},
		{"unsafe id is replaced", "bad id\nwith newline", false},
		{"too long id is replaced", string(make([]byte, 65)), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seenInHandler string
			handler := requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seenInHandler = RequestIDFrom(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.incoming != "" {
				req.Header.Set(requestIDHeader, tt.incoming)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			got := rec.Header().Get(requestIDHeader)
			if !validRequestID(got) {
				t.Fatalf("response id %q is not a valid id", got)
			}
			if got != seenInHandler {
				t.Errorf("handler saw %q but response header is %q", seenInHandler, got)
			}
			if (got == tt.incoming) != tt.wantSame {
				t.Errorf("response id = %q, incoming = %q, wantSame = %v", got, tt.incoming, tt.wantSame)
			}
		})
	}
}

func TestRecoverPanics(t *testing.T) {
	s := newTestServer(fakePinger{})
	handler := s.recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("something broke")
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != "internal_error" {
		t.Errorf("error code = %q, want internal_error", body.Error.Code)
	}
}
