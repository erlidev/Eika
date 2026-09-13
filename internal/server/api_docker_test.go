//go:build docker

package server_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/erlidev/eika/internal/tool/builtin"
)

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m))
}

// api is a server under test: the real store on a throwaway database, a
// workspace host backed by temporary directories, and a provider the test
// scripts per run.
type api struct {
	*server.Server
	store     *store.Store
	host      *fakeHost
	hub       *fakeHub
	questions *builtin.Questions

	mu       sync.Mutex
	provider provider.Provider
}

// newAPI returns a server wired to a database of this test's own.
func newAPI(t *testing.T) *api {
	t.Helper()
	st := storetest.Open(t)
	a := &api{store: st, host: newFakeHost(t), hub: newFakeHub(), questions: builtin.NewQuestions()}
	tools, err := builtin.Registry(a.questions)
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	a.Server = server.New(testConfig(), testLogger(), server.Deps{
		Store:      st,
		Hub:        a.hub,
		Workspaces: a.host,
		Models:     fakeModels{names: []string{"test-model"}, build: a.buildProvider},
		Tools:      tools,
		Questions:  a.questions,
	}, server.Options{})
	t.Cleanup(a.Server.Close)
	return a
}

// script makes steps the responses the next run gets.
func (a *api) script(steps ...providertest.Step) *providertest.Provider {
	p := providertest.New(steps...)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.provider = p
	return p
}

// buildProvider hands the run manager whatever the test scripted last.
func (a *api) buildProvider(name string) (provider.Provider, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.provider == nil {
		return nil, fmt.Errorf("no provider scripted for model %s", name)
	}
	return a.provider, nil
}

// newProject creates a local project on a directory the test owns and returns
// the project and that directory.
func (a *api) newProject(t *testing.T, name string) (projectWire, string) {
	t.Helper()
	dir := t.TempDir()
	rec := request(t, a.Server, "POST", "/api/projects", map[string]any{
		"name": name, "kind": "local", "host_path": dir,
	})
	return decodeBody[projectWire](t, rec, 201), dir
}

// newWorkspace creates a workspace in a project.
func (a *api) newWorkspace(t *testing.T, projectID string) workspaceWire {
	t.Helper()
	rec := request(t, a.Server, "POST", "/api/workspaces", map[string]any{
		"project_id": projectID, "name": "work",
	})
	return decodeBody[workspaceWire](t, rec, 201)
}

// newSession opens a session in a workspace.
func (a *api) newSession(t *testing.T, workspaceID string) sessionWire {
	t.Helper()
	rec := request(t, a.Server, "POST", "/api/sessions", map[string]any{
		"workspace_id": workspaceID, "title": "a session",
	})
	return decodeBody[sessionWire](t, rec, 201)
}

// runState reads what a session is doing.
func (a *api) runState(t *testing.T, sessionID string) runStateWire {
	t.Helper()
	rec := request(t, a.Server, "GET", "/api/sessions/"+sessionID+"/run", nil)
	return decodeBody[runStateWire](t, rec, 200)
}

// waitFor polls until cond holds, failing the test when it never does. The
// run manager works on its own goroutine, so every assertion about a run is
// an assertion about something that becomes true.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// waitIdle waits until the session has no run going.
func (a *api) waitIdle(t *testing.T, sessionID string) runStateWire {
	t.Helper()
	var state runStateWire
	waitFor(t, "the run to finish", func() bool {
		state = a.runState(t, sessionID)
		return !state.Active
	})
	return state
}

// waitQuestion waits until a run asks the user something.
func (a *api) waitQuestion(t *testing.T, sessionID string) builtin.Question {
	t.Helper()
	var q builtin.Question
	waitFor(t, "a question", func() bool {
		state := a.runState(t, sessionID)
		if len(state.Questions) == 0 {
			return false
		}
		q = state.Questions[0]
		return true
	})
	return q
}

// The wire shapes the tests decode. They are written out rather than shared
// with the handlers so that a change to a handler's struct that changes the
// JSON fails a test instead of passing quietly.
type projectWire struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	RemoteURL     string `json:"remote_url"`
	HostPath      string `json:"host_path"`
	DefaultBranch string `json:"default_branch"`
}

type projectsWire struct {
	Projects []projectWire `json:"projects"`
}

type workspaceWire struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	Name       string `json:"name"`
	Branch     string `json:"branch"`
	BaseCommit string `json:"base_commit"`
	State      string `json:"state"`
}

type workspacesWire struct {
	Workspaces []workspaceWire `json:"workspaces"`
}

type diffWire struct {
	WorkspaceID string `json:"workspace_id"`
	BaseCommit  string `json:"base_commit"`
	Diff        string `json:"diff"`
	Status      string `json:"status"`
}

type sessionWire struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Title       string `json:"title"`
	HeadEntryID string `json:"head_entry_id"`
}

type sessionsWire struct {
	Sessions []sessionWire `json:"sessions"`
}

type entryWire struct {
	ID      string           `json:"id"`
	Kind    string           `json:"kind"`
	Commit  string           `json:"commit"`
	Message provider.Message `json:"message"`
}

type pathWire struct {
	SessionID string             `json:"session_id"`
	Entries   []entryWire        `json:"entries"`
	Messages  []provider.Message `json:"messages"`
}

type outlineWire struct {
	SessionID   string `json:"session_id"`
	HeadEntryID string `json:"head_entry_id"`
	Nodes       []struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Preview string `json:"preview"`
	} `json:"nodes"`
}

type runWire struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
	Error     string `json:"error"`
}

type runStateWire struct {
	SessionID        string             `json:"session_id"`
	Active           bool               `json:"active"`
	Run              *runWire           `json:"run"`
	PendingSteering  []string           `json:"pending_steering"`
	PendingFollowUps []string           `json:"pending_follow_ups"`
	Questions        []builtin.Question `json:"questions"`
}

type settingsWire struct {
	Settings map[string]json.RawMessage `json:"settings"`
}

type modelsWire struct {
	Models []struct {
		Name          string `json:"name"`
		ContextWindow int    `json:"context_window"`
	} `json:"models"`
	Default string `json:"default"`
}

func TestProjectRoutes(t *testing.T) {
	a := newAPI(t)

	created, dir := a.newProject(t, "demo")
	if created.Kind != "local" || created.HostPath != dir || created.DefaultBranch != "main" {
		t.Fatalf("created = %+v", created)
	}
	if len(a.hub.projects) != 1 || a.hub.projects[0] != "demo" {
		t.Errorf("hub projects = %v, want [demo]: a local project is mirrored too", a.hub.projects)
	}

	got := decodeBody[projectWire](t, request(t, a.Server, "GET", "/api/projects/"+created.ID, nil), 200)
	if got.ID != created.ID {
		t.Errorf("get id = %q, want %q", got.ID, created.ID)
	}
	list := decodeBody[projectsWire](t, request(t, a.Server, "GET", "/api/projects", nil), 200)
	if len(list.Projects) != 1 {
		t.Errorf("projects = %d, want 1", len(list.Projects))
	}

	remote := decodeBody[projectWire](t, request(t, a.Server, "POST", "/api/projects", map[string]any{
		"name": "upstream", "kind": "remote", "remote_url": "https://example.invalid/x.git",
		"default_branch": "trunk",
	}), 201)
	if remote.DefaultBranch != "trunk" {
		t.Errorf("default_branch = %q, want trunk", remote.DefaultBranch)
	}
	if a.hub.mirrored["upstream"] != "https://example.invalid/x.git" {
		t.Errorf("mirrored = %v, want the remote url", a.hub.mirrored)
	}

	if rec := request(t, a.Server, "DELETE", "/api/projects/"+created.ID, nil); rec.Code != 204 {
		t.Fatalf("delete status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, a.Server, "GET", "/api/projects/"+created.ID, nil); rec.Code != 404 {
		t.Errorf("get after delete = %d, want 404", rec.Code)
	}
}

func TestProjectRoutesRejectBadRequests(t *testing.T) {
	a := newAPI(t)
	existing, _ := a.newProject(t, "taken")

	cases := []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"unknown kind", map[string]any{"name": "x", "kind": "svn"}, 400, "invalid_request"},
		{"remote without a url", map[string]any{"name": "x", "kind": "remote"}, 400, "invalid_request"},
		{"local without a path", map[string]any{"name": "x", "kind": "local"}, 400, "invalid_request"},
		{"local with a relative path", map[string]any{"name": "x", "kind": "local", "host_path": "rel"}, 400, "invalid_request"},
		{"a name that is taken", map[string]any{"name": existing.Name, "kind": "local", "host_path": "/tmp"}, 409, "conflict"},
		{"a name no repository can have", map[string]any{"name": "../etc", "kind": "local", "host_path": "/tmp"}, 400, "invalid_request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := request(t, a.Server, "POST", "/api/projects", c.body)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.status, rec.Body.String())
			}
			if code, _ := errorOf(t, rec); code != c.code {
				t.Errorf("code = %q, want %q", code, c.code)
			}
		})
	}
	if rec := request(t, a.Server, "GET", "/api/projects/nope", nil); rec.Code != 404 {
		t.Errorf("unknown project = %d, want 404", rec.Code)
	}
}

func TestWorkspaceRoutes(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)

	ws := a.newWorkspace(t, project.ID)
	if ws.State != "running" || ws.Branch != "main" {
		t.Fatalf("created = %+v", ws)
	}
	if ws.BaseCommit == "" {
		t.Error("base_commit is empty: a local workspace records the commit it starts from")
	}

	list := decodeBody[workspacesWire](t, request(t, a.Server, "GET", "/api/workspaces?project_id="+project.ID, nil), 200)
	if len(list.Workspaces) != 1 || list.Workspaces[0].ID != ws.ID {
		t.Errorf("workspaces = %+v, want the one created", list.Workspaces)
	}
	empty := decodeBody[workspacesWire](t, request(t, a.Server, "GET", "/api/workspaces?project_id=other", nil), 200)
	if len(empty.Workspaces) != 0 {
		t.Errorf("workspaces of another project = %d, want 0", len(empty.Workspaces))
	}

	// The diff reports what the workspace changed since its base commit,
	// including the files git does not track yet.
	write(t, dir, "hello.txt", "changed\n")
	write(t, dir, "new.txt", "brand new\n")
	diff := decodeBody[diffWire](t, request(t, a.Server, "GET", "/api/workspaces/"+ws.ID+"/diff", nil), 200)
	if diff.BaseCommit != ws.BaseCommit {
		t.Errorf("base_commit = %q, want %q", diff.BaseCommit, ws.BaseCommit)
	}
	if !strings.Contains(diff.Diff, "changed") {
		t.Errorf("diff = %q, want the changed line", diff.Diff)
	}
	if !strings.Contains(diff.Status, "new.txt") {
		t.Errorf("status = %q, want the untracked file", diff.Status)
	}

	stopped := decodeBody[workspaceWire](t, request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/stop", nil), 200)
	if stopped.State != "stopped" {
		t.Errorf("state after stop = %q, want stopped", stopped.State)
	}
	if rec := request(t, a.Server, "GET", "/api/workspaces/"+ws.ID+"/diff", nil); rec.Code != 409 {
		t.Errorf("diff of a stopped workspace = %d, want 409", rec.Code)
	}
	started := decodeBody[workspaceWire](t, request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/start", nil), 200)
	if started.State != "running" {
		t.Errorf("state after start = %q, want running", started.State)
	}

	if rec := request(t, a.Server, "DELETE", "/api/workspaces/"+ws.ID, nil); rec.Code != 204 {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, a.Server, "GET", "/api/workspaces/"+ws.ID, nil); rec.Code != 404 {
		t.Errorf("get after delete = %d, want 404", rec.Code)
	}
}

func TestWorkspaceCreationLeavesNothingBehindWhenItFails(t *testing.T) {
	a := newAPI(t)
	project, _ := a.newProject(t, "demo")
	a.host.startErr = fmt.Errorf("no daemon")

	if rec := request(t, a.Server, "POST", "/api/workspaces", map[string]any{
		"project_id": project.ID, "name": "work",
	}); rec.Code != 500 {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	live, err := a.host.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(live) != 0 {
		t.Errorf("host holds %d workspaces, want none", len(live))
	}
	rows, err := a.store.Workspaces(t.Context(), project.ID)
	if err != nil {
		t.Fatalf("workspaces: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("store holds %d workspaces, want none", len(rows))
	}
}

func TestReconcileRecordsWhatTheHostActuallyHas(t *testing.T) {
	a := newAPI(t)
	project, _ := a.newProject(t, "demo")
	ws := a.newWorkspace(t, project.ID)

	// The container went away while the harness was down.
	host, err := a.host.Inspect(t.Context(), ws.ID)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if err := a.host.Destroy(t.Context(), &host); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if err := a.Server.Reconcile(t.Context()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := decodeBody[workspaceWire](t, request(t, a.Server, "GET", "/api/workspaces/"+ws.ID, nil), 200)
	if got.State != "gone" {
		t.Errorf("state = %q, want gone", got.State)
	}
}

func TestSessionRoutes(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)

	sess := a.newSession(t, ws.ID)
	if sess.WorkspaceID != ws.ID {
		t.Fatalf("session = %+v", sess)
	}
	list := decodeBody[sessionsWire](t, request(t, a.Server, "GET", "/api/sessions?workspace_id="+ws.ID, nil), 200)
	if len(list.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(list.Sessions))
	}

	a.script(providertest.Text("first answer"))
	if rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages",
		map[string]any{"text": "first question"}); rec.Code != 202 {
		t.Fatalf("post message = %d: %s", rec.Code, rec.Body.String())
	}
	a.waitIdle(t, sess.ID)

	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	if len(path.Entries) != 2 {
		t.Fatalf("entries = %d, want the question and the answer: %+v", len(path.Entries), path.Entries)
	}
	if path.Entries[0].Kind != "user" || path.Entries[1].Kind != "assistant" {
		t.Errorf("kinds = %q %q, want user assistant", path.Entries[0].Kind, path.Entries[1].Kind)
	}
	if path.Entries[1].Commit == "" {
		t.Error("the assistant entry recorded no commit, so a fork could not clone at it")
	}
	if len(path.Messages) != 2 || path.Messages[1].Content != "first answer" {
		t.Errorf("messages = %+v", path.Messages)
	}

	outline := decodeBody[outlineWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/outline", nil), 200)
	if len(outline.Nodes) != 2 || outline.HeadEntryID != path.Entries[1].ID {
		t.Errorf("outline = %+v", outline)
	}

	// Moving the head back to the question branches the tree in place.
	moved := decodeBody[sessionWire](t, request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/head",
		map[string]any{"entry_id": path.Entries[0].ID}), 200)
	if moved.HeadEntryID != path.Entries[0].ID {
		t.Errorf("head = %q, want the first entry", moved.HeadEntryID)
	}

	fork := decodeBody[sessionWire](t, request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/fork",
		map[string]any{"entry_id": path.Entries[1].ID, "title": "a fork"}), 201)
	if fork.ID == sess.ID || fork.Title != "a fork" || fork.WorkspaceID != ws.ID {
		t.Errorf("fork = %+v", fork)
	}
	forkPath := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+fork.ID+"/path", nil), 200)
	if len(forkPath.Entries) != 2 {
		t.Errorf("fork entries = %d, want the copied path", len(forkPath.Entries))
	}

	if rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/fork", map[string]any{"title": "no entry"}); rec.Code != 400 {
		t.Errorf("fork without an entry = %d, want 400", rec.Code)
	}
	if rec := request(t, a.Server, "DELETE", "/api/sessions/"+fork.ID, nil); rec.Code != 204 {
		t.Fatalf("delete = %d", rec.Code)
	}
	if rec := request(t, a.Server, "GET", "/api/sessions/"+fork.ID, nil); rec.Code != 404 {
		t.Errorf("get after delete = %d, want 404", rec.Code)
	}
}

func TestSettingsAndModels(t *testing.T) {
	a := newAPI(t)

	models := decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200)
	if len(models.Models) != 1 || models.Models[0].Name != "test-model" || models.Models[0].ContextWindow != 8192 {
		t.Fatalf("models = %+v", models)
	}
	if models.Default != "" {
		t.Errorf("default = %q, want none before the user picks one", models.Default)
	}

	put := decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"default_model": "test-model", "theme": "dark",
	}), 200)
	if string(put.Settings["default_model"]) != `"test-model"` {
		t.Errorf("settings = %v", put.Settings)
	}
	got := decodeBody[settingsWire](t, request(t, a.Server, "GET", "/api/settings", nil), 200)
	if len(got.Settings) != 2 {
		t.Errorf("settings = %v, want both keys", got.Settings)
	}
	models = decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200)
	if models.Default != "test-model" {
		t.Errorf("default = %q, want the setting", models.Default)
	}

	// A run may only name a model the deployment configured.
	sess := a.session(t)
	if rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages",
		map[string]any{"text": "hi", "model": "gone"}); rec.Code != 400 {
		t.Errorf("unknown model = %d, want 400", rec.Code)
	}
}

// initRepo makes dir a git repository with one commit, which is what a local
// project's host directory looks like.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	write(t, dir, "hello.txt", "hello\n")
	for _, args := range [][]string{
		{"init", "--initial-branch", "main"},
		{"config", "user.email", "eika@example.invalid"},
		{"config", "user.name", "Eika Test"},
		{"add", "."},
		{"commit", "--message", "first"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// write puts a file in a workspace directory.
func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
