package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/surakaraali/mini-cdp/internal/store"
)

// ProfileReader returns user profiles. *store.Store implements it.
type ProfileReader interface {
	GetProfile(ctx context.Context, userID string) (store.Profile, error)
}

// handleGetUser returns one user's profile: attributes, event counts, purchase stats and latest events.
func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	profile, err := s.profiles.GetProfile(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No user with this id.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}
