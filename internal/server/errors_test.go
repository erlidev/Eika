package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
	"github.com/erlidev/eika/internal/workspace"
	"github.com/erlidev/eika/internal/workspace/hub"
)

// Error mapping is tested from inside the package: it is the one rule every
// handler relies on and it has no route of its own.

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"missing row", fmt.Errorf("read project p1: %w", store.ErrNotFound), http.StatusNotFound, codeNotFound},
		{"missing container", fmt.Errorf("%w: w1", workspace.ErrNoWorkspace), http.StatusNotFound, codeNotFound},
		{"missing hub project", fmt.Errorf("%w: demo", hub.ErrNoProject), http.StatusNotFound, codeNotFound},
		{"answered question", fmt.Errorf("answer question q1: %w", builtin.ErrNoQuestion), http.StatusNotFound, codeNotFound},
		{"duplicate row", fmt.Errorf("create project demo: %w", store.ErrConflict), http.StatusConflict, codeConflict},
		{"bad project name", fmt.Errorf("%w: %q", hub.ErrBadProject, "../etc"), http.StatusBadRequest, codeInvalidRequest},
		{"bad branch name", fmt.Errorf("%w: %q", workspace.ErrBadBranch, "-x"), http.StatusBadRequest, codeInvalidRequest},
		{"bad answer", fmt.Errorf("answer question q1: %w", builtin.ErrBadAnswer), http.StatusBadRequest, codeInvalidRequest},
		{"validation", invalidf("name is required"), http.StatusBadRequest, codeInvalidRequest},
		{"not found", notFoundf("no run is waiting"), http.StatusNotFound, codeNotFound},
		{"conflict", conflictf("session s1 already has a run in progress"), http.StatusConflict, codeConflict},
		{"anything else", errors.New("connection reset"), http.StatusInternalServerError, codeInternal},
	}
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, code := statusOf(c.err)
			if status != c.status || code != c.code {
				t.Errorf("statusOf = %d %q, want %d %q", status, code, c.status, c.code)
			}

			rec := httptest.NewRecorder()
			s.fail(rec, httptest.NewRequest(http.MethodGet, "/api/projects", nil), c.err)
			if rec.Code != c.status {
				t.Fatalf("response status = %d, want %d", rec.Code, c.status)
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %q: %v", rec.Body.String(), err)
			}
			if body.Error.Code != c.code {
				t.Errorf("code = %q, want %q", body.Error.Code, c.code)
			}
			want := c.err.Error()
			if c.status == http.StatusInternalServerError {
				// An internal failure never tells the client what broke.
				want = "internal error"
			}
			if body.Error.Message != want {
				t.Errorf("message = %q, want %q", body.Error.Message, want)
			}
		})
	}
}
