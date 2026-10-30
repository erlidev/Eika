//go:build docker

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/executor/sandbox"
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
func requestOn(ctx context.Context, t *testing.T, s *server.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	r := httptest.NewRequestWithContext(ctx, method, path, bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	checkContract(t, r, rec)
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
	// createErr and startErr fail the matching call when set.
	createErr  error
	startErr   error
	inspectErr error
	// images are the images HasImage reports present; imageErr fails it,
	// the way a Docker socket the harness cannot use does.
	images   map[string]bool
	imageErr error
	// startHook runs before Start answers, which is how a test acts in the
	// middle of a creation the harness has already got a container out of.
	startHook func()
	// process starts what Process is asked for, which is how a test puts a
	// stdio MCP server in a workspace; processes records where.
	process   func(spec sandbox.ProcessSpec) (io.ReadWriteCloser, error)
	processes []string
	// egressControl is what EgressControl reports: whether the host has an
	// internal network to put a restricted workspace on.
	egressControl bool
	// confined records every confinement a workspace was created or
	// confined with, in order, by workspace id.
	confined map[string][]workspace.Confinement
	// ports are the base URLs PortURL answers with, by port, which is how a
	// test puts a server behind a workspace's port.
	ports map[int]string
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
	return &fakeHost{
		root: t.TempDir(), workspaces: map[string]*fakeWorkspace{},
		confined: map[string][]workspace.Confinement{}, ports: map[int]string{},
	}
}

// filesOf returns the directory the files of a workspace the host reported
// are in: its container's directory, narrowed to a worktree as the real
// host's daemon client is.
func (h *fakeHost) filesOf(ws workspace.Workspace) (string, error) {
	dir, err := h.dirOf(ws.ID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.FromSlash(ws.Dir)), nil
}

// dirOf returns the directory a workspace's container holds.
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
		return "", fmt.Errorf("git %s in %s: %w: %s", args[0], dir, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// Create makes the workspace's directory.
func (h *fakeHost) Create(_ context.Context, spec workspace.Spec) (workspace.Workspace, error) {
	if h.createErr != nil {
		return workspace.Workspace{}, h.createErr
	}
	if spec.Confinement.Proxied && !h.EgressControl() {
		return workspace.Workspace{}, workspace.ErrNoEgressControl
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
		HostPath:    spec.HostPath,
		ProxyToken:  "proxy-token-" + spec.ID,
		Proxied:     spec.Confinement.Proxied,
	}
	if ws.Image == "" {
		ws.Image = "eika-sandbox:latest"
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.workspaces[ws.ID] = &fakeWorkspace{ws: ws, dir: dir}
	h.confined[ws.ID] = append(h.confined[ws.ID], spec.Confinement)
	return ws, nil
}

// Confine records the confinement a workspace was given.
func (h *fakeHost) Confine(_ context.Context, ws *workspace.Workspace, c workspace.Confinement) error {
	if c.Proxied && !h.EgressControl() {
		return workspace.ErrNoEgressControl
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	found, ok := h.workspaces[ws.ID]
	if !ok {
		return fmt.Errorf("%w: %s", workspace.ErrNoWorkspace, ws.ID)
	}
	found.ws.Proxied, ws.Proxied = c.Proxied, c.Proxied
	h.confined[ws.ID] = append(h.confined[ws.ID], c)
	return nil
}

// confinements returns what a workspace was created and confined with.
func (h *fakeHost) confinements(id string) []workspace.Confinement {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.confined[id])
}

// Usage reports a fixed sample of a running workspace.
func (h *fakeHost) Usage(context.Context, workspace.Workspace) (workspace.Usage, error) {
	return workspace.Usage{CPUPercent: 42, MemoryBytes: 256 << 20, MemoryLimitBytes: 1 << 30, PIDs: 7}, nil
}

// Capacity is a host of four cores and 8 GiB.
func (h *fakeHost) Capacity(context.Context) (workspace.Capacity, error) {
	return workspace.Capacity{CPUs: 4, MemoryBytes: 8 << 30}, nil
}

// EgressControl reports what the test set.
func (h *fakeHost) EgressControl() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.egressControl
}

// PortURL answers with the server the test put behind a port.
func (h *fakeHost) PortURL(_ context.Context, ws workspace.Workspace, port int) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	base, ok := h.ports[port]
	if !ok {
		return "", fmt.Errorf("workspace %s has nothing on port %d", ws.ID, port)
	}
	return base, nil
}

// Start marks the workspace running.
func (h *fakeHost) Start(_ context.Context, ws *workspace.Workspace) error {
	if h.startHook != nil {
		h.startHook()
	}
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

// Executor returns an executor on the workspace's directory, or on the
// worktree it is narrowed to.
func (h *fakeHost) Executor(ws workspace.Workspace) (executor.Executor, error) {
	dir, err := h.filesOf(ws)
	if err != nil {
		return nil, err
	}
	return local.New(dir)
}

// Terminal is refused: the terminal proxy has tests of its own, against a
// scripted sandbox, that need no database.
func (h *fakeHost) Terminal(_ context.Context, ws workspace.Workspace, _, _ uint16) (*websocket.Conn, error) {
	return nil, fmt.Errorf("the fake host has no terminal for %s", ws.ID)
}

// Process starts what the test scripted, in a workspace the host has.
func (h *fakeHost) Process(_ context.Context, ws workspace.Workspace, spec sandbox.ProcessSpec, _ func(string)) (io.ReadWriteCloser, error) {
	h.mu.Lock()
	_, ok := h.workspaces[ws.ID]
	start := h.process
	if ok {
		h.processes = append(h.processes, ws.ID)
	}
	h.mu.Unlock()
	switch {
	case !ok:
		return nil, fmt.Errorf("%w: %s", workspace.ErrNoWorkspace, ws.ID)
	case start == nil:
		return nil, fmt.Errorf("the fake host runs no %s", spec.Command)
	}
	return start(spec)
}

// started returns the workspaces a process was started in, in order.
func (h *fakeHost) started() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.processes)
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

// HasImage reports the images the test said are there.
func (h *fakeHost) HasImage(_ context.Context, ref string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.imageErr != nil {
		return false, h.imageErr
	}
	return h.images[ref], nil
}

// fakeProviders builds whatever provider the test scripted, for any endpoint
// of the one kind the harness knows.
type fakeProviders struct {
	build func(kind string, e provider.Endpoint) (provider.Provider, error)
}

// Kinds lists the kinds the real registry has.
func (fakeProviders) Kinds() []string { return []string{"openai"} }

// Build returns the scripted provider.
func (p fakeProviders) Build(kind string, e provider.Endpoint) (provider.Provider, error) {
	if p.build == nil {
		return nil, fmt.Errorf("no provider for kind %s", kind)
	}
	return p.build(kind, e)
}

var (
	_ server.Workspaces = (*fakeHost)(nil)
	_ server.Providers  = fakeProviders{}
)

// branchShow reads one path out of a branch in a workspace's repository, which
// is how a test checks the work a child left on its own branch.
func (h *fakeHost) branchShow(ctx context.Context, workspaceID, branch, path string) (string, error) {
	dir, err := h.dirOf(workspaceID)
	if err != nil {
		return "", err
	}
	return git(ctx, dir, "show", branch+":"+path)
}
