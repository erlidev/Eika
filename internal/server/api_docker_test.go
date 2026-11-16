//go:build docker

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/egress"
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
	"github.com/erlidev/eika/internal/tool/builtin"
)

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m))
}

// api is a server under test: the real store on a throwaway database, a
// computer host backed by temporary directories, and a provider the test
// scripts per run.
type api struct {
	*server.Server
	store     *store.Store
	deps      server.Deps
	host      *fakeHost
	questions *builtin.Questions
	secrets   *secret.Box
	mcp       *mcp.Pool
	egress    *egress.Proxy
	// searxng and marginalia answer web searches; fetched serves every page
	// web_fetch reads.
	searxng, marginalia *searchtest.Searcher
	fetched             *searchtest.Transport
	// testProvider is the provider the model every run uses by default
	// belongs to.
	testProvider store.Provider

	mu       sync.Mutex
	provider provider.Provider
	// at holds the providers served at particular base URLs instead, which
	// is how a test tells a utility model's calls from a run's.
	at map[string]provider.Provider
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
	a := &api{store: st, host: newFakeHost(t), questions: builtin.NewQuestions(), secrets: secrets}
	// The proxy asks the server for policies, as it does in the harness.
	a.egress = egress.New(egress.Options{Policy: func(ctx context.Context, id string) (egress.Policy, error) {
		return a.EgressPolicy(ctx, id)
	}}, testLogger())
	// The bus is built here rather than left to server.New, because the
	// MCP pool emits on the same one the stream fans out.
	bus := event.NewBus(testLogger())
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
	tools, err := builtin.Registry(builtin.Deps{Questions: a.questions, Search: engine, Pages: pages})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	// The pool closes after the server, whose runs call its tools, so its
	// cleanup is registered first.
	a.mcp = server.NewMCP(testConfig(), st, secrets, a.host, bus, testLogger())
	t.Cleanup(a.mcp.Close)
	a.deps = server.Deps{
		Store:     st,
		Computers: a.host,
		Providers: fakeProviders{build: a.buildProvider},
		Secrets:   secrets,
		Tools:     tools,
		Questions: a.questions,
		Bus:       bus,
		Search:    engine,
		Pages:     pages,
		Egress:    a.egress,
		MCP:       a.mcp,
	}
	a.Server = server.New(testConfig(), testLogger(), a.deps, server.Options{})
	t.Cleanup(a.Close)

	// Every run uses test-model unless it names another. Its window is wide
	// enough for the whole tool registry: the agent loop refuses a request
	// whose conservative upper bound does not fit, and the schemas of the
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
	if served, ok := a.at[e.BaseURL]; ok {
		p = served
	}
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

// serveAt makes p the provider every build on baseURL hands out.
func (a *api) serveAt(baseURL string, p provider.Provider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.at == nil {
		a.at = make(map[string]provider.Provider)
	}
	a.at[baseURL] = p
}

// lastEndpoint returns the endpoint the last provider was built on.
func (a *api) lastEndpoint() provider.Endpoint {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.endpoint
}

// newSession opens a session in a computer.
func (a *api) newSession(t *testing.T, computerID string) sessionWire {
	t.Helper()
	rec := request(t, a.Server, "POST", "/api/sessions", map[string]any{
		"computer_id": computerID, "title": "a session",
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
type computerWire struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Image   string      `json:"image"`
	State   string      `json:"state"`
	Sandbox sandboxWire `json:"sandbox"`
	Home    bool        `json:"home"`
}

type computersWire struct {
	Computers []computerWire `json:"computers"`
}

type sessionWire struct {
	ID          string `json:"id"`
	ComputerID  string `json:"computer_id"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	HeadEntryID string `json:"head_entry_id"`
	Pinned      bool   `json:"pinned"`
	Archived    bool   `json:"archived"`
}

type sessionsWire struct {
	Sessions []sessionWire `json:"sessions"`
}

type entryWire struct {
	ID      string           `json:"id"`
	Kind    string           `json:"kind"`
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
	PendingSteering  []queuedWire       `json:"pending_steering"`
	PendingFollowUps []queuedWire       `json:"pending_follow_ups"`
	Questions        []builtin.Question `json:"questions"`
}

type queuedWire struct {
	Text   string `json:"text"`
	Images int    `json:"images"`
}

type settingsWire struct {
	Settings map[string]json.RawMessage `json:"settings"`
	Defaults struct {
		SandboxImage string `json:"sandbox_image"`
		// ComputerIdleMinutes is how long a computer may go unused.
		ComputerIdleMinutes int `json:"computer_idle_minutes"`
		Compaction          struct {
			Auto             bool `json:"auto"`
			ReserveTokens    int  `json:"reserve_tokens"`
			KeepRecentTokens int  `json:"keep_recent_tokens"`
			Prompts          struct {
				Summary string `json:"summary"`
			} `json:"prompts"`
		} `json:"compaction"`
	} `json:"defaults"`
}

type modelWire struct {
	ID               string   `json:"id"`
	ProviderID       string   `json:"provider_id"`
	Kind             string   `json:"kind"`
	Name             string   `json:"name"`
	Model            string   `json:"model"`
	ContextWindow    int      `json:"context_window"`
	ProbedWindow     int      `json:"probed_window"`
	EffectiveWindow  int      `json:"effective_window"`
	MaxOutput        int      `json:"max_output"`
	ReasoningEffort  string   `json:"reasoning_effort"`
	ReasoningEfforts []string `json:"reasoning_efforts"`
	ThinkingSwitch   string   `json:"thinking_switch"`
	PreserveThinking bool     `json:"preserve_thinking"`
	ImageInput       bool     `json:"image_input"`
}

type modelsWire struct {
	Models  []modelWire `json:"models"`
	Default string      `json:"default"`
}

// A computer has no owner of its own: the harness has one user, and the
// sign-in token is the only way to a computer. These requests come from
// someone without it, each to a route that reads or changes the computer.
func TestNoOneWithoutASignInTokenReachesAComputer(t *testing.T) {
	a := newAPI(t)
	cx := a.home(t)
	sess := a.newSession(t, cx.ID)

	routes := []struct{ method, path string }{
		{"GET", "/api/computers"},
		{"GET", "/api/sessions/" + sess.ID},
		{"POST", "/api/sessions/" + sess.ID + "/messages"},
	}
	// A token the harness once handed out and then revoked is no better than
	// none.
	first := decodeBody[signInWire](t, requestWith(t, a.Server, "POST", "/api/auth/setup",
		map[string]any{"password": "correct horse battery"}, ""), 201)
	if rec := requestWith(t, a.Server, "POST", "/api/auth/logout", nil, bearer(first.Token)); rec.Code != 204 {
		t.Fatalf("sign-out = %d, want 204", rec.Code)
	}
	for _, header := range []string{"", bearer("not-a-token"), bearer(first.Token)} {
		for _, r := range routes {
			rec := requestWith(t, a.Server, r.method, r.path, map[string]any{"name": "taken over"}, header)
			if rec.Code != 401 {
				t.Errorf("%s %s with %q = %d, want 401", r.method, r.path, header, rec.Code)
			}
		}
	}

	// None of it changed the computer.
	if got := a.homeOf(t); got.Name != "home" || got.State != "running" {
		t.Errorf("computer = %+v, want it untouched and running", got)
	}
}

func TestReconcileRecordsWhatTheHostActuallyHas(t *testing.T) {
	a := newAPI(t)
	cx := a.home(t)

	// The container went away while the harness was down.
	a.host.lose(t, cx.ID)
	if err := a.Reconcile(t.Context()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := a.computerState(t, cx.ID); got != "gone" {
		t.Errorf("state = %q, want gone", got)
	}
}

func TestReconcilePreservesStateWhenInspectFails(t *testing.T) {
	a := newAPI(t)
	cx := a.home(t)
	a.host.inspectErr = fmt.Errorf("docker daemon unavailable")

	if err := a.Reconcile(t.Context()); err == nil {
		t.Fatal("Reconcile succeeded when inspection failed")
	}
	stored, err := a.store.Computer(t.Context(), cx.ID)
	if err != nil {
		t.Fatalf("read computer: %v", err)
	}
	if stored.State != "running" {
		t.Errorf("state = %q, want running", stored.State)
	}
}

func TestSessionRoutes(t *testing.T) {
	a := newAPI(t)
	cx := a.home(t)

	sess := a.newSession(t, cx.ID)
	if sess.ComputerID != cx.ID {
		t.Fatalf("session = %+v", sess)
	}
	list := decodeBody[sessionsWire](t, request(t, a.Server, "GET", "/api/sessions?computer_id="+cx.ID, nil), 200)
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

	if rec := request(t, a.Server, "DELETE", "/api/sessions/"+sess.ID, nil); rec.Code != 204 {
		t.Fatalf("delete = %d", rec.Code)
	}
	if rec := request(t, a.Server, "GET", "/api/sessions/"+sess.ID, nil); rec.Code != 404 {
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
	if got.Defaults.SandboxImage != "eika-sandbox:latest" {
		t.Errorf("defaults = %+v", got.Defaults)
	}
	if c := got.Defaults.Compaction; !c.Auto || c.ReserveTokens != 16384 || c.KeepRecentTokens != 20000 || !strings.HasPrefix(c.Prompts.Summary, "The messages above") {
		t.Errorf("compaction defaults = %+v", c)
	}
	if rec := request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"compaction": map[string]any{"auto": false, "keep_recent_tokens": 4096, "prompts": map[string]any{"summary": "Be brief."}},
	}); rec.Code != 200 {
		t.Errorf("a good compaction setting = %d: %s", rec.Code, rec.Body.String())
	}

	// The keys the harness reads are checked; one bad value writes nothing.
	for name, body := range map[string]map[string]any{
		"a default model that does not exist": {"default_model": "gone"},
		"a class model that does not exist":   {"model_classes": map[string]any{"fast": "gone"}},
		"a compaction with an unknown field":  {"compaction": map[string]any{"auto": true, "reserve": 1}},
		"a good key beside a bad one":         {"theme": "light", "sandbox_image": ""},
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
	// A run may only name a model that exists.
	sess := a.session(t)
	if rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages",
		map[string]any{"text": "hi", "model": "gone"}); rec.Code != 400 {
		t.Errorf("unknown model = %d, want 400", rec.Code)
	}
}
