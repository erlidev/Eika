//go:build docker

package server_test

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// The wire shapes the subagent routes answer with, written out so that a
// change to a handler's struct fails a test instead of passing quietly.
type agentResultWire struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Branch      string `json:"branch"`
	State       string `json:"state"`
	Commit      string `json:"commit"`
	Summary     string `json:"summary"`
	DiffStat    string `json:"diff_stat"`
	Error       string `json:"error"`
}

type agentWire struct {
	ID              string           `json:"id"`
	ParentSessionID string           `json:"parent_session_id"`
	SessionID       string           `json:"session_id"`
	WorkspaceID     string           `json:"workspace_id"`
	Name            string           `json:"name"`
	Branch          string           `json:"branch"`
	State           string           `json:"state"`
	Result          *agentResultWire `json:"result"`
	Agents          []agentWire      `json:"agents"`
}

type agentsWire struct {
	SessionID string      `json:"session_id"`
	Agents    []agentWire `json:"agents"`
}

type mergeWire struct {
	WorkspaceID string   `json:"workspace_id"`
	Branch      string   `json:"branch"`
	Strategy    string   `json:"strategy"`
	Merged      bool     `json:"merged"`
	Commit      string   `json:"commit"`
	Conflicts   []string `json:"conflicts"`
	Message     string   `json:"message"`
}

// spawnStep is a model response that hands a task to a child agent.
func spawnStep(callID, name, task string, wait bool) providertest.Step {
	return providertest.Calls("handing it over", providertest.Call(callID, "spawn_agent", map[string]any{
		"name": name, "task": task, "wait": wait,
	}))
}

// agents reads the children of a session.
func (a *api) agents(t *testing.T, sessionID string) agentsWire {
	t.Helper()
	rec := request(t, a.Server, "GET", "/api/sessions/"+sessionID+"/agents", nil)
	return decodeBody[agentsWire](t, rec, 200)
}

// waitAgent waits until a session's one child reaches a state that is not
// running, and returns it. The child reports on a goroutine of its own, so
// the row it leaves is something that becomes true.
func (a *api) waitAgent(t *testing.T, sessionID string) agentWire {
	t.Helper()
	var child agentWire
	waitFor(t, "the child agent to finish", func() bool {
		listed := a.agents(t, sessionID)
		if len(listed.Agents) != 1 {
			return false
		}
		child = listed.Agents[0]
		return child.State != "running"
	})
	return child
}

// toolResults returns the content of every tool result in a session's path.
func (a *api) toolResults(t *testing.T, sessionID string) []string {
	t.Helper()
	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sessionID+"/path", nil), 200)
	var out []string
	for _, e := range path.Entries {
		if e.Kind == "tool_result" {
			out = append(out, e.Message.Content)
		}
	}
	return out
}

// spawnChild sets up a session whose run hands one task to a child agent that
// writes a file, and returns the parent's workspace and what the child became.
func spawnChild(t *testing.T, a *api, command string) (workspaceWire, agentWire) {
	t.Helper()
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)

	// One script serves both runs: spawn_agent blocks the parent while the
	// child works, so the calls reach the provider in this order.
	a.script(
		spawnStep("c1", "worker", "write the notes", true),
		providertest.Calls("", providertest.Call("c2", "bash", map[string]any{"command": command})),
		providertest.Text("wrote notes.txt"),
		providertest.Text("the child is done"),
	)
	a.postMessage(t, sess.ID, "hand it to a child", "", 202)
	state := a.waitIdle(t, sess.ID)
	if state.Run == nil || state.Run.State != "done" {
		t.Fatalf("parent run = %+v, want done", state.Run)
	}
	return ws, a.waitAgent(t, sess.ID)
}

// TestSpawnAgentRunsAChildAndReportsBack walks the whole subagent flow: the
// parent hands over a task, a child works in a workspace of its own, its work
// reaches the hub on its own branch, and the parent gets a result naming it.
func TestSpawnAgentRunsAChildAndReportsBack(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)

	conn := stream(t, a.Server, "topics="+event.SessionTopic(sess.ID))
	a.waitSubscribed(t)

	a.script(
		spawnStep("c1", "worker", "write the notes", true),
		providertest.Calls("", providertest.Call("c2", "bash",
			map[string]any{"command": "echo from the child > notes.txt"})),
		providertest.Text("wrote notes.txt"),
		providertest.Text("the child is done"),
	)
	a.postMessage(t, sess.ID, "hand it to a child", "", 202)

	events := collect(t, conn, event.TypeSubagentFinished)
	var started event.SubagentStarted
	var finished event.SubagentFinished
	for _, e := range events {
		switch e.Type {
		case event.TypeSubagentStarted:
			if err := e.DecodePayload(&started); err != nil {
				t.Fatalf("decode subagent.started: %v", err)
			}
		case event.TypeSubagentFinished:
			if err := e.DecodePayload(&finished); err != nil {
				t.Fatalf("decode subagent.finished: %v", err)
			}
		}
		if e.Topic != event.SessionTopic(sess.ID) {
			t.Errorf("event %s arrived on topic %q, want the parent session's", e.Type, e.Topic)
		}
	}
	// The branch carries a tag from the child's id, so two children a parent
	// named the same do not write over each other in the hub.
	if started.SubagentID == "" || started.Name != "worker" {
		t.Errorf("subagent.started = %+v, want the child named", started)
	}
	if !strings.HasPrefix(started.Branch, "main-worker-") || started.Branch == "main-worker-" {
		t.Errorf("subagent.started branch = %q, want main-worker-<tag>", started.Branch)
	}
	if started.Task != "write the notes" {
		t.Errorf("subagent.started task = %q, want the task the parent gave", started.Task)
	}
	if finished.SubagentID != started.SubagentID || finished.State != "done" {
		t.Errorf("subagent.finished = %+v, want the same child, done", finished)
	}
	if finished.Summary != "wrote notes.txt" || !strings.Contains(finished.DiffStat, "notes.txt") {
		t.Errorf("subagent.finished = %+v, want the child's summary and diffstat", finished)
	}

	state := a.waitIdle(t, sess.ID)
	if state.Run == nil || state.Run.State != "done" {
		t.Fatalf("parent run = %+v, want done", state.Run)
	}
	child := a.waitAgent(t, sess.ID)
	if child.State != "done" || child.Branch != started.Branch || child.Name != "worker" {
		t.Fatalf("child = %+v, want a finished worker on %s", child, started.Branch)
	}
	if child.SessionID == sess.ID || child.WorkspaceID == ws.ID {
		t.Errorf("child = %+v, want a session and a workspace of its own", child)
	}
	if child.Result == nil {
		t.Fatal("a finished child reported no result")
	}
	if child.Result.Summary != "wrote notes.txt" || child.Result.Commit == "" {
		t.Errorf("result = %+v, want the child's last message and its commit", child.Result)
	}
	if len(child.Agents) != 0 {
		t.Errorf("child agents = %+v, want none", child.Agents)
	}

	// The work is in the hub, on the child's branch, which is what lets the
	// parent fetch and merge it.
	content, err := a.host.hubShow(t.Context(), "demo", child.Branch, "notes.txt")
	if err != nil {
		t.Fatalf("read notes.txt from the hub: %v", err)
	}
	if strings.TrimSpace(content) != "from the child" {
		t.Errorf("hub notes.txt = %q, want what the child wrote", content)
	}

	// The parent's model saw the branch, the commit, and the summary.
	results := a.toolResults(t, sess.ID)
	if len(results) != 1 {
		t.Fatalf("tool results = %v, want the one spawn_agent result", results)
	}
	for _, want := range []string{child.Branch, child.Result.Commit, "wrote notes.txt", "notes.txt"} {
		if !strings.Contains(results[0], want) {
			t.Errorf("tool result = %q, want %q in it", results[0], want)
		}
	}

	// A finished child's workspace is stopped, not destroyed: the user opens
	// it to see what the child did.
	host, err := a.host.Inspect(t.Context(), child.WorkspaceID)
	if err != nil {
		t.Fatalf("inspect the child workspace: %v", err)
	}
	if string(host.State) != "stopped" {
		t.Errorf("child workspace state = %q, want stopped", host.State)
	}
	workspaces := decodeBody[workspacesWire](t, request(t, a.Server, "GET", "/api/workspaces", nil), 200)
	if len(workspaces.Workspaces) != 2 {
		t.Errorf("workspaces = %+v, want the parent's and the child's", workspaces.Workspaces)
	}
}

// TestAbortingAParentRunAbortsItsChildren checks that a child whose parent is
// stopped stops too, and still leaves its work on its branch.
func TestAbortingAParentRunAbortsItsChildren(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)

	// The child writes a file and then asks a question nobody answers, which
	// is what holds the whole tree open until the abort arrives.
	a.script(
		spawnStep("c1", "worker", "write the notes", true),
		providertest.Calls("", providertest.Call("c2", "bash",
			map[string]any{"command": "echo half done > notes.txt"})),
		askStep("c3", "should I carry on?"),
	)
	run := a.postMessage(t, sess.ID, "hand it to a child", "", 202)

	var child agentWire
	waitFor(t, "the child to start", func() bool {
		listed := a.agents(t, sess.ID)
		if len(listed.Agents) != 1 {
			return false
		}
		child = listed.Agents[0]
		return true
	})
	a.waitQuestion(t, child.SessionID)

	aborted := decodeBody[runWire](t, request(t, a.Server, "POST", "/api/runs/"+run.ID+"/abort", nil), 200)
	if aborted.State != "aborted" {
		t.Fatalf("parent run = %+v, want aborted", aborted)
	}
	final := a.waitAgent(t, sess.ID)
	if final.State != "aborted" {
		t.Fatalf("child = %+v, want aborted with its parent", final)
	}
	// The child's own run was over before the spawner reported on it. A
	// spawner that gave up waiting when its context ended would commit, push,
	// and stop the child's workspace while its agent loop was still writing
	// to it.
	if childRun := a.runState(t, final.SessionID); childRun.Active {
		t.Errorf("child run = %+v, want it finished before the child was reported on", childRun)
	}
	// An aborted child still commits and pushes: the work is not thrown away.
	content, err := a.host.hubShow(t.Context(), "demo", final.Branch, "notes.txt")
	if err != nil {
		t.Fatalf("read notes.txt from the hub: %v", err)
	}
	if strings.TrimSpace(content) != "half done" {
		t.Errorf("hub notes.txt = %q, want what the child managed to write", content)
	}
}

// TestAbortSubagentRouteStopsOneChild checks the route the UI stops a child
// with, on a child the parent started without waiting for it.
func TestAbortSubagentRouteStopsOneChild(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)

	// With wait false the parent and the child run at the same time, so the
	// two calls that follow the spawn race for the script. Both are the same
	// question, so whichever gets which, each run ends up waiting on one.
	a.script(
		spawnStep("c1", "worker", "write the notes", false),
		askStep("c2", "hold on"),
		askStep("c3", "hold on"),
	)
	a.postMessage(t, sess.ID, "start a child and carry on", "", 202)

	var child agentWire
	waitFor(t, "the child to start", func() bool {
		listed := a.agents(t, sess.ID)
		if len(listed.Agents) != 1 {
			return false
		}
		child = listed.Agents[0]
		return true
	})
	// The parent did not wait: it got the child's id and went on to its own
	// next model call while the child was still working.
	a.waitQuestion(t, sess.ID)
	results := a.toolResults(t, sess.ID)
	if len(results) != 1 || !strings.Contains(results[0], "is running") {
		t.Fatalf("tool results = %v, want one saying the child is running", results)
	}
	a.waitQuestion(t, child.SessionID)

	stopped := decodeBody[agentWire](t, request(t, a.Server, "POST", "/api/subagents/"+child.ID+"/abort", nil), 200)
	if stopped.ID != child.ID || stopped.State != "aborted" {
		t.Fatalf("aborted child = %+v, want %s aborted", stopped, child.ID)
	}
	if rec := request(t, a.Server, "POST", "/api/subagents/"+child.ID+"/abort", nil); rec.Code != 409 {
		t.Errorf("aborting a finished child = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, a.Server, "POST", "/api/subagents/nosuchagent/abort", nil); rec.Code != 404 {
		t.Errorf("aborting an unknown child = %d, want 404", rec.Code)
	}
}

// TestMergeBringsAChildBranchIntoTheParent checks the route a parent takes a
// child's work back with, and what it reports when git cannot.
func TestMergeBringsAChildBranchIntoTheParent(t *testing.T) {
	t.Run("a clean merge", func(t *testing.T) {
		a := newAPI(t)
		parent, child := spawnChild(t, a, "echo from the child > notes.txt")

		body := decodeBody[mergeWire](t, request(t, a.Server,
			"POST", "/api/workspaces/"+parent.ID+"/merge",
			map[string]any{"source_workspace_id": child.WorkspaceID}), 200)
		if !body.Merged || body.Branch != child.Branch || body.Strategy != "merge" {
			t.Fatalf("merge = %+v, want the child's branch merged", body)
		}
		if body.Commit == "" || len(body.Conflicts) != 0 {
			t.Errorf("merge = %+v, want a commit and no conflicts", body)
		}
		diff := decodeBody[diffWire](t, request(t, a.Server,
			"GET", "/api/workspaces/"+parent.ID+"/diff", nil), 200)
		if strings.Contains(diff.Status, "notes.txt") {
			t.Errorf("workspace status = %q, want the merged file committed, not untracked", diff.Status)
		}
	})

	t.Run("a conflict", func(t *testing.T) {
		a := newAPI(t)
		parent, child := spawnChild(t, a, "echo from the child > notes.txt")

		// The parent writes the same file differently, so the branches cannot
		// both be right and git has to stop.
		dir, err := a.host.dirOf(parent.ID)
		if err != nil {
			t.Fatalf("parent workspace directory: %v", err)
		}
		write(t, dir, "notes.txt", "from the parent\n")
		for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "the parent's own notes"}} {
			if _, err := git(t.Context(), dir, args...); err != nil {
				t.Fatalf("commit in the parent workspace: %v", err)
			}
		}

		body := decodeBody[mergeWire](t, request(t, a.Server,
			"POST", "/api/workspaces/"+parent.ID+"/merge",
			map[string]any{"source_workspace_id": child.WorkspaceID}), 200)
		if body.Merged {
			t.Fatalf("merge = %+v, want it to report conflicts", body)
		}
		if len(body.Conflicts) != 1 || body.Conflicts[0] != "notes.txt" {
			t.Errorf("conflicts = %v, want notes.txt", body.Conflicts)
		}
	})

	t.Run("bad requests", func(t *testing.T) {
		a := newAPI(t)
		project, dir := a.newProject(t, "demo")
		initRepo(t, dir)
		ws := a.newWorkspace(t, project.ID)
		cases := map[string]struct {
			body map[string]any
			want int
		}{
			"neither a source nor a branch": {map[string]any{}, 400},
			"an unknown strategy":           {map[string]any{"branch": "main", "strategy": "squash"}, 400},
			"itself":                        {map[string]any{"source_workspace_id": ws.ID}, 400},
			"a branch the hub has not":      {map[string]any{"branch": "nothing-here"}, 400},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				rec := request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/merge", c.body)
				if rec.Code != c.want {
					t.Errorf("status = %d, want %d: %s", rec.Code, c.want, rec.Body.String())
				}
			})
		}
	})
}

// TestForkWithWorkspaceClonesAtTheEntryCommit checks that a fork can rewind
// the files as well as the conversation.
func TestForkWithWorkspaceClonesAtTheEntryCommit(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)

	a.script(
		providertest.Calls("", providertest.Call("c1", "bash",
			map[string]any{"command": "echo one > one.txt && git add -A && git commit -m one"})),
		providertest.Text("committed one.txt"),
	)
	a.postMessage(t, sess.ID, "make a commit", "", 202)
	a.waitIdle(t, sess.ID)

	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	last := path.Entries[len(path.Entries)-1]
	if last.Commit == "" {
		t.Fatalf("last entry = %+v, want a recorded commit to fork at", last)
	}

	fork := decodeBody[sessionWire](t, request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/fork",
		map[string]any{"entry_id": last.ID, "title": "a what if", "with_workspace": true}), 201)
	if fork.WorkspaceID == ws.ID {
		t.Fatalf("fork = %+v, want a workspace of its own", fork)
	}
	forked := decodeBody[workspaceWire](t,
		request(t, a.Server, "GET", "/api/workspaces/"+fork.WorkspaceID, nil), 200)
	if !strings.HasPrefix(forked.Branch, "main-fork-") {
		t.Errorf("fork branch = %q, want main-fork-<id>", forked.Branch)
	}
	if forked.BaseCommit != last.Commit {
		t.Errorf("fork base commit = %q, want the entry's %q", forked.BaseCommit, last.Commit)
	}
	forkDir, err := a.host.dirOf(fork.WorkspaceID)
	if err != nil {
		t.Fatalf("fork workspace directory: %v", err)
	}
	content, err := git(t.Context(), forkDir, "show", "HEAD:one.txt")
	if err != nil {
		t.Fatalf("read one.txt in the fork: %v", err)
	}
	if strings.TrimSpace(content) != "one" {
		t.Errorf("fork one.txt = %q, want the commit's content", content)
	}

	// An entry with no commit has nothing to clone, and the request says so
	// rather than making an empty workspace.
	root := path.Entries[0]
	if root.Commit == "" {
		rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/fork",
			map[string]any{"entry_id": root.ID, "with_workspace": true})
		if rec.Code != 400 {
			t.Errorf("forking at an entry with no commit = %d, want 400: %s", rec.Code, rec.Body.String())
		}
	}
}

// TestShutdownStopsAChildWhoseParentIsLongDone checks that a detached child
// is stopped and recorded before the harness lets go of its database.
func TestShutdownStopsAChildWhoseParentIsLongDone(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)

	// The parent starts a child without waiting, so the two runs go at once
	// and race for the script; both next steps are the same question, so it
	// does not matter which gets which. The parent's is answered and it ends;
	// the child's is not, so only the shutdown stops it.
	a.script(
		spawnStep("c1", "worker", "write the notes", false),
		askStep("c2", "should I carry on?"),
		askStep("c3", "should I carry on?"),
		providertest.Text("the parent is done"),
	)
	a.postMessage(t, sess.ID, "start a child and carry on", "", 202)

	var child agentWire
	waitFor(t, "the child to start", func() bool {
		listed := a.agents(t, sess.ID)
		if len(listed.Agents) != 1 {
			return false
		}
		child = listed.Agents[0]
		return true
	})
	a.waitQuestion(t, child.SessionID)
	question := a.waitQuestion(t, sess.ID)
	if rec := request(t, a.Server, "POST", "/api/questions/"+question.ID+"/answer",
		map[string]any{"answer": "yes"}); rec.Code != 204 {
		t.Fatalf("answer = %d: %s", rec.Code, rec.Body.String())
	}
	state := a.waitIdle(t, sess.ID)
	if state.Run == nil || state.Run.State != "done" {
		t.Fatalf("parent run = %+v, want done while the child runs on", state.Run)
	}

	a.Server.Close()

	// The row is final by the time Close returns: the store is about to go.
	stopped := a.agents(t, sess.ID)
	if len(stopped.Agents) != 1 || stopped.Agents[0].State == "running" {
		t.Fatalf("children after shutdown = %+v, want the child recorded as stopped", stopped.Agents)
	}
	if state := a.runState(t, child.SessionID); state.Active {
		t.Errorf("child run = %+v, want it finished", state)
	}
}

// TestForkWithWorkspaceRollsBackWhenTheForkFails checks that a workspace made
// for a fork that never happens does not outlive the request.
func TestForkWithWorkspaceRollsBackWhenTheForkFails(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)

	a.script(
		providertest.Calls("", providertest.Call("c1", "bash",
			map[string]any{"command": "echo one > one.txt && git add -A && git commit -m one"})),
		providertest.Text("committed one.txt"),
	)
	a.postMessage(t, sess.ID, "make a commit", "", 202)
	a.waitIdle(t, sess.ID)
	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	last := path.Entries[len(path.Entries)-1]

	// The session goes away while its fork's workspace is being cloned, which
	// is what leaves the fork with nothing to belong to.
	a.host.cloneHook = func() {
		if rec := request(t, a.Server, "DELETE", "/api/sessions/"+sess.ID, nil); rec.Code != 204 {
			t.Errorf("delete the session = %d: %s", rec.Code, rec.Body.String())
		}
	}
	rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/fork",
		map[string]any{"entry_id": last.ID, "with_workspace": true})
	if rec.Code < 400 {
		t.Fatalf("fork = %d, want it to fail once its session is gone: %s", rec.Code, rec.Body.String())
	}

	// Neither the container nor its row is left behind.
	live, err := a.host.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(live) != 1 || live[0].ID != ws.ID {
		t.Errorf("host holds %+v, want the original workspace alone", live)
	}
	workspaces := decodeBody[workspacesWire](t, request(t, a.Server, "GET", "/api/workspaces", nil), 200)
	if len(workspaces.Workspaces) != 1 || workspaces.Workspaces[0].ID != ws.ID {
		t.Errorf("workspace rows = %+v, want the original row alone", workspaces.Workspaces)
	}
}

// TestMergePushesTheSourcesOwnBranch checks that naming a branch to merge does
// not send the source workspace's HEAD to that branch instead of its own.
func TestMergePushesTheSourcesOwnBranch(t *testing.T) {
	a := newAPI(t)
	parent, child := spawnChild(t, a, "echo from the child > notes.txt")

	// The child's workspace becomes the target, and the parent, still on
	// main, the source. The parent has a commit the hub has not seen.
	if rec := request(t, a.Server, "POST", "/api/workspaces/"+child.WorkspaceID+"/start", nil); rec.Code != 200 {
		t.Fatalf("start the child workspace = %d: %s", rec.Code, rec.Body.String())
	}
	dir, err := a.host.dirOf(parent.ID)
	if err != nil {
		t.Fatalf("parent workspace directory: %v", err)
	}
	write(t, dir, "later.txt", "after the child\n")
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "a later commit"}} {
		if _, err := git(t.Context(), dir, args...); err != nil {
			t.Fatalf("commit in the parent workspace: %v", err)
		}
	}

	body := decodeBody[mergeWire](t, request(t, a.Server,
		"POST", "/api/workspaces/"+child.WorkspaceID+"/merge",
		map[string]any{"source_workspace_id": parent.ID, "branch": child.Branch}), 200)
	if !body.Merged || body.Branch != child.Branch {
		t.Fatalf("merge = %+v, want the named branch merged", body)
	}
	// The source pushed the branch it is on, not the branch that was named.
	if _, err := a.host.hubShow(t.Context(), "demo", "main", "later.txt"); err != nil {
		t.Errorf("the source's own branch was not pushed: %v", err)
	}
	content, err := a.host.hubShow(t.Context(), "demo", child.Branch, "notes.txt")
	if err != nil {
		t.Fatalf("read notes.txt from the hub: %v", err)
	}
	if strings.TrimSpace(content) != "from the child" {
		t.Errorf("hub %s notes.txt = %q, want the child's branch untouched", child.Branch, content)
	}
}
