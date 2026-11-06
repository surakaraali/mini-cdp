package httpapi

import (
	"context"
	"crypto/rand"
	"net/http"
	"runtime/debug"
	"time"
)

const requestIDHeader = "X-Request-ID"

// ctxKey is a private type, so no other package can read or overwrite our context values by accident.
type ctxKey int

const requestIDKey ctxKey = 0

// requestID gives every request an id, returns it in the X-Request-ID header and stores it in the
// context. When a user reports an error, the id finds the matching log lines.
// A caller may send its own id (for tracing across services); it is kept only if it looks safe,
// so nobody can inject odd characters into our logs.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !validRequestID(id) {
			id = rand.Text() // 26 random characters
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFrom returns the request id stored by the requestID middleware, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// validRequestID accepts 1 to 64 letters, digits, '-' or '_'.
func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		isDigit := c >= '0' && c <= '9'
		if !isLetter && !isDigit && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// statusRecorder remembers the status code a handler wrote, so the logger can report it.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rec *statusRecorder) WriteHeader(code int) {
	if !rec.wroteHeader {
		rec.status = code
		rec.wroteHeader = true
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}
	return rec.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the real writer underneath.
func (rec *statusRecorder) Unwrap() http.ResponseWriter {
	return rec.ResponseWriter
}

// logRequests writes one log line per request with its status and duration.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		s.log.InfoContext(r.Context(), "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", float64(time.Since(start).Microseconds())/1000,
			"request_id", RequestIDFrom(r.Context()),
		)
	})
}

// recoverPanics turns a panic in a handler into a 500 response and a log line,
// instead of dropping the connection without an answer.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v) // a deliberate abort; net/http handles it
			}
			s.log.ErrorContext(r.Context(), "panic in handler",
				"panic", v,
				"stack", string(debug.Stack()),
				"request_id", RequestIDFrom(r.Context()),
			)
			writeError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Please try again.")
		}()
		next.ServeHTTP(w, r)
	})
}
