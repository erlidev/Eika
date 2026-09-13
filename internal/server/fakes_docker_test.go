//go:build docker

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/workspace"
)

// request sends one API request with the bearer token and returns the
// recorded response.
func request(t *testing.T, s *server.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return requestWith(t, s, method, path, body, "Bearer "+testToken)
}

// requestOn sends one API request on a context of the caller's choosing,
// which is how a test stands in for a client that gave up.
func requestOn(t *testing.T, ctx context.Context, s *server.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	r := httptest.NewRequestWithContext(ctx, method, path, bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

// decodeBody decodes a JSON response body into T, failing the test when the
// status is not the expected one.
func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return v
}

// fakeHost is a workspace host whose workspaces are directories in a
// temporary tree. It is what lets the handler tests exercise every route
// without a Docker daemon; the real host is covered by the workspace
// package's own docker-tagged tests.
type fakeHost struct {
	root string

	mu         sync.Mutex
	workspaces map[string]*fakeWorkspace
	// baseCommit is what Clone reports as the commit a workspace starts from.
	baseCommit string
	// createErr, startErr, and cloneErr fail the matching call when set.
	createErr  error
	startErr   error
	cloneErr   error
	inspectErr error
	// cloneHook runs before Clone answers, which is how a test acts in the
	// middle of a creation the harness has already got a container out of.
	cloneHook func()
}

// fakeWorkspace is one workspace of a fakeHost.
type fakeWorkspace struct {
	ws  workspace.Workspace
	dir string
}

// newFakeHost returns a host whose workspaces live under a temporary
// directory the test owns.
func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	return &fakeHost{root: t.TempDir(), workspaces: map[string]*fakeWorkspace{}}
}

// Create makes the workspace's directory.
func (h *fakeHost) Create(_ context.Context, spec workspace.Spec) (workspace.Workspace, error) {
	if h.createErr != nil {
		return workspace.Workspace{}, h.createErr
	}
	dir := spec.HostPath
	if dir == "" {
		dir = filepath.Join(h.root, spec.ID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return workspace.Workspace{}, err
		}
	}
	ws := workspace.Workspace{
		ID:          spec.ID,
		ContainerID: "container-" + spec.ID,
		Image:       spec.Image,
		State:       workspace.StateCreating,
		Project:     spec.Project,
		HostPath:    spec.HostPath,
	}
	if ws.Image == "" {
		ws.Image = "eika-sandbox:latest"
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.workspaces[ws.ID] = &fakeWorkspace{ws: ws, dir: dir}
	return ws, nil
}

// Start marks the workspace running.
func (h *fakeHost) Start(_ context.Context, ws *workspace.Workspace) error {
	if h.startErr != nil {
		return h.startErr
	}
	return h.update(ws, workspace.StateRunning)
}

// Stop marks the workspace stopped.
func (h *fakeHost) Stop(_ context.Context, ws *workspace.Workspace) error {
	return h.update(ws, workspace.StateStopped)
}

// Destroy forgets the workspace and removes its directory. It honours the
// context, as a Docker client does, so that a cleanup running on the request
// that failed would be seen to fail with it.
func (h *fakeHost) Destroy(ctx context.Context, ws *workspace.Workspace) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	found, ok := h.workspaces[ws.ID]
	delete(h.workspaces, ws.ID)
	h.mu.Unlock()
	if ok && found.ws.HostPath == "" {
		if err := os.RemoveAll(found.dir); err != nil {
			return err
		}
	}
	ws.State = workspace.StateGone
	return nil
}

// Inspect returns one workspace.
func (h *fakeHost) Inspect(_ context.Context, id string) (workspace.Workspace, error) {
	if h.inspectErr != nil {
		return workspace.Workspace{}, h.inspectErr
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	found, ok := h.workspaces[id]
	if !ok {
		return workspace.Workspace{}, fmt.Errorf("%w: %s", workspace.ErrNoWorkspace, id)
	}
	return found.ws, nil
}

// List returns every workspace the host has.
func (h *fakeHost) List(context.Context) ([]workspace.Workspace, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]workspace.Workspace, 0, len(h.workspaces))
	for _, found := range h.workspaces {
		out = append(out, found.ws)
	}
	return out, nil
}

// Clone reports the configured base commit without touching git.
func (h *fakeHost) Clone(context.Context, workspace.Workspace, string, string) (string, error) {
	if h.cloneHook != nil {
		h.cloneHook()
	}
	if h.cloneErr != nil {
		return "", h.cloneErr
	}
	return h.baseCommit, nil
}

// Executor returns an executor on the workspace's directory.
func (h *fakeHost) Executor(ws workspace.Workspace) (executor.Executor, error) {
	h.mu.Lock()
	found, ok := h.workspaces[ws.ID]
	h.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", workspace.ErrNoWorkspace, ws.ID)
	}
	return local.New(found.dir)
}

// update records a new state for a workspace.
func (h *fakeHost) update(ws *workspace.Workspace, state workspace.State) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	found, ok := h.workspaces[ws.ID]
	if !ok {
		return fmt.Errorf("%w: %s", workspace.ErrNoWorkspace, ws.ID)
	}
	found.ws.State = state
	ws.State = state
	return nil
}

// fakeModels hands out scripted providers.
type fakeModels struct {
	names []string
	build func(name string) (provider.Provider, error)
}

// Names lists the model names the test configured.
func (m fakeModels) Names() []string { return m.names }

// Provider returns the scripted provider for a model.
func (m fakeModels) Provider(name string) (provider.Provider, error) {
	if m.build == nil {
		return nil, fmt.Errorf("no provider for model %s", name)
	}
	return m.build(name)
}

var (
	_ server.Workspaces = (*fakeHost)(nil)
	_ server.Models     = fakeModels{}
)
