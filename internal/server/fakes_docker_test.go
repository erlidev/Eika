//go:build docker

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/executor/sandbox"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/server"
)

// request sends one API request with the bearer token and returns the
// recorded response.
func request(t *testing.T, s *server.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return requestWith(t, s, method, path, body, "Bearer "+testToken)
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

// fakeHost is a computer host whose computers are directories in a
// temporary tree. It is what lets the handler tests exercise every route
// without a Docker daemon; the real host is covered by the computer
// package's own docker-tagged tests.
type fakeHost struct {
	root string

	mu        sync.Mutex
	computers map[string]*fakeComputer
	// createErr, confineErr and inspectErr fail the matching call when set.
	createErr  error
	confineErr error
	inspectErr error
	// process starts what Process is asked for, which is how a test puts a
	// stdio MCP server in a computer; processes records where.
	process   func(spec sandbox.ProcessSpec) (io.ReadWriteCloser, error)
	processes []string
	// egressControl is what EgressControl reports: whether the host has an
	// internal network to put a restricted computer on.
	egressControl bool
	// confined records every confinement a computer was created or
	// confined with, in order, by computer id.
	confined map[string][]computer.Confinement
	// created counts the containers made under each computer id, so that a
	// container made again gets a proxy token of its own, as a real one does.
	created map[string]int
}

// fakeComputer is one computer of a fakeHost.
type fakeComputer struct {
	cx  computer.Computer
	dir string
}

// newFakeHost returns a host whose computers live under a temporary
// directory the test owns.
func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	return &fakeHost{
		root: t.TempDir(), computers: map[string]*fakeComputer{},
		confined: map[string][]computer.Confinement{}, created: map[string]int{},
	}
}

// failConfine makes every Confine fail with err until it is called with nil,
// the way a Docker daemon that refuses a container update does.
func (h *fakeHost) failConfine(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.confineErr = err
}

// failCreate makes every Create fail with err until it is called with nil,
// the way a sandbox image that is not built yet does.
func (h *fakeHost) failCreate(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.createErr = err
}

// filesOf returns the directory the files of a computer the host reported
// are in: its container's directory.
func (h *fakeHost) filesOf(cx computer.Computer) (string, error) {
	return h.dirOf(cx.ID)
}

// dirOf returns the directory a computer's container holds.
func (h *fakeHost) dirOf(id string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	found, ok := h.computers[id]
	if !ok {
		return "", fmt.Errorf("%w: %s", computer.ErrNoComputer, id)
	}
	return found.dir, nil
}

// Create makes the computer's directory.
func (h *fakeHost) Create(_ context.Context, spec computer.Spec) (computer.Computer, error) {
	h.mu.Lock()
	createErr := h.createErr
	h.mu.Unlock()
	if createErr != nil {
		return computer.Computer{}, createErr
	}
	if spec.Confinement.Proxied && !h.EgressControl() {
		return computer.Computer{}, computer.ErrNoEgressControl
	}
	dir := filepath.Join(h.root, spec.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return computer.Computer{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.created[spec.ID]++
	cx := computer.Computer{
		ID:          spec.ID,
		ContainerID: "container-" + spec.ID,
		Image:       spec.Image,
		State:       computer.StateCreating,
		ProxyToken:  fmt.Sprintf("proxy-token-%s-%d", spec.ID, h.created[spec.ID]),
		Proxied:     spec.Confinement.Proxied,
	}
	if cx.Image == "" {
		cx.Image = "eika-sandbox:latest"
	}
	h.computers[cx.ID] = &fakeComputer{cx: cx, dir: dir}
	h.confined[cx.ID] = append(h.confined[cx.ID], spec.Confinement)
	return cx, nil
}

// Confine records the confinement a computer was given.
func (h *fakeHost) Confine(_ context.Context, cx *computer.Computer, c computer.Confinement) error {
	h.mu.Lock()
	confineErr := h.confineErr
	h.mu.Unlock()
	if confineErr != nil {
		return confineErr
	}
	if c.Proxied && !h.EgressControl() {
		return computer.ErrNoEgressControl
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	found, ok := h.computers[cx.ID]
	if !ok {
		return fmt.Errorf("%w: %s", computer.ErrNoComputer, cx.ID)
	}
	found.cx.Proxied, cx.Proxied = c.Proxied, c.Proxied
	h.confined[cx.ID] = append(h.confined[cx.ID], c)
	return nil
}

// confinements returns what a computer was created and confined with.
func (h *fakeHost) confinements(id string) []computer.Confinement {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.confined[id])
}

// proxyToken returns the egress proxy token of a computer's container.
func (h *fakeHost) proxyToken(id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if found, ok := h.computers[id]; ok {
		return found.cx.ProxyToken
	}
	return ""
}

// Capacity is a host of four cores and 8 GiB.
func (h *fakeHost) Capacity(context.Context) (computer.Capacity, error) {
	return computer.Capacity{CPUs: 4, MemoryBytes: 8 << 30}, nil
}

// EgressControl reports what the test set.
func (h *fakeHost) EgressControl() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.egressControl
}

// Start marks the computer running.
func (h *fakeHost) Start(_ context.Context, cx *computer.Computer) error {
	return h.update(cx, computer.StateRunning)
}

// Stop marks the computer stopped.
func (h *fakeHost) Stop(_ context.Context, cx *computer.Computer) error {
	return h.update(cx, computer.StateStopped)
}

// lose forgets a computer and removes its directory, as a container lost
// while the harness was down would be.
func (h *fakeHost) lose(t *testing.T, id string) {
	t.Helper()
	h.mu.Lock()
	found, ok := h.computers[id]
	delete(h.computers, id)
	h.mu.Unlock()
	if ok {
		if err := os.RemoveAll(found.dir); err != nil {
			t.Fatalf("remove the computer's directory: %v", err)
		}
	}
}

// Inspect returns one computer.
func (h *fakeHost) Inspect(_ context.Context, id string) (computer.Computer, error) {
	if h.inspectErr != nil {
		return computer.Computer{}, h.inspectErr
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	found, ok := h.computers[id]
	if !ok {
		return computer.Computer{}, fmt.Errorf("%w: %s", computer.ErrNoComputer, id)
	}
	return found.cx, nil
}

// count returns how many computers the host has.
func (h *fakeHost) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.computers)
}

// Executor returns an executor on the computer's directory.
func (h *fakeHost) Executor(cx computer.Computer) (executor.Executor, error) {
	dir, err := h.filesOf(cx)
	if err != nil {
		return nil, err
	}
	return local.New(dir)
}

// Process starts what the test scripted, in a computer the host has.
func (h *fakeHost) Process(_ context.Context, cx computer.Computer, spec sandbox.ProcessSpec, _ func(string)) (io.ReadWriteCloser, error) {
	h.mu.Lock()
	_, ok := h.computers[cx.ID]
	start := h.process
	if ok {
		h.processes = append(h.processes, cx.ID)
	}
	h.mu.Unlock()
	switch {
	case !ok:
		return nil, fmt.Errorf("%w: %s", computer.ErrNoComputer, cx.ID)
	case start == nil:
		return nil, fmt.Errorf("the fake host runs no %s", spec.Command)
	}
	return start(spec)
}

// started returns the computers a process was started in, in order.
func (h *fakeHost) started() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.processes)
}

// update records a new state for a computer.
func (h *fakeHost) update(cx *computer.Computer, state computer.State) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	found, ok := h.computers[cx.ID]
	if !ok {
		return fmt.Errorf("%w: %s", computer.ErrNoComputer, cx.ID)
	}
	found.cx.State = state
	cx.State = state
	return nil
}

// HasImage reports no image present.
func (h *fakeHost) HasImage(context.Context, string) (bool, error) { return false, nil }

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
	_ server.Computers = (*fakeHost)(nil)
	_ server.Providers = fakeProviders{}
)
