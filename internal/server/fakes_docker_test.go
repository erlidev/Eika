//go:build docker

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// package's own docker-tagged tests. Its hub is a directory of bare
// repositories and real git commands, so that cloning, pushing, and merging
// behave as they do in a sandbox.
type fakeHost struct {
	root string
	// hub holds one bare repository per project, which Push, CloneAt, and
	// Fetch talk to with the git binary.
	hub string

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
	return &fakeHost{root: t.TempDir(), hub: t.TempDir(), workspaces: map[string]*fakeWorkspace{}}
}

// CloneAt clones the project from the fake hub and puts the workspace on
// branch at commit.
func (h *fakeHost) CloneAt(ctx context.Context, ws workspace.Workspace, project, branch, commit string) (string, error) {
	dir, err := h.dirOf(ws.ID)
	if err != nil {
		return "", err
	}
	repo, err := h.repo(ctx, project)
	if err != nil {
		return "", err
	}
	if h.cloneHook != nil {
		h.cloneHook()
	}
	if _, err := git(ctx, dir, "clone", repo, "."); err != nil {
		return "", err
	}
	// The real host configures the identity inside the container before it
	// clones, so a clone here is committable too.
	for _, args := range [][]string{
		{"config", "user.name", "Eika Test"},
		{"config", "user.email", "test@eika.local"},
	} {
		if _, err := git(ctx, dir, args...); err != nil {
			return "", err
		}
	}
	args := []string{"checkout", "-B", branch}
	if commit != "" {
		args = append(args, commit)
	}
	if _, err := git(ctx, dir, args...); err != nil {
		return "", err
	}
	return git(ctx, dir, "rev-parse", "HEAD")
}

// hubShow reads one path out of a branch in the fake hub, which is how a test
// checks that a workspace's work actually arrived there.
func (h *fakeHost) hubShow(ctx context.Context, project, branch, path string) (string, error) {
	repo, err := h.repo(ctx, project)
	if err != nil {
		return "", err
	}
	return git(ctx, repo, "show", branch+":"+path)
}

// Push sends a workspace's branch to the fake hub, creating the project's
// repository when it has none.
func (h *fakeHost) Push(ctx context.Context, ws workspace.Workspace, project, branch string) error {
	dir, err := h.dirOf(ws.ID)
	if err != nil {
		return err
	}
	repo, err := h.repo(ctx, project)
	if err != nil {
		return err
	}
	_, err = git(ctx, dir, "push", repo, "HEAD:refs/heads/"+branch)
	return err
}

// Fetch brings a branch from the fake hub into a workspace.
func (h *fakeHost) Fetch(ctx context.Context, ws workspace.Workspace, project, branch string) error {
	dir, err := h.dirOf(ws.ID)
	if err != nil {
		return err
	}
	repo, err := h.repo(ctx, project)
	if err != nil {
		return err
	}
	_, err = git(ctx, dir, "fetch", repo, branch)
	return err
}

// repo returns the project's bare repository in the fake hub, creating it the
// first time it is asked for.
func (h *fakeHost) repo(ctx context.Context, project string) (string, error) {
	path := filepath.Join(h.hub, project+".git")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if _, err := git(ctx, h.hub, "init", "--bare", "--initial-branch=main", path); err != nil {
		return "", err
	}
	return path, nil
}

// dirOf returns the directory a workspace's files live in.
func (h *fakeHost) dirOf(id string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	found, ok := h.workspaces[id]
	if !ok {
		return "", fmt.Errorf("%w: %s", workspace.ErrNoWorkspace, id)
	}
	return found.dir, nil
}

// git runs one git command in a directory of the test's own tree. The
// identity is passed on the command line so that the machine running the
// tests needs no git configuration.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{
		"-c", "user.name=Eika Test",
		"-c", "user.email=test@eika.local",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %v: %s", args[0], dir, err, out)
	}
	return strings.TrimSpace(string(out)), nil
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
