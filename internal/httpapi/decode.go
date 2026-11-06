package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// decodeJSON reads one JSON object from the request body into dst.
// On failure it writes a 400 or 413 response and returns false, so a handler can simply return.
//
// It is strict on purpose:
//   - the body is capped at maxBytes, so one huge request cannot eat the server's memory;
//   - unknown fields are rejected, so a typo like "occured_at" is reported instead of silently ignored;
//   - only one JSON value is allowed in the body;
//   - numbers inside free-form maps (properties, user_attributes) keep their exact digits.
//     By default Go turns them into float64, which silently changes an order id like
//     9007199254740993 into ...992. UseNumber keeps the original text instead.
func decodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	dec.UseNumber()

	if err := dec.Decode(dst); err != nil {
		status, code, message := describeDecodeError(err)
		writeError(w, status, code, message)
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "The body must contain a single JSON object.")
		return false
	}
	return true
}

// describeDecodeError turns Go's JSON errors into messages a client developer can act on,
// without leaking internal names like Go struct types.
func describeDecodeError(err error) (status int, code, message string) {
	var (
		syntaxErr    *json.SyntaxError
		typeErr      *json.UnmarshalTypeError
		tooLargeErr  *http.MaxBytesError
		timeParseErr *time.ParseError
	)

	switch {
	case errors.Is(err, io.EOF):
		return http.StatusBadRequest, "invalid_json", "The body is empty."
	case errors.Is(err, io.ErrUnexpectedEOF):
		return http.StatusBadRequest, "invalid_json", "The body ends in the middle of a JSON value."
	case errors.As(err, &syntaxErr):
		return http.StatusBadRequest, "invalid_json", fmt.Sprintf("Malformed JSON at byte %d.", syntaxErr.Offset)
	case errors.As(err, &typeErr):
		return http.StatusBadRequest, "invalid_json", fmt.Sprintf("Field %q must be %s.", typeErr.Field, jsonTypeName(typeErr.Type.Kind().String()))
	case errors.As(err, &timeParseErr):
		return http.StatusBadRequest, "invalid_json", "Timestamps must use RFC 3339, e.g. 2026-11-06T10:00:00Z."
	case errors.As(err, &tooLargeErr):
		return http.StatusRequestEntityTooLarge, "body_too_large", fmt.Sprintf("The body must be at most %d bytes.", tooLargeErr.Limit)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// encoding/json has no error type for this case, only this message.
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return http.StatusBadRequest, "unknown_field", fmt.Sprintf("Unknown field %s.", field)
	default:
		return http.StatusBadRequest, "invalid_json", "The body is not valid JSON for this endpoint."
	}
}

// jsonTypeName maps Go kinds to JSON words: a client sends "an object", not "a map".
func jsonTypeName(goKind string) string {
	switch goKind {
	case "string":
		return "a string"
	case "map", "struct":
		return "an object"
	case "slice", "array":
		return "an array"
	case "bool":
		return "true or false"
	case "int", "int64", "float64":
		return "a number"
	default:
		return "a different type"
	}
}
