// Package httpapi is the HTTP layer: routing, handlers and middleware.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Pinger is the small part of the database that the health check needs.
// Depending on this interface instead of the real pool lets tests pass a fake.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps lists what the handlers depend on. Each field is an interface, so tests can pass fakes.
type Deps struct {
	DB     Pinger
	Events EventSaver
}

// Server holds what the handlers need.
type Server struct {
	log    *slog.Logger
	db     Pinger
	events EventSaver
}

// NewServer creates a Server.
func NewServer(log *slog.Logger, deps Deps) *Server {
	return &Server{log: log, db: deps.DB, events: deps.Events}
}

// Handler returns the router wrapped in middleware.
// The outermost wrapper runs first: requestID, then logging, then panic recovery, then the route.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /v1/events", s.handleCreateEvent)
	mux.HandleFunc("POST /v1/events/batch", s.handleCreateEventBatch)

	var h http.Handler = mux
	h = s.recoverPanics(h)
	h = s.logRequests(h)
	h = requestID(h)
	return h
}

type healthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// handleHealth tells a load balancer or Docker whether the service can do its job.
// It answers 503 when the database is unreachable, because then no endpoint would work.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		s.log.ErrorContext(ctx, "health check: database ping failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "unavailable", Database: "down"})
		return
	}
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Database: "up"})
}
