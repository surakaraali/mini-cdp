package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/surakaraali/mini-cdp/internal/segments"
	"github.com/surakaraali/mini-cdp/internal/store"
)

const (
	maxSegmentBodyBytes = 64 << 10
	defaultPageSize     = 50
	maxPageSize         = 500
	previewSampleSize   = 10
)

// SegmentStore saves segments and finds their members. *store.Store implements it.
type SegmentStore interface {
	CreateSegment(ctx context.Context, in segments.Input) (segments.Segment, error)
	ListSegments(ctx context.Context) ([]segments.Segment, error)
	GetSegment(ctx context.Context, id int64) (segments.Segment, error)
	UpdateSegment(ctx context.Context, id int64, in segments.Input) (segments.Segment, error)
	DeleteSegment(ctx context.Context, id int64) error
	SegmentMembers(ctx context.Context, rules segments.Rules, now time.Time, afterUserID string, limit int) ([]segments.Member, error)
	CountSegment(ctx context.Context, rules segments.Rules, now time.Time) (int64, error)
}

// segmentID reads {id} from the path. On a bad id it writes 400 and returns false.
func segmentID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_id", "The segment id must be a positive whole number.")
		return 0, false
	}
	return id, true
}

// writeStoreError maps the store's errors to responses. It returns false when err is nil.
func (s *Server) writeStoreError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No segment with this id.")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "name_taken", "A segment with this name already exists.")
	default:
		s.internalError(w, r, err)
	}
	return true
}

// decodeSegmentInput reads and validates a create or update body.
func decodeSegmentInput(w http.ResponseWriter, r *http.Request) (segments.Input, bool) {
	var in segments.Input
	if !decodeJSON(w, r, maxSegmentBodyBytes, &in) {
		return in, false
	}
	if errs := in.Validate(); len(errs) > 0 {
		writeValidationError(w, errs)
		return in, false
	}
	return in, true
}

func (s *Server) handleCreateSegment(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeSegmentInput(w, r)
	if !ok {
		return
	}
	seg, err := s.segments.CreateSegment(r.Context(), in)
	if s.writeStoreError(w, r, err) {
		return
	}
	w.Header().Set("Location", "/v1/segments/"+strconv.FormatInt(seg.ID, 10))
	writeJSON(w, http.StatusCreated, seg)
}

func (s *Server) handleListSegments(w http.ResponseWriter, r *http.Request) {
	list, err := s.segments.ListSegments(r.Context())
	if s.writeStoreError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"segments": list})
}

func (s *Server) handleGetSegment(w http.ResponseWriter, r *http.Request) {
	id, ok := segmentID(w, r)
	if !ok {
		return
	}
	seg, err := s.segments.GetSegment(r.Context(), id)
	if s.writeStoreError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, seg)
}

// handleUpdateSegment replaces the whole segment (PUT semantics: send every field).
func (s *Server) handleUpdateSegment(w http.ResponseWriter, r *http.Request) {
	id, ok := segmentID(w, r)
	if !ok {
		return
	}
	in, ok := decodeSegmentInput(w, r)
	if !ok {
		return
	}
	seg, err := s.segments.UpdateSegment(r.Context(), id, in)
	if s.writeStoreError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, seg)
}

func (s *Server) handleDeleteSegment(w http.ResponseWriter, r *http.Request) {
	id, ok := segmentID(w, r)
	if !ok {
		return
	}
	if s.writeStoreError(w, r, s.segments.DeleteSegment(r.Context(), id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type membersResponse struct {
	SegmentID  int64             `json:"segment_id"`
	Users      []segments.Member `json:"users"`
	NextCursor *string           `json:"next_cursor"` // null on the last page
}

// handleSegmentMembers lists the users in a segment, one page at a time:
// GET /v1/segments/{id}/users?limit=50&cursor=<next_cursor from the previous page>
//
// Membership is computed when asked, so it always reflects the latest events.
func (s *Server) handleSegmentMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := segmentID(w, r)
	if !ok {
		return
	}

	limit := defaultPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxPageSize {
			writeError(w, http.StatusBadRequest, "invalid_query", "limit must be a whole number from 1 to 500.")
			return
		}
		limit = n
	}
	cursor := r.URL.Query().Get("cursor")

	seg, err := s.segments.GetSegment(r.Context(), id)
	if s.writeStoreError(w, r, err) {
		return
	}

	// Ask for one extra row: if it comes back, there is another page.
	members, err := s.segments.SegmentMembers(r.Context(), seg.Rules, time.Now(), cursor, limit+1)
	if s.writeStoreError(w, r, err) {
		return
	}

	resp := membersResponse{SegmentID: id, Users: members}
	if len(members) > limit {
		resp.Users = members[:limit]
		next := resp.Users[limit-1].UserID
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

type previewRequest struct {
	Rules segments.Rules `json:"rules"`
}

type previewResponse struct {
	UserCount     int64    `json:"user_count"`
	SampleUserIDs []string `json:"sample_user_ids"`
}

// handlePreviewSegment answers "how many users would these rules match?" without saving anything,
// so a marketer can adjust the rules before creating the segment.
func (s *Server) handlePreviewSegment(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if !decodeJSON(w, r, maxSegmentBodyBytes, &req) {
		return
	}
	if errs := req.Rules.Validate(); len(errs) > 0 {
		writeValidationError(w, errs.WithPrefix("rules."))
		return
	}

	now := time.Now() // one "now" for both queries, so they agree on the time windows
	count, err := s.segments.CountSegment(r.Context(), req.Rules, now)
	if s.writeStoreError(w, r, err) {
		return
	}
	sample, err := s.segments.SegmentMembers(r.Context(), req.Rules, now, "", previewSampleSize)
	if s.writeStoreError(w, r, err) {
		return
	}

	ids := make([]string, len(sample))
	for i, m := range sample {
		ids[i] = m.UserID
	}
	writeJSON(w, http.StatusOK, previewResponse{UserCount: count, SampleUserIDs: ids})
}
