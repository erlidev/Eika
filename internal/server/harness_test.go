package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/workspace/hub"
)

// testToken is the bearer token every test server is configured with.
const testToken = "test-token"

// testConfig returns a configuration with the test token and one model.
func testConfig() config.Config {
	cfg := config.Default()
	cfg.AuthToken = testToken
	cfg.Models = []config.Model{{
		Name:          "test-model",
		BaseURL:       "http://model.invalid",
		APIKeyEnv:     "EIKA_TEST_KEY",
		ContextWindow: 8192,
		MaxOutput:     1024,
	}}
	return cfg
}

// requestWith sends one API request with the given Authorization header,
// which may be empty.
func requestWith(t *testing.T, s *server.Server, method, path string, body any, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, reader)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

// errorOf decodes the API's error body.
func errorOf(t *testing.T, rec *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code, body.Error.Message
}

// fakeHub records what the API asked of the git hub.
type fakeHub struct {
	mu          sync.Mutex
	projects    []string
	mirrored    map[string]string
	credentials map[string]hub.Credentials
	initErr     error
}

// newFakeHub returns an empty hub.
func newFakeHub() *fakeHub {
	return &fakeHub{
		mirrored:    map[string]string{},
		credentials: map[string]hub.Credentials{},
	}
}

// Init records that a project's repository was asked for. It rejects a name
// no directory can have, as the real hub does, because that check is what the
// API relies on to validate a project name.
func (h *fakeHub) Init(_ context.Context, project string) (string, error) {
	if h.initErr != nil {
		return "", h.initErr
	}
	if project == "" || strings.ContainsAny(project, "/\\") || strings.Contains(project, "..") {
		return "", fmt.Errorf("%w: %q", hub.ErrBadProject, project)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.projects = append(h.projects, project)
	return "/hub/" + project + ".git", nil
}

// Mirror records the remote a project mirrors.
func (h *fakeHub) Mirror(_ context.Context, project, remoteURL string, creds hub.Credentials) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mirrored[project] = remoteURL
	h.credentials[project] = creds
	return nil
}

// Handler stands in for git's Smart HTTP backend, which authenticates
// workspaces itself.
func (h *fakeHub) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("git"))
	})
}

var _ server.Hub = (*fakeHub)(nil)
