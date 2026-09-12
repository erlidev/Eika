package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSON writes v as a JSON response with the given status code. An
// encoding failure is logged rather than returned because the status line has
// already been sent.
func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Error("write json response", "error", err)
	}
}
