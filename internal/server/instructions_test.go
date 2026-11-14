package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstructionWritesRejectMalformedBodiesBeforeStorage(t *testing.T) {
	s := &Server{log: slog.Default()}
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		bodies  []string
	}{
		{"global", s.handlePutInstructions, []string{`null`, `{}`, `{"about_me":"a"}`, `{"about_me":"a","rules":null}`, `{"about_me":"a","rules":4}`, `{"about_me":"a","rules":"b","project_id":"p"}`}},
		{"project", s.handlePutProjectInstructions, []string{`null`, `{}`, `{"instructions":null}`, `{"instructions":4}`, `{"instructions":"a","rules":"b"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, body := range tc.bodies {
				rec := httptest.NewRecorder()
				tc.handler(rec, httptest.NewRequest("PUT", "/api/instructions", strings.NewReader(body)))
				if rec.Code != 400 {
					t.Fatalf("body %s: status %d", body, rec.Code)
				}
			}
		})
	}
}
