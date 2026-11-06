package httpapi

import (
	"encoding/json"
	"net/http"
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
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError sends an error in the standard shape.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: errorDetail{Code: code, Message: message}})
}
