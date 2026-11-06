package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/surakaraali/mini-cdp/internal/validate"
)

// writeJSON sends body as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already sent, so an encoding error cannot change the response any more.
	_ = json.NewEncoder(w).Encode(body)
}

// errorResponse is the shape of every error the API returns:
//
//	{"error": {"code": "invalid_json", "message": "..."}}
//
// code is stable and meant for programs; message is for people and may change.
type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details validate.Errors `json:"details,omitempty"` // one entry per invalid field
}

// writeError sends an error in the standard shape.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: errorDetail{Code: code, Message: message}})
}

// writeValidationError sends 422 with every field problem. 422 (not 400) means:
// the JSON was readable, but its content breaks the rules.
func writeValidationError(w http.ResponseWriter, errs validate.Errors) {
	writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: errorDetail{
		Code:    "validation_failed",
		Message: "Some fields are invalid. See details.",
		Details: errs,
	}})
}

// internalError logs the real cause and sends a generic 500.
// The client never sees database messages: they can reveal table names or data.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.ErrorContext(r.Context(), "request failed", "error", err, "request_id", RequestIDFrom(r.Context()))
	writeError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Please try again.")
}
