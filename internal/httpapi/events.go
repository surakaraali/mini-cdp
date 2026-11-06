package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/surakaraali/mini-cdp/internal/events"
	"github.com/surakaraali/mini-cdp/internal/validate"
)

const (
	maxEventBodyBytes = 64 << 10 // 64 KB for one event
	maxBatchBodyBytes = 1 << 20  // 1 MB for a batch
	maxBatchSize      = 500
)

// EventSaver stores events. *store.Store implements it; tests can pass a fake.
type EventSaver interface {
	SaveEvents(ctx context.Context, evs []events.Event) (events.SaveResult, error)
}

type eventResponse struct {
	EventID string `json:"event_id"`
	Status  string `json:"status"` // "created" or "duplicate"
}

// handleCreateEvent stores one event.
// 201 Created for a new event, 200 OK when the same event_id was stored before,
// so a client can retry after a timeout without creating a second copy.
func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	var ev events.Event
	if !decodeJSON(w, r, maxEventBodyBytes, &ev) {
		return
	}
	if errs := ev.Validate(time.Now()); len(errs) > 0 {
		writeValidationError(w, errs)
		return
	}

	res, err := s.events.SaveEvents(r.Context(), []events.Event{ev})
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	if res.Created == 1 {
		writeJSON(w, http.StatusCreated, eventResponse{EventID: ev.EventID, Status: "created"})
		return
	}
	writeJSON(w, http.StatusOK, eventResponse{EventID: ev.EventID, Status: "duplicate"})
}

type batchRequest struct {
	Events []events.Event `json:"events"`
}

type batchResponse struct {
	Received          int      `json:"received"`
	Created           int      `json:"created"`
	Duplicates        int      `json:"duplicates"`
	DuplicateEventIDs []string `json:"duplicate_event_ids"`
}

// handleCreateEventBatch stores up to 500 events in one request.
// If any event is invalid, nothing is stored and every problem is listed;
// partial success would force clients to work out which events to resend.
func (s *Server) handleCreateEventBatch(w http.ResponseWriter, r *http.Request) {
	var req batchRequest
	if !decodeJSON(w, r, maxBatchBodyBytes, &req) {
		return
	}

	switch n := len(req.Events); {
	case n == 0:
		writeValidationError(w, validate.Errors{{Field: "events", Problem: "must contain at least 1 event"}})
		return
	case n > maxBatchSize:
		writeValidationError(w, validate.Errors{{Field: "events", Problem: fmt.Sprintf("must contain at most %d events, got %d", maxBatchSize, n)}})
		return
	}

	now := time.Now()
	var errs validate.Errors
	for i := range req.Events {
		itemErrs := req.Events[i].Validate(now) // by index, so Validate can normalize the event in place
		errs = append(errs, itemErrs.WithPrefix(fmt.Sprintf("events[%d].", i))...)
	}
	if len(errs) > 0 {
		writeValidationError(w, errs)
		return
	}

	res, err := s.events.SaveEvents(r.Context(), req.Events)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	dupIDs := res.DuplicateIDs
	if dupIDs == nil {
		dupIDs = []string{} // send [] rather than null
	}
	writeJSON(w, http.StatusOK, batchResponse{
		Received:          len(req.Events),
		Created:           res.Created,
		Duplicates:        len(dupIDs),
		DuplicateEventIDs: dupIDs,
	})
}
