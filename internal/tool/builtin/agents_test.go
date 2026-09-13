package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// fakeAgents is a spawner that records what the tools asked it for and hands
// back whatever the test scripted. It is what keeps these tests about the
// tools rather than about workspaces.
type fakeAgents struct {
	spawned  []builtin.SpawnRequest
	started  []builtin.SpawnRequest
	waited   []string
	result   builtin.AgentResult
	children []builtin.AgentResult
	err      error
}

func (f *fakeAgents) Spawn(_ context.Context, req builtin.SpawnRequest) (builtin.AgentResult, error) {
	f.spawned = append(f.spawned, req)
	return f.result, f.err
}

func (f *fakeAgents) Start(_ context.Context, req builtin.SpawnRequest) (builtin.AgentResult, error) {
	f.started = append(f.started, req)
	return builtin.AgentResult{ID: f.result.ID, Name: req.Name, Branch: f.result.Branch, State: "running"}, f.err
}

func (f *fakeAgents) Wait(_ context.Context, _ string, ids []string) ([]builtin.AgentResult, error) {
	f.waited = append(f.waited, ids...)
	return f.children, f.err
}

func (f *fakeAgents) List(context.Context, string) ([]builtin.AgentResult, error) {
	return f.children, f.err
}

// agentTool returns one registered agent tool, backed by the given spawner.
func agentTool(t *testing.T, name string, agents builtin.Subagents) tool.Tool {
	t.Helper()
	r, err := builtin.Registry(nil, agents)
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	tl, ok := r.Get(name)
	if !ok {
		t.Fatalf("%s is not registered", name)
	}
	return tl
}

// callTool runs a tool with JSON arguments in a session.
func callTool(t *testing.T, tl tool.Tool, args string) tool.Result {
	t.Helper()
	res, err := tl.Call(context.Background(), tool.CallContext{SessionID: "s1"}, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", tl.Name(), err)
	}
	return res
}

// done is a child that finished and reported everything it could.
func done() builtin.AgentResult {
	return builtin.AgentResult{
		ID:          "a1",
		SessionID:   "c1",
		WorkspaceID: "wc1",
		Name:        "worker",
		Branch:      "main-worker",
		State:       "done",
		Commit:      "abc123",
		Summary:     "changed one file",
		DiffStat:    " notes.txt | 1 +",
	}
}

func TestSpawnAgentWaitsByDefault(t *testing.T) {
	agents := &fakeAgents{result: done()}
	res := callTool(t, agentTool(t, "spawn_agent", agents),
		`{"name": "worker", "task": "write the notes"}`)

	if len(agents.spawned) != 1 || len(agents.started) != 0 {
		t.Fatalf("spawned %d, started %d, want one waited-for spawn", len(agents.spawned), len(agents.started))
	}
	req := agents.spawned[0]
	if req.ParentSessionID != "s1" || req.Name != "worker" || req.Task != "write the notes" {
		t.Errorf("request = %+v, want the calling session's own child", req)
	}
	if res.IsError {
		t.Errorf("result = %+v, want a child that finished to be no error", res)
	}
	for _, want := range []string{"worker", "main-worker", "abc123", "changed one file", "notes.txt"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("content = %q, want %q in it", res.Content, want)
		}
	}
	var details builtin.AgentResult
	if err := json.Unmarshal(res.Details, &details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if details != done() {
		t.Errorf("details = %+v, want the whole result", details)
	}
}

func TestSpawnAgentWithoutWaitingReturnsTheChildID(t *testing.T) {
	agents := &fakeAgents{result: done()}
	res := callTool(t, agentTool(t, "spawn_agent", agents),
		`{"name": "worker", "task": "write the notes", "wait": false}`)

	if len(agents.started) != 1 || len(agents.spawned) != 0 {
		t.Fatalf("spawned %d, started %d, want one spawn that did not wait", len(agents.spawned), len(agents.started))
	}
	if res.IsError || !strings.Contains(res.Content, "a1") || !strings.Contains(res.Content, "running") {
		t.Errorf("result = %+v, want the running child's id", res)
	}
}

func TestSpawnAgentPassesTheOverridesOn(t *testing.T) {
	agents := &fakeAgents{result: done()}
	callTool(t, agentTool(t, "spawn_agent", agents),
		`{"name": "worker", "task": "do it", "branch_suffix": "fix", "model": "big"}`)

	req := agents.spawned[0]
	if req.BranchSuffix != "fix" || req.Model != "big" {
		t.Errorf("request = %+v, want the overrides passed through", req)
	}
}

// TestSpawnAgentTakesNoImage pins that a model cannot name the image its
// child runs: nothing validates an image name it made up, so a child runs
// what its parent runs.
func TestSpawnAgentTakesNoImage(t *testing.T) {
	tl := agentTool(t, "spawn_agent", &fakeAgents{result: done()})
	if strings.Contains(string(tl.Schema()), "image") {
		t.Errorf("schema = %s, want no image parameter", tl.Schema())
	}
	// A model that names one anyway is not obeyed; the argument goes nowhere.
	res := callTool(t, tl, `{"name": "worker", "task": "do it", "image": "other:latest"}`)
	if res.IsError || strings.Contains(res.Content, "other:latest") {
		t.Errorf("result = %+v, want the image ignored", res)
	}
}

func TestAgentToolsReportProblemsAsToolErrors(t *testing.T) {
	failing := &fakeAgents{err: errors.New("too many subagents are already running")}
	cases := []struct {
		name string
		tool string
		args string
		// agents is the spawner the tool is built on; nil means a harness
		// that cannot spawn at all.
		agents builtin.Subagents
		want   string
	}{
		{"a spawn the spawner refuses", "spawn_agent", `{"name": "w", "task": "do it"}`, failing, "too many"},
		{"no task", "spawn_agent", `{"name": "w", "task": "  "}`, failing, "task is required"},
		{"a harness that cannot spawn", "spawn_agent", `{"name": "w", "task": "do it"}`, nil, "cannot spawn"},
		{"no ids to wait for", "wait_agents", `{"ids": []}`, failing, "ids is required"},
		{"malformed arguments", "spawn_agent", `{"name":`, failing, "arguments"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := callTool(t, agentTool(t, c.tool, c.agents), c.args)
			if !res.IsError || !strings.Contains(res.Content, c.want) {
				t.Errorf("result = %+v, want an error saying %q", res, c.want)
			}
		})
	}
}

func TestWaitAgentsReportsEveryChild(t *testing.T) {
	failed := done()
	failed.ID, failed.Name, failed.State, failed.Error = "a2", "checker", "error", "the tests did not build"
	agents := &fakeAgents{children: []builtin.AgentResult{done(), failed}}

	res := callTool(t, agentTool(t, "wait_agents", agents), `{"ids": ["a1", "a2"]}`)
	if len(agents.waited) != 2 || agents.waited[0] != "a1" || agents.waited[1] != "a2" {
		t.Errorf("waited for %v, want both ids", agents.waited)
	}
	// One child that failed makes the whole result an error, because the
	// parent has to notice.
	if !res.IsError {
		t.Errorf("result = %+v, want an error when one child failed", res)
	}
	for _, want := range []string{"worker", "checker", "the tests did not build"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("content = %q, want %q in it", res.Content, want)
		}
	}
}

func TestWaitAgentsBoundsHowManyItTakes(t *testing.T) {
	ids := make([]string, 17)
	for i := range ids {
		ids[i] = "a"
	}
	encoded, err := json.Marshal(map[string]any{"ids": ids})
	if err != nil {
		t.Fatalf("encode arguments: %v", err)
	}
	res := callTool(t, agentTool(t, "wait_agents", &fakeAgents{}), string(encoded))
	if !res.IsError || !strings.Contains(res.Content, "at most") {
		t.Errorf("result = %+v, want a bounded wait", res)
	}
}

func TestListAgentsReportsTheChildrenOfTheCallingSession(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		res := callTool(t, agentTool(t, "list_agents", &fakeAgents{}), `{}`)
		if res.IsError || !strings.Contains(res.Content, "no agents") {
			t.Errorf("result = %+v, want a plain answer that there are none", res)
		}
	})
	t.Run("one still running", func(t *testing.T) {
		running := done()
		running.State, running.Summary, running.Commit = "running", "", ""
		res := callTool(t, agentTool(t, "list_agents", &fakeAgents{children: []builtin.AgentResult{running}}), `{}`)
		if res.IsError || !strings.Contains(res.Content, "running") {
			t.Errorf("result = %+v, want the running child listed", res)
		}
	})
}
