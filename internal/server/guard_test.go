package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/erlidev/eika/internal/sentry"
)

func TestGuardHealthAPI(t *testing.T) {
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil)), gate: sentry.New(sentry.Options{})}
	w := httptest.NewRecorder()
	s.handleGuard(w, httptest.NewRequest("GET", "/api/guard", nil))
	var result guardResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Status != "unknown" {
		t.Fatalf("response %s: %v", w.Body, err)
	}
}
