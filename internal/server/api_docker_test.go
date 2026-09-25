//go:build docker

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/searchtest"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/erlidev/eika/internal/subagent"
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
	spawner   *subagent.Spawner
	secrets   *secret.Box
	mcp       *mcp.Pool
	// searxng and marginalia answer web searches; fetched serves every page
	// web_fetch reads.
	searxng, marginalia *searchtest.Searcher
	fetched             *searchtest.Transport
	// testProvider is the provider the model every run uses by default
	// belongs to.
	testProvider store.Provider

	mu       sync.Mutex
	provider provider.Provider
	// endpoint is the endpoint the last provider was built on.
	endpoint provider.Endpoint
	// delay holds up building a run's provider, which is how a test widens
	// the window two concurrent requests race in.
	delay time.Duration
}

// newAPI returns a server wired to a database of this test's own.
func newAPI(t *testing.T) *api {
	t.Helper()
	st := storetest.Open(t)
	secrets, err := secret.Load(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatalf("secret.Load: %v", err)
	}
	a := &api{store: st, host: newFakeHost(t), hub: newFakeHub(), questions: builtin.NewQuestions(), secrets: secrets}
	// The bus is built here rather than left to server.New, because the
	// spawner emits on the same one the stream fans out.
	bus := event.NewBus(testLogger())
	a.spawner = subagent.New(subagent.Options{
		Store:      st,
		Workspaces: a.host,
		Emitter:    bus,
		Limits: func(context.Context) subagent.Limits {
			return subagent.Limits{MaxDepth: 2, MaxChildren: 4}
		},
		Logger: testLogger(),
	})
	a.searxng = searchtest.New(search.Result{Title: "Tokio", URL: "https://tokio.rs/", Description: "An async runtime."})
	a.marginalia = searchtest.New(search.Result{Title: "Small web", URL: "https://small.example/"})
	none := searchtest.New()
	pageClient, fetched := searchtest.Client(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Guide\n\nthe timeout is 30s\n"))
	})
	a.fetched = fetched
	engine, pages, err := server.NewSearch(t.Context(), testConfig(), st, secrets, testLogger(), search.Searchers{
		SearxNG: a.searxng, Exa: none, Tavily: none, Brave: none, Marginalia: a.marginalia,
		Wikipedia: none, Arxiv: none, GitHubCode: none, GitHubRepos: none, GitHubIssues: none,
	}, pageClient)
	if err != nil {
		t.Fatalf("server.NewSearch: %v", err)
	}
	tools, err := builtin.Registry(builtin.Deps{Questions: a.questions, Agents: a.spawner, Search: engine, Pages: pages})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	// The pool closes after the server, whose runs call its tools, so its
	// cleanup is registered first.
	a.mcp = server.NewMCP(testConfig(), st, secrets, a.host, bus, testLogger())
	t.Cleanup(a.mcp.Close)
	a.Server = server.New(testConfig(), testLogger(), server.Deps{
		Store:      st,
		Hub:        a.hub,
		Workspaces: a.host,
		Providers:  fakeProviders{build: a.buildProvider},
		Secrets:    secrets,
		Tools:      tools,
		Questions:  a.questions,
		Bus:        bus,
		Search:     engine,
		Pages:      pages,
		MCP:        a.mcp,
	}, server.Options{})
	a.Server.UseSubagents(a.spawner, a.spawner.Attach)
	t.Cleanup(a.Server.Close)

	// Every run uses test-model unless it names another. Its window is wide
	// enough for the whole tool registry: the agent loop refuses a request
	// whose conservative upper bound does not fit, and the schemas of thirteen
	// tools are most of a small window.
	key, err := secrets.Seal("test-key")
	if err != nil {
		t.Fatalf("seal key: %v", err)
	}
	a.testProvider, err = st.CreateProvider(t.Context(), store.Provider{
		Name: "test", Kind: "openai", BaseURL: "http://model.invalid", APIKey: key,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if _, err := st.CreateModel(t.Context(), store.Model{
		ProviderID: a.testProvider.ID, Name: "test-model", Model: "test-model",
		ContextWindow: 32768, MaxOutput: 1024,
	}); err != nil {
		t.Fatalf("create model: %v", err)
	}
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

// buildProvider hands the run manager whatever the test scripted last, and
// records the endpoint it was asked for.
func (a *api) buildProvider(kind string, e provider.Endpoint) (provider.Provider, error) {
	a.mu.Lock()
	p, delay := a.provider, a.delay
	a.endpoint = e
	a.mu.Unlock()
	time.Sleep(delay)
	if p == nil {
		return nil, fmt.Errorf("no %s provider scripted for %s", kind, e.BaseURL)
	}
	return p, nil
}

// use makes p the provider the next build hands out.
func (a *api) use(p provider.Provider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.provider = p
}

// lastEndpoint returns the endpoint the last provider was built on.
func (a *api) lastEndpoint() provider.Endpoint {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.endpoint
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
	ID                string `json:"id"`
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	RemoteURL         string `json:"remote_url"`
	RemoteUsername    string `json:"remote_username"`
	RemotePasswordSet bool   `json:"remote_password_set"`
	HostPath          string `json:"host_path"`
	DefaultBranch     string `json:"default_branch"`
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
	Image      string `json:"image"`
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
	ID              string `json:"id"`
	WorkspaceID     string `json:"workspace_id"`
	Title           string `json:"title"`
	Kind            string `json:"kind"`
	HeadEntryID     string `json:"head_entry_id"`
	ParentSessionID string `json:"parent_session_id"`
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
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		Preview   string `json:"preview"`
		Resumable bool   `json:"resumable"`
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
	Defaults struct {
		SandboxImage        string `json:"sandbox_image"`
		SubagentMaxDepth    int    `json:"subagent_max_depth"`
		SubagentMaxChildren int    `json:"subagent_max_children"`
	} `json:"defaults"`
}

type modelWire struct {
	ID               string   `json:"id"`
	ProviderID       string   `json:"provider_id"`
	Name             string   `json:"name"`
	Model            string   `json:"model"`
	ContextWindow    int      `json:"context_window"`
	MaxOutput        int      `json:"max_output"`
	ReasoningEffort  string   `json:"reasoning_effort"`
	ReasoningEfforts []string `json:"reasoning_efforts"`
	ThinkingSwitch   string   `json:"thinking_switch"`
	PreserveThinking bool     `json:"preserve_thinking"`
}

type modelsWire struct {
	Models  []modelWire `json:"models"`
	Default string      `json:"default"`
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

func TestPrivateRemoteCredentialsAreSealed(t *testing.T) {
	a := newAPI(t)
	rec := request(t, a.Server, "POST", "/api/projects", map[string]any{
		"name":            "private",
		"kind":            "remote",
		"remote_url":      "https://example.invalid/private.git",
		"remote_username": "git-user",
		"remote_password": "private-token",
	})
	created := decodeBody[projectWire](t, rec, 201)
	if created.RemoteUsername != "git-user" || !created.RemotePasswordSet {
		t.Errorf("credentials = %q, set=%v", created.RemoteUsername, created.RemotePasswordSet)
	}
	if strings.Contains(rec.Body.String(), "private-token") {
		t.Errorf("response exposed the password: %s", rec.Body.String())
	}
	if creds := a.hub.credentials["private"]; creds.Username != "git-user" || creds.Password != "private-token" {
		t.Errorf("hub credentials = %+v, want the project's", creds)
	}
	stored, err := a.store.Project(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	if len(stored.RemotePassword) == 0 || strings.Contains(string(stored.RemotePassword), "private-token") {
		t.Errorf("stored password = %q, want it sealed", stored.RemotePassword)
	}

	// A new token is tried against the remote before it replaces the old one.
	updated := decodeBody[projectWire](t, request(t, a.Server, "PATCH", "/api/projects/"+created.ID,
		map[string]any{"remote_password": "rotated-token"}), 200)
	if creds := a.hub.credentials["private"]; creds.Username != "git-user" || creds.Password != "rotated-token" {
		t.Errorf("hub credentials after rotation = %+v", creds)
	}
	if !updated.RemotePasswordSet {
		t.Error("the rotated password is not reported as set")
	}

	// A token the remote refuses is not stored.
	a.hub.mirrorErr = fmt.Errorf("authentication failed for wrong-token")
	rec = request(t, a.Server, "PATCH", "/api/projects/"+created.ID, map[string]any{"remote_password": "wrong-token"})
	if rec.Code != 400 || strings.Contains(rec.Body.String(), "wrong-token") {
		t.Errorf("refused token = %d %s, want a 400 that does not quote it", rec.Code, rec.Body.String())
	}
	a.hub.mirrorErr = nil
	if creds, _ := a.store.Project(t.Context(), created.ID); string(creds.RemotePassword) == string(stored.RemotePassword) {
		t.Error("the stored password did not change on the rotation")
	}

	// A password the harness can no longer open, as after the secret key
	// file changed, is replaced by entering a new one.
	unreadable, err := a.store.Project(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	unreadable.RemotePassword = []byte("sealed under a key this harness lacks")
	if _, err := a.store.UpdateProject(t.Context(), unreadable); err != nil {
		t.Fatalf("corrupt the stored password: %v", err)
	}
	if rec := request(t, a.Server, "PATCH", "/api/projects/"+created.ID, map[string]any{"remote_username": "git-user"}); rec.Code != 409 {
		t.Errorf("username change over an unreadable password = %d, want 409", rec.Code)
	}
	decodeBody[projectWire](t, request(t, a.Server, "PATCH", "/api/projects/"+created.ID,
		map[string]any{"remote_password": "re-entered-token"}), 200)
	if creds := a.hub.credentials["private"]; creds.Username != "git-user" || creds.Password != "re-entered-token" {
		t.Errorf("hub credentials after re-entry = %+v", creds)
	}

	// Removing the password makes the remote public again.
	cleared := decodeBody[projectWire](t, request(t, a.Server, "PATCH", "/api/projects/"+created.ID,
		map[string]any{"remote_password": ""}), 200)
	if cleared.RemoteUsername != "" || cleared.RemotePasswordSet {
		t.Errorf("cleared = %+v, want no credentials", cleared)
	}

	local, _ := a.newProject(t, "local")
	if rec := request(t, a.Server, "PATCH", "/api/projects/"+local.ID,
		map[string]any{"remote_username": "u", "remote_password": "p"}); rec.Code != 400 {
		t.Errorf("credentials on a local project = %d, want 400", rec.Code)
	}
	branch := decodeBody[projectWire](t, request(t, a.Server, "PATCH", "/api/projects/"+local.ID,
		map[string]any{"default_branch": "trunk"}), 200)
	if branch.DefaultBranch != "trunk" {
		t.Errorf("default_branch = %q, want trunk", branch.DefaultBranch)
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
		{"remote with url credentials", map[string]any{"name": "x", "kind": "remote", "remote_url": "https://token@example.test/repo.git"}, 400, "invalid_request"},
		{"remote with a url query", map[string]any{"name": "x", "kind": "remote", "remote_url": "https://example.test/repo.git?token=private"}, 400, "invalid_request"},
		{"remote with an empty url query", map[string]any{"name": "x", "kind": "remote", "remote_url": "https://example.test/repo.git?"}, 400, "invalid_request"},
		{"remote with a url fragment", map[string]any{"name": "x", "kind": "remote", "remote_url": "https://example.test/repo.git#private"}, 400, "invalid_request"},
		{"remote with half a credential pair", map[string]any{"name": "x", "kind": "remote", "remote_url": "https://example.test/repo.git", "remote_password": "token"}, 400, "invalid_request"},
		{"local with remote credentials", map[string]any{"name": "x", "kind": "local", "host_path": "/tmp", "remote_username": "u", "remote_password": "p"}, 400, "invalid_request"},
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

func TestProjectCreationDoesNotReturnURLCredentialData(t *testing.T) {
	a := newAPI(t)
	for name, remote := range map[string]string{
		"userinfo": "https://user:password-secret@example.test/repo.git",
		"query":    "https://example.test/repo.git?token=query-secret",
		"fragment": "https://example.test/repo.git#fragment-secret",
	} {
		t.Run(name, func(t *testing.T) {
			rec := request(t, a.Server, "POST", "/api/projects", map[string]any{
				"name": name, "kind": "remote", "remote_url": remote,
			})
			if rec.Code != 400 {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			for _, secret := range []string{"password-secret", "query-secret", "fragment-secret"} {
				if strings.Contains(rec.Body.String(), secret) {
					t.Errorf("response exposed %q: %s", secret, rec.Body.String())
				}
			}
		})
	}
}

func TestProjectRoutesRemoveCredentialDataFromStoredLegacyURLs(t *testing.T) {
	a := newAPI(t)
	project, err := a.store.CreateProject(t.Context(), store.Project{
		Name:          "legacy-private",
		Kind:          store.ProjectRemote,
		RemoteURL:     "https://user:password-secret@example.test/repo.git?token=query-secret#fragment-secret",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("create legacy project: %v", err)
	}

	for _, path := range []string{"/api/projects/" + project.ID, "/api/projects"} {
		rec := request(t, a.Server, "GET", path, nil)
		if rec.Code != 200 {
			t.Fatalf("GET %s status = %d, want 200: %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "https://example.test/repo.git") {
			t.Errorf("GET %s did not return the sanitized remote: %s", path, rec.Body.String())
		}
		for _, secret := range []string{"password-secret", "query-secret", "fragment-secret"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Errorf("GET %s exposed %q: %s", path, secret, rec.Body.String())
			}
		}
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

func TestReconcileAbortsRunsLeftByAnEarlierProcess(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	run, err := a.store.StartRun(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("start stale run: %v", err)
	}
	if err := a.Server.Reconcile(t.Context()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, err := a.store.Run(t.Context(), run.ID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if got.State != store.RunAborted || got.FinishedAt.IsZero() {
		t.Errorf("run = %+v, want an aborted run with a finish time", got)
	}
}

func TestReconcilePreservesStateWhenInspectFails(t *testing.T) {
	a := newAPI(t)
	project, _ := a.newProject(t, "demo")
	ws := a.newWorkspace(t, project.ID)
	a.host.inspectErr = fmt.Errorf("docker daemon unavailable")

	if err := a.Server.Reconcile(t.Context()); err == nil {
		t.Fatal("Reconcile succeeded when inspection failed")
	}
	stored, err := a.store.Workspace(t.Context(), ws.ID)
	if err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	if stored.State != "running" {
		t.Errorf("state = %q, want running", stored.State)
	}
}

func TestDeleteWorkspacePreservesRowWhenInspectFails(t *testing.T) {
	a := newAPI(t)
	project, _ := a.newProject(t, "demo")
	ws := a.newWorkspace(t, project.ID)
	a.host.inspectErr = fmt.Errorf("docker daemon unavailable")

	rec := request(t, a.Server, "DELETE", "/api/workspaces/"+ws.ID, nil)
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if _, err := a.store.Workspace(t.Context(), ws.ID); err != nil {
		t.Errorf("workspace row was deleted after an inspection failure: %v", err)
	}
}

// A run's tool calls have to be answered before the conversation can go
// anywhere else, so the entries in the middle of a turn are not places a
// head or a fork may be put: a session made there fails on its next run.
func TestHeadAndForkRefuseAnUnansweredTurn(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)

	a.script(
		providertest.Calls("looking",
			provider.ToolCall{ID: "call_1", Name: "bash", Arguments: provider.ToolArguments(`{"command":"ls"}`)},
			provider.ToolCall{ID: "call_2", Name: "bash", Arguments: provider.ToolArguments(`{"command":"ls"}`)},
		),
		providertest.Text("both read"),
	)
	if rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages",
		map[string]any{"text": "what is here"}); rec.Code != 202 {
		t.Fatalf("post message = %d: %s", rec.Code, rec.Body.String())
	}
	a.waitIdle(t, sess.ID)

	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	// user, assistant with two calls, two results, the closing answer.
	if len(path.Entries) != 5 {
		t.Fatalf("entries = %d, want 5: %+v", len(path.Entries), path.Entries)
	}
	outline := decodeBody[outlineWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/outline", nil), 200)
	want := []bool{true, false, false, true, true}
	for i, node := range outline.Nodes {
		if node.Resumable != want[i] {
			t.Errorf("node %d (%s) resumable = %v, want %v", i, node.Kind, node.Resumable, want[i])
		}
	}

	// The assistant turn that asked for two calls, and the result that
	// answers only the first, are both mid-turn.
	for _, at := range []int{1, 2} {
		body := map[string]any{"entry_id": path.Entries[at].ID}
		if rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/head", body); rec.Code != 400 {
			t.Errorf("head at entry %d = %d, want 400: %s", at, rec.Code, rec.Body.String())
		}
		body["title"] = "mid-turn"
		if rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/fork", body); rec.Code != 400 {
			t.Errorf("fork at entry %d = %d, want 400: %s", at, rec.Code, rec.Body.String())
		}
	}
	// The result that answers the last call closes the turn, so it is a
	// place a branch may start.
	if rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/head",
		map[string]any{"entry_id": path.Entries[3].ID}); rec.Code != 200 {
		t.Errorf("head at the closing result = %d, want 200: %s", rec.Code, rec.Body.String())
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

	if fork.Kind != "fork" || fork.ParentSessionID != sess.ID {
		t.Errorf("fork = %+v, want kind fork under %s", fork, sess.ID)
	}
	// The fork lives in the source's workspace, so it is in the plain
	// listing; a fork with a workspace of its own would need descendants.
	tree := decodeBody[sessionsWire](t, request(t, a.Server, "GET",
		"/api/sessions?workspace_id="+ws.ID+"&descendants=true", nil), 200)
	if len(tree.Sessions) != 2 {
		t.Errorf("sessions with descendants = %d, want the session and its fork", len(tree.Sessions))
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
	if len(models.Models) != 1 || models.Models[0].Name != "test-model" || models.Models[0].ContextWindow != 32768 {
		t.Fatalf("models = %+v", models)
	}
	if models.Default != "test-model" {
		t.Errorf("default = %q, want the only model before the user picks one", models.Default)
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
	if got.Defaults.SandboxImage != "eika-sandbox:latest" || got.Defaults.SubagentMaxDepth != 2 || got.Defaults.SubagentMaxChildren != 4 {
		t.Errorf("defaults = %+v", got.Defaults)
	}

	// The keys the harness reads are checked; one bad value writes nothing.
	for name, body := range map[string]map[string]any{
		"a default model that does not exist": {"default_model": "gone"},
		"a default model that is not a name":  {"default_model": 7},
		"an empty sandbox image":              {"sandbox_image": ""},
		"a sandbox image with a space":        {"sandbox_image": "eika sandbox"},
		"a subagent depth of zero":            {"subagent_max_depth": 0},
		"a subagent depth past the limit":     {"subagent_max_depth": 9},
		"a child count that is not a number":  {"subagent_max_children": "two"},
		"a setup flag that is not a boolean":  {"setup_complete": "yes"},
		"a key that is too long":              {strings.Repeat("k", 65): true},
		"a good key beside a bad one":         {"theme": "light", "subagent_max_depth": 0},
	} {
		t.Run(name, func(t *testing.T) {
			if rec := request(t, a.Server, "PUT", "/api/settings", body); rec.Code != 400 {
				t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
	after := decodeBody[settingsWire](t, request(t, a.Server, "GET", "/api/settings", nil), 200)
	if string(after.Settings["theme"]) != `"dark"` {
		t.Errorf("theme = %s, want the rejected request to have written nothing", after.Settings["theme"])
	}
	// A write the database refuses partway through, here a JSON string
	// PostgreSQL cannot store, leaves every key as it was.
	if rec := request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"theme": "light", "a_note": "nul \u0000 byte", "z_note": "fine",
	}); rec.Code != 500 {
		t.Errorf("unstorable value = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	after = decodeBody[settingsWire](t, request(t, a.Server, "GET", "/api/settings", nil), 200)
	if string(after.Settings["theme"]) != `"dark"` || after.Settings["z_note"] != nil {
		t.Errorf("settings = %v, want the failed write to have written nothing", after.Settings)
	}
	ok := decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"sandbox_image": "custom:1", "subagent_max_depth": 3, "subagent_max_children": 8, "setup_complete": true,
		"default_model": nil,
	}), 200)
	if string(ok.Settings["subagent_max_depth"]) != "3" || string(ok.Settings["default_model"]) != "null" {
		t.Errorf("settings = %v", ok.Settings)
	}

	// A run may only name a model that exists.
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

func TestWorkspaceCreationCleansUpEvenWhenTheRequestIsGone(t *testing.T) {
	a := newAPI(t)
	// A remote project, because that is the kind whose workspaces clone from
	// the hub: a local one is bind-mounted and has nothing to fetch.
	project := decodeBody[projectWire](t, request(t, a.Server, "POST", "/api/projects", map[string]any{
		"name": "upstream", "kind": "remote", "remote_url": "https://example.invalid/x.git",
	}), 201)

	// The client gives up while the workspace is being filled, which is the
	// most likely reason the creation fails at all. The container the harness
	// already made is still its own to remove, so the cleanup must not run on
	// the request's context.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a.host.cloneHook = cancel
	a.host.cloneErr = fmt.Errorf("the hub is unreachable")

	rec := requestOn(t, ctx, a.Server, "POST", "/api/workspaces",
		map[string]any{"project_id": project.ID, "name": "work"})
	if rec.Code == 201 {
		t.Fatalf("the workspace was created despite the clone failing: %s", rec.Body.String())
	}

	live, err := a.host.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(live) != 0 {
		t.Errorf("host holds %d workspaces, want none", len(live))
	}
}
