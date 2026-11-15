package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Invalid overrides are rejected before the handler accesses the store.
func TestAgentOverrideRejectsInvalidPromptAndFields(t *testing.T) {
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, body := range []string{`{"prompt":"before\u0000after"}`, `{"tools":["bash"]}`, "{\"prompt\":\"bad\xfftext\"}"} {
		t.Run(body, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, "/api/agents/eika", strings.NewReader(body))
			r.SetPathValue("name", "eika")
			w := httptest.NewRecorder()
			s.handlePutAgent(w, r)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"invalid_request"`) {
				t.Fatalf("response = %d %s, want invalid_request", w.Code, w.Body.String())
			}
		})
	}
}
