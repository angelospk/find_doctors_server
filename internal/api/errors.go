package api

import (
	"encoding/json"
	"net/http"
)

// errorBody is the standard JSON error envelope returned by the API.
type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// writeJSONError sends a structured JSON error response with the given status.
// code is a short, stable, machine-friendly identifier ("missing_param",
// "invalid_id", "upstream_failure", ...). message is human-friendly.
func writeJSONError(w http.ResponseWriter, status int, code, message string, details ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := errorBody{Error: errorPayload{Code: code, Message: message}}
	if len(details) > 0 {
		body.Error.Details = details[0]
	}
	_ = json.NewEncoder(w).Encode(body)
}

// writeJSON sends a JSON success response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
