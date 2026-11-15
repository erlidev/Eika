//go:build docker

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
)

// byAgent hands each agent's calls to its own script, found by the agent's
// system prompt, since Eika and its subagents run at the same time.
type byAgent map[string]provider.Provider

func (b byAgent) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	for prefix, p := range b {
		if strings.Contains(req.System, prefix) {
			return p.Stream(ctx, req)
		}
	}
	return nil, fmt.Errorf("no script for system prompt %.40q", req.System)
}

// noticeTurns answers each background turn that delivers notices with
// "noted" and hands every other call to its script, so that a script for Eika
// holds only the turns the user starts.
type noticeTurns struct {
	provider.Provider
	// arrived and release, when release is not nil, hold each background
	// call until release closes, after telling arrived.
	arrived chan struct{}
	release chan struct{}

	mu       sync.Mutex
	requests []provider.Request
}

func answeringNotices(p provider.Provider) *noticeTurns { return &noticeTurns{Provider: p} }

func (n *noticeTurns) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	if !deliversNotices(req) {
		return n.Provider.Stream(ctx, req)
	}
	n.mu.Lock()
	n.requests = append(n.requests, req)
	n.mu.Unlock()
	if n.release != nil {
		n.arrived <- struct{}{}
		select {
		case <-n.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return providertest.New(providertest.Text("noted")).Stream(ctx, req)
}

// Requests returns the background turns' requests, oldest first.
func (n *noticeTurns) Requests() []provider.Request {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]provider.Request(nil), n.requests...)
}

// deliversNotices reports whether the newest user-side message of a request
// is one a background turn sent: notices and nothing the user wrote.
func deliversNotices(req provider.Request) bool {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if m := req.Messages[i]; m.Role == provider.RoleUser && !m.Summary {
			return m.Content == "" && len(m.Images) == 0 && len(m.Notices) > 0
		}
	}
	return false
}

// heldScript holds every call until release closes, which keeps a subagent
// running while the test looks at it.
type heldScript struct {
	*providertest.Provider
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newHeldScript(steps ...providertest.Step) *heldScript {
	return &heldScript{Provider: providertest.New(steps...), started: make(chan struct{}, 8), release: make(chan struct{})}
}

func (g *heldScript) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	g.started <- struct{}{}
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.Provider.Stream(ctx, req)
}

func (g *heldScript) open() { g.once.Do(func() { close(g.release) }) }

// toolResultsByCall maps the tool results of a request's conversation by the ids
// of the calls that asked for them.
func toolResultsByCall(req provider.Request) map[string]string {
	results := map[string]string{}
	ids := provider.WireToolCallIDs(req.Messages)
	for _, m := range req.Messages {
		if m.Role == provider.RoleTool {
			results[provider.WireToolCallID(ids, m.ToolCallID)] = m.Content
		}
	}
	return results
}

// delegating is a server whose home session runs Eika with the production
// delegation tools, and a project directory on the home computer.
type delegating struct {
	*api
	home      store.Session
	computer  string
	directory string
	files     string
}

func newDelegating(t *testing.T) delegating {
	t.Helper()
	a := newAPI(t)
	cx := a.home(t)
	if err := a.BindAgentToolsForTest(a.host); err != nil {
		t.Fatal(err)
	}
	files, err := a.host.dirOf(cx.ID)
	if err != nil {
		t.Fatal(err)
	}
	host, err := a.host.Inspect(t.Context(), cx.ID)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := a.host.Executor(host)
	if err != nil {
		t.Fatal(err)
	}
	directory := ex.Root() + "/projects/kitchen"
	if _, err := a.store.CreateProject(t.Context(), "kitchen", store.ProjectFields{Name: "Kitchen", Status: "active", Directory: directory}); err != nil {
		t.Fatal(err)
	}
	home, err := a.store.CreateSession(t.Context(), store.Session{Kind: store.SessionHome, Name: "home", Agent: "eika"})
	if err != nil {
		t.Fatal(err)
	}
	a.assignClasses(t, map[string]any{"main": "test-model", "fast": "test-model"})
	// Two slots, so Eika's turn goes on while a subagent holds a call.
	if _, err := a.store.OverrideProvider(t.Context(), a.testProvider.ID, store.ProviderOverride{Slots: new(2)}); err != nil {
		t.Fatal(err)
	}
	return delegating{api: a, home: home, computer: cx.ID, directory: directory, files: files}
}

func call(id, name string, args map[string]any) providertest.Step {
	return providertest.Calls("", providertest.Call(id, name, args))
}

func (d delegating) child(t *testing.T, name string) store.Session {
	t.Helper()
	row, err := d.store.Subagent(t.Context(), d.home.ID, name)
	if err != nil {
		t.Fatalf("subagent %s: %v", name, err)
	}
	return row
}

func (d delegating) waitState(t *testing.T, name string, want store.SessionState) store.Session {
	t.Helper()
	var row store.Session
	waitFor(t, name+" to be "+string(want), func() bool {
		row = d.child(t, name)
		return row.State == want
	})
	return row
}

// priorityOf returns the priority of a session's call holding a slot, or ""
// when it has none.
func (d delegating) priorityOf(t *testing.T, sessionID string) string {
	t.Helper()
	body := decodeBody[struct {
		Pools []struct {
			Running []struct {
				SessionID string `json:"session_id"`
				Priority  string `json:"priority"`
			} `json:"running"`
		} `json:"pools"`
	}](t, request(t, d.Server, "GET", "/api/inference", nil), 200)
	for _, pool := range body.Pools {
		for _, running := range pool.Running {
			if running.SessionID == sessionID {
				return running.Priority
			}
		}
	}
	return ""
}

// requestText is everything a request shows the model but its tools.
func requestText(req provider.Request) string { return req.System + fmt.Sprint(req.Messages) }

func TestEikaDelegatesToNamedSubagents(t *testing.T) {
	d := newDelegating(t)
	eika := providertest.New(
		call("start-worker", "subagent_start", map[string]any{"agent": "worker", "name": "plumber", "brief": "Find a plumber", "project": "kitchen"}),
		call("start-researcher", "subagent_start", map[string]any{"agent": "researcher", "name": "flights", "brief": "Find flights to Lisbon"}),
		call("busy", "subagent_message", map[string]any{"subagent": "plumber", "text": "hurry"}),
		call("duplicate", "subagent_start", map[string]any{"agent": "researcher", "name": "plumber", "brief": "Replace the brief"}),
		providertest.Text("on it"),
		call("resume", "subagent_message", map[string]any{"subagent": "plumber", "text": "get a second quote"}),
		call("board", "subagent_status", map[string]any{}),
		providertest.Text("asked for a second quote"),
	)
	worker := newHeldScript(
		providertest.Calls("", providertest.Call("pwd", "bash", map[string]any{"command": "pwd"}),
			providertest.Call("notes", "file_write", map[string]any{"path": "notes.md", "content": "quotes"})),
		call("done", "report", map[string]any{"summary": "Found a plumber\nBob, 555-0100", "outputs": []string{"notes.md"}, "follow_ups": []string{"book Bob"}}),
		providertest.Text("plumber found"),
		call("again", "report", map[string]any{"summary": "Found a second quote"}),
		providertest.Text("second quote found"),
	)
	researcher := providertest.New(call("done", "report", map[string]any{"summary": "Flights found"}), providertest.Text("flights found"))
	d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a worker": worker, "You are a researcher": researcher})

	d.postMessage(t, d.home.ID, "find a plumber and flights", "", 202)
	<-worker.started
	// Eika's turn ends while the worker still runs: the turn did not wait.
	d.waitIdle(t, d.home.ID)
	plumber := d.child(t, "plumber")
	if plumber.State != store.SessionRunning || plumber.ComputerID != d.computer || plumber.Agent != "worker" || plumber.Brief != "Find a plumber" {
		t.Fatalf("plumber = %+v", plumber)
	}
	if got := d.priorityOf(t, plumber.ID); got != inference.Foreground.String() {
		t.Errorf("worker priority = %q, want foreground", got)
	}
	results := toolResultsByCall(eika.Requests()[4])
	for id, want := range map[string]string{
		"start-worker": `"note":"Started.`, "start-researcher": `"name":"flights"`,
		"busy": "is running", "duplicate": "already exists",
	} {
		if !strings.Contains(results[id], want) {
			t.Errorf("%s result = %q, want %q", id, results[id], want)
		}
	}
	flights := d.waitState(t, "flights", store.SessionIdle)
	if flights.ComputerID != "" {
		t.Errorf("researcher has computer %s", flights.ComputerID)
	}
	for _, def := range researcher.Requests()[0].Tools {
		if def.Name == "bash" || strings.HasPrefix(def.Name, "file_") || strings.HasPrefix(def.Name, "subagent_") {
			t.Errorf("researcher offered %s", def.Name)
		}
	}

	worker.open()
	d.waitState(t, "plumber", store.SessionIdle)
	first := worker.Requests()[0]
	if !strings.Contains(requestText(first), "Find a plumber") {
		t.Error("worker did not get its brief")
	}
	if !strings.Contains(requestText(first), "Directory: "+d.directory) {
		t.Error("worker preamble does not name the project directory")
	}
	for _, def := range first.Tools {
		if strings.HasPrefix(def.Name, "subagent_") || strings.HasPrefix(def.Name, "project_") {
			t.Errorf("worker offered %s", def.Name)
		}
	}
	if got, want := d.child(t, "plumber").Result, "Found a plumber\nBob, 555-0100\n\nOutputs:\n- notes.md\n\nFollow-ups:\n- book Bob"; got != want {
		t.Errorf("plumber's report = %q, want %q", got, want)
	}
	if got := d.child(t, "flights").Result; got != "Flights found" {
		t.Errorf("flights' report = %q", got)
	}
	work := toolResultsByCall(worker.Requests()[1])
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(work["pwd"])); got != mustEval(t, d.directory) {
		t.Errorf("worker ran in %q, want %q", work["pwd"], d.directory)
	}
	if data, err := os.ReadFile(filepath.Join(d.files, "projects", "kitchen", "notes.md")); err != nil || string(data) != "quotes" {
		t.Errorf("notes.md in the project = %q, %v", data, err)
	}

	d.postMessage(t, d.home.ID, "get a second quote", "", 202)
	d.waitIdle(t, d.home.ID)
	d.waitState(t, "plumber", store.SessionIdle)
	resumed := worker.Requests()[3]
	if text := fmt.Sprint(resumed.Messages); !strings.Contains(text, "plumber found") || !strings.Contains(text, "get a second quote") {
		t.Errorf("resume lost the worker's context: %s", text)
	}
	results = toolResultsByCall(eika.Requests()[7])
	if !strings.Contains(results["resume"], `"note":"Accepted your message. It continues in its own context."`) ||
		!strings.Contains(results["resume"], `"state":"running"`) || !strings.Contains(results["board"], `"report":"Found a plumber"`) ||
		!strings.Contains(results["board"], `"report":"Flights found"`) || !strings.Contains(results["board"], `"name":"plumber"`) ||
		!strings.Contains(results["board"], `"name":"flights"`) || !strings.Contains(results["board"], `"project":"kitchen"`) {
		t.Errorf("resume and board results = %q", results)
	}

	// Eika messages its subagents; the API does not.
	d.postMessage(t, plumber.ID, "direct", "", 409)
	if rec := request(t, d.Server, "POST", "/api/sessions/"+plumber.ID+"/compact", map[string]any{}); rec.Code != 409 {
		t.Errorf("compact subagent = %d", rec.Code)
	}
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOnlyTheHomeSessionDelegates(t *testing.T) {
	d := newDelegating(t)
	// An eika session that is not home is offered the tools by its
	// definition, and the binding still refuses it.
	other, err := d.store.CreateSession(t.Context(), store.Session{Agent: "eika"})
	if err != nil {
		t.Fatal(err)
	}
	eika := d.script(call("start", "subagent_start", map[string]any{"agent": "worker", "name": "nested", "brief": "b"}),
		call("status", "subagent_status", map[string]any{}), providertest.Text("done"))
	d.postMessage(t, other.ID, "delegate", "", 202)
	d.waitIdle(t, other.ID)
	results := toolResultsByCall(eika.Requests()[2])
	for _, id := range []string{"start", "status"} {
		if !strings.Contains(results[id], "only Eika's home session can delegate") {
			t.Errorf("%s result = %q", id, results[id])
		}
	}
	if rows, err := d.store.Subagents(t.Context(), other.ID); err != nil || len(rows) != 0 {
		t.Fatalf("children of a non-home session: %+v %v", rows, err)
	}
	if got, err := d.DelegationPriority(t.Context(), d.home.ID); err != nil || got != inference.Background {
		t.Fatalf("priority without a home turn = %v, %v; want background", got, err)
	}
}

func TestCancelAbortAndShutdownStopSubagentRuns(t *testing.T) {
	d := newDelegating(t)
	eika := providertest.New(
		call("start", "subagent_start", map[string]any{"agent": "worker", "name": "plumber", "brief": "Find a plumber"}),
		providertest.Text("on it"),
		call("cancel", "subagent_cancel", map[string]any{"subagent": "plumber"}),
		providertest.Text("stopped"),
		call("again", "subagent_message", map[string]any{"subagent": "plumber", "text": "try again"}),
		providertest.Text("resumed"),
		call("third", "subagent_message", map[string]any{"subagent": "plumber", "text": "once more"}),
		providertest.Text("resumed again"),
	)
	worker := newHeldScript()
	d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a worker": worker})

	d.postMessage(t, d.home.ID, "find a plumber", "", 202)
	<-worker.started
	d.waitIdle(t, d.home.ID)
	plumber := d.child(t, "plumber")
	if plumber.ProjectID != "" || plumber.ComputerID != d.computer {
		t.Fatalf("worker without a project = %+v", plumber)
	}
	d.postMessage(t, d.home.ID, "stop it", "", 202)
	d.waitIdle(t, d.home.ID)
	if got := d.child(t, "plumber").State; got != store.SessionCancelled {
		t.Fatalf("after subagent_cancel = %s", got)
	}
	if results := toolResultsByCall(eika.Requests()[3]); !strings.Contains(results["cancel"], `"state":"cancelled"`) {
		t.Fatalf("cancel result = %q", results["cancel"])
	}

	// The user's abort of the subagent's run is a cancel, not a failure.
	d.postMessage(t, d.home.ID, "try again", "", 202)
	<-worker.started
	d.waitIdle(t, d.home.ID)
	run := d.runState(t, plumber.ID)
	if run.Run == nil {
		t.Fatal("no subagent run")
	}
	if rec := request(t, d.Server, "POST", "/api/runs/"+run.Run.ID+"/abort", nil); rec.Code != 200 {
		t.Fatalf("abort = %d %s", rec.Code, rec.Body.String())
	}
	d.waitState(t, "plumber", store.SessionCancelled)

	// Shutdown cancels a running subagent and records it.
	d.postMessage(t, d.home.ID, "once more", "", 202)
	<-worker.started
	d.waitIdle(t, d.home.ID)
	d.Close()
	if got := d.child(t, "plumber").State; got != store.SessionCancelled {
		t.Fatalf("after shutdown = %s", got)
	}
}

// holdAfter runs the first calls of its script and holds the next one until
// its context ends, which keeps a turn going while the test acts on it.
type holdAfter struct {
	*providertest.Provider
	calls   int
	count   atomic.Int32
	holding chan struct{}
}

func newHoldAfter(calls int, steps ...providertest.Step) *holdAfter {
	return &holdAfter{Provider: providertest.New(steps...), calls: calls, holding: make(chan struct{}, 1)}
}

func (h *holdAfter) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	if int(h.count.Add(1)) <= h.calls {
		return h.Provider.Stream(ctx, req)
	}
	h.holding <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSubagentThatEndsWithoutReportingIsAskedOnceThenItsFinalMessageIsTheReport(t *testing.T) {
	d := newDelegating(t)
	eika := providertest.New(
		call("start-a", "subagent_start", map[string]any{"agent": "researcher", "name": "asks", "brief": "Find flights"}),
		call("start-b", "subagent_start", map[string]any{"agent": "researcher", "name": "silent", "brief": "Find hotels"}),
		providertest.Text("started both"),
	)
	// "asks" reports when it is asked to draft one; "silent" never calls report.
	asks := providertest.New(
		providertest.Text("Flights are cheapest on Tuesday."),
		call("draft", "report", map[string]any{"summary": "Tuesday is cheapest", "follow_ups": []string{"book Tuesday"}}),
		providertest.Text("reported"),
	)
	silent := providertest.New(
		providertest.Text("Hotels near the station are best."),
		providertest.Text("Understood. The station hotels are best."),
	)
	d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a researcher": byTask{"Find flights": asks, "Find hotels": silent}})

	d.postMessage(t, d.home.ID, "plan the trip", "", 202)
	d.waitIdle(t, d.home.ID)
	d.waitState(t, "asks", store.SessionIdle)
	d.waitState(t, "silent", store.SessionIdle)

	if got, want := d.child(t, "asks").Result, "Tuesday is cheapest\n\nFollow-ups:\n- book Tuesday"; got != want {
		t.Errorf("drafted report = %q, want %q", got, want)
	}
	if reqs := asks.Requests(); len(reqs) != 3 || !strings.Contains(fmt.Sprint(reqs[1].Messages), "Draft your report now with the report tool.") {
		t.Errorf("asks made %d calls; the second must carry the draft request", len(reqs))
	}
	// A second silence ends the asking: its last message is the report.
	if got := d.child(t, "silent").Result; got != "Understood. The station hotels are best." {
		t.Errorf("fallback report = %q", got)
	}
	if reqs := silent.Requests(); len(reqs) != 2 {
		t.Errorf("silent made %d calls, want 2: the draft request is sent once", len(reqs))
	}
}

// byTask hands a subagent's calls to the script whose brief it was given.
type byTask map[string]provider.Provider

func (b byTask) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	for brief, p := range b {
		if strings.Contains(fmt.Sprint(req.Messages), brief) {
			return p.Stream(ctx, req)
		}
	}
	return nil, fmt.Errorf("no script for this brief")
}

func TestSubagentMaxRunningSettingLimitsRunsAndIsValidated(t *testing.T) {
	d := newDelegating(t)
	if got := decodeBody[struct {
		Defaults struct {
			SubagentMaxRunning int `json:"subagent_max_running"`
		} `json:"defaults"`
	}](t, request(t, d.Server, "GET", "/api/settings", nil), 200).Defaults.SubagentMaxRunning; got != 4 {
		t.Errorf("default subagent_max_running = %d, want 4", got)
	}
	for _, bad := range []string{`0`, `33`, `-1`, `1.5`, `4.0`, `4e0`, `"4"`, `true`, `[]`, `{}`} {
		rec := request(t, d.Server, "PUT", "/api/settings", json.RawMessage(`{"subagent_max_running":`+bad+`}`))
		if rec.Code != 400 {
			t.Errorf("subagent_max_running = %s: status %d, want 400", bad, rec.Code)
		}
	}
	if rec := request(t, d.Server, "PUT", "/api/settings", map[string]any{"subagent_max_running": 1}); rec.Code != 200 {
		t.Fatalf("set the limit: %d %s", rec.Code, rec.Body.String())
	}

	eika := providertest.New(
		call("one", "subagent_start", map[string]any{"agent": "worker", "name": "one", "brief": "first"}),
		call("two", "subagent_start", map[string]any{"agent": "researcher", "name": "two", "brief": "second"}),
		providertest.Text("only one fits"),
		call("again", "subagent_start", map[string]any{"agent": "researcher", "name": "two", "brief": "second"}),
		providertest.Text("started the second"),
	)
	worker := newHeldScript(call("done", "report", map[string]any{"summary": "first done"}), providertest.Text("ok"))
	researcher := providertest.New(call("done", "report", map[string]any{"summary": "second done"}), providertest.Text("ok"))
	d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a worker": worker, "You are a researcher": researcher})

	d.postMessage(t, d.home.ID, "start two", "", 202)
	d.waitIdle(t, d.home.ID)
	results := toolResultsByCall(eika.Requests()[2])
	if !strings.Contains(results["one"], `"note":"Started.`) || !strings.Contains(results["two"], "at most 1") {
		t.Fatalf("results = %q", results)
	}
	if _, err := d.store.Subagent(t.Context(), d.home.ID, "two"); err == nil {
		t.Fatal("the refused subagent has a row")
	}
	worker.open()
	d.waitState(t, "one", store.SessionIdle)
	d.postMessage(t, d.home.ID, "try the second again", "", 202)
	d.waitIdle(t, d.home.ID)
	d.waitState(t, "two", store.SessionIdle)
	if got := d.child(t, "two").Result; got != "second done" {
		t.Errorf("second report = %q", got)
	}
}

func TestAbortingTheHomeReplyLeavesItsSubagentsRunning(t *testing.T) {
	d := newDelegating(t)
	// Eika starts the worker, and its next call is held: the home run is
	// still going when the user aborts it.
	eika := newHoldAfter(1, call("start", "subagent_start", map[string]any{"agent": "worker", "name": "plumber", "brief": "Find a plumber"}))
	worker := newHeldScript(call("done", "report", map[string]any{"summary": "Found one"}), providertest.Text("done"))
	d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a worker": worker})

	d.postMessage(t, d.home.ID, "find a plumber", "", 202)
	<-worker.started
	<-eika.holding
	home := d.runState(t, d.home.ID)
	if home.Run == nil {
		t.Fatal("home has no run to abort")
	}
	if rec := request(t, d.Server, "POST", "/api/runs/"+home.Run.ID+"/abort", nil); rec.Code != 200 {
		t.Fatalf("abort home = %d %s", rec.Code, rec.Body.String())
	}
	d.waitIdle(t, d.home.ID)
	if got := d.child(t, "plumber").State; got != store.SessionRunning {
		t.Fatalf("after the home abort the worker is %s, want running", got)
	}
	if d.runState(t, d.child(t, "plumber").ID).Run == nil {
		t.Fatal("the worker's run was stopped with the home reply")
	}
	worker.open()
	d.waitState(t, "plumber", store.SessionIdle)
	if got := d.child(t, "plumber").Result; got != "Found one" {
		t.Errorf("report after the home abort = %q", got)
	}
}

func TestShutdownCancelsRunningSubagentsWithoutReports(t *testing.T) {
	d := newDelegating(t)
	eika := providertest.New(
		call("one", "subagent_start", map[string]any{"agent": "worker", "name": "one", "brief": "first"}),
		call("two", "subagent_start", map[string]any{"agent": "researcher", "name": "two", "brief": "second"}),
		providertest.Text("started"),
	)
	worker, researcher := newHeldScript(), newHeldScript()
	d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a worker": worker, "You are a researcher": researcher})
	// Each held subagent call keeps a slot, and Eika's last call needs a third.
	if _, err := d.store.OverrideProvider(t.Context(), d.testProvider.ID, store.ProviderOverride{Slots: new(3)}); err != nil {
		t.Fatal(err)
	}
	d.postMessage(t, d.home.ID, "start both", "", 202)
	<-worker.started
	<-researcher.started
	d.waitIdle(t, d.home.ID)
	d.Close()
	for _, name := range []string{"one", "two"} {
		if row := d.child(t, name); row.State != store.SessionCancelled || row.Result != "" {
			t.Errorf("%s after shutdown = %s with report %q", name, row.State, row.Result)
		}
	}
}

func TestFallbackReportIgnoresAnEarlierAssignmentsAnswer(t *testing.T) {
	d := newDelegating(t)
	eika := providertest.New(
		call("start", "subagent_start", map[string]any{"agent": "researcher", "name": "flights", "brief": "Find flights"}),
		providertest.Text("started"),
		call("resume", "subagent_message", map[string]any{"subagent": "flights", "text": "Now find trains"}),
		providertest.Text("resumed"),
	)
	// Neither assignment calls report. The first ends with text; the second,
	// and its draft request, with none.
	researcher := providertest.New(
		providertest.Text("Flights are cheapest on Tuesday."),
		providertest.Text("Tuesday it is."),
		providertest.Text(""),
		providertest.Text(""),
	)
	d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a researcher": researcher})

	d.postMessage(t, d.home.ID, "find flights", "", 202)
	d.waitIdle(t, d.home.ID)
	d.waitState(t, "flights", store.SessionIdle)
	if got := d.child(t, "flights").Result; got != "Tuesday it is." {
		t.Fatalf("first fallback report = %q", got)
	}
	d.postMessage(t, d.home.ID, "now trains", "", 202)
	d.waitIdle(t, d.home.ID)
	waitFor(t, "the second assignment's report", func() bool {
		row := d.child(t, "flights")
		return row.State == store.SessionIdle && row.Result != "Tuesday it is."
	})
	if got := d.child(t, "flights").Result; got != "The subagent ended without a report or a final message." {
		t.Errorf("second fallback report = %q, want the no-report sentence", got)
	}
}

// cancelled is the part of a cancelled subagent the test reads.
type cancelled struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

func TestUserCancelsASubagentThroughTheAPI(t *testing.T) {
	d := newDelegating(t)
	eika := providertest.New(
		call("start", "subagent_start", map[string]any{"agent": "worker", "name": "plumber", "brief": "Find a plumber"}),
		providertest.Text("on it"),
	)
	worker := newHeldScript()
	d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a worker": worker})
	d.postMessage(t, d.home.ID, "find a plumber", "", 202)
	<-worker.started
	d.waitIdle(t, d.home.ID)
	plumber := d.waitState(t, "plumber", store.SessionRunning)

	rec := request(t, d.Server, "POST", "/api/sessions/"+plumber.ID+"/cancel", nil)
	if got := decodeBody[cancelled](t, rec, 200); got.State != string(store.SessionCancelled) || got.Name != "plumber" {
		t.Fatalf("cancel = %+v", got)
	}
	if got := d.child(t, "plumber").State; got != store.SessionCancelled {
		t.Fatalf("stored state = %s", got)
	}
	// Nothing is left to stop, which changes nothing.
	decodeBody[cancelled](t, request(t, d.Server, "POST", "/api/sessions/"+plumber.ID+"/cancel", nil), 200)

	for path, want := range map[string]int{
		"/api/sessions/" + d.home.ID + "/cancel": 409,
		"/api/sessions/missing/cancel":           404,
	} {
		if rec := request(t, d.Server, "POST", path, nil); rec.Code != want {
			t.Errorf("POST %s = %d %s, want %d", path, rec.Code, rec.Body.String(), want)
		}
	}
}
