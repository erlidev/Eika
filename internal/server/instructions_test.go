package server

import (
	"errors"
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
		{"global", s.handlePutInstructions, []string{`null`, `{}`, `{"about_me":"a"}`, `{"about_me":"a","rules":null}`, `{"about_me":"a","rules":4}`, `{"about_me":"a","rules":"b","project_id":"p"}`, "{\"about_me\":\"\xff\",\"rules\":\"ok\"}", "{\"about_me\":\"ok\",\"rules\":\"\xff\"}"}},
		{"project", s.handlePutProjectInstructions, []string{`null`, `{}`, `{"instructions":null}`, `{"instructions":4}`, `{"instructions":"a","rules":"b"}`, "{\"instructions\":\"\xff\"}"}},
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

func TestDecodeInstructionJSONPreservesValidUnicodeAndLimitsBody(t *testing.T) {
	request := httptest.NewRequest("PUT", "/", strings.NewReader(`{"instructions":"Café 🌍"}`))
	got, err := decodeInstructionJSON[putProjectInstructionsRequest](request)
	if err != nil || got.Instructions == nil || *got.Instructions != "Café 🌍" {
		t.Fatalf("decoded = %+v, error = %v", got, err)
	}
	request = httptest.NewRequest("PUT", "/", strings.NewReader(strings.Repeat("a", maxRequestBytes+1)))
	_, err = decodeInstructionJSON[putProjectInstructionsRequest](request)
	var e apiError
	if !errors.As(err, &e) || e.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %v", err)
	}
}
