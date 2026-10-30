//go:build docker

package server_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/workspace"
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
// lands on its own branch, and the parent gets a result naming it.
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
	// named the same do not write over each other.
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

	// The work is on the child's branch in the parent's repository, which is
	// what lets the parent merge it.
	content, err := a.host.branchShow(t.Context(), ws.ID, child.Branch, "notes.txt")
	if err != nil {
		t.Fatalf("read notes.txt from the child's branch: %v", err)
	}
	if strings.TrimSpace(content) != "from the child" {
		t.Errorf("branch notes.txt = %q, want what the child wrote", content)
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

	// The child worked in a worktree in the parent's container, not a
	// container of its own, and the worktree stays for the user to look at.
	if _, err := a.host.Inspect(t.Context(), child.WorkspaceID); err == nil {
		t.Error("the child has a container of its own")
	}
	workspaces := decodeBody[workspacesWire](t, request(t, a.Server, "GET", "/api/workspaces", nil), 200)
	if len(workspaces.Workspaces) != 2 {
		t.Fatalf("workspaces = %+v, want the parent's and the child's", workspaces.Workspaces)
	}
	childWS := decodeBody[workspaceWire](t, request(t, a.Server, "GET", "/api/workspaces/"+child.WorkspaceID, nil), 200)
	if childWS.WorktreeOf != ws.ID || childWS.State != "running" || childWS.Branch != child.Branch {
		t.Errorf("child workspace = %+v, want a running worktree of %s on %s", childWS, ws.ID, child.Branch)
	}
	// The project is local, so the parent's container holds the project's
	// own directory.
	tree := filepath.Join(dir, filepath.FromSlash(workspace.WorktreeDir(child.WorkspaceID)))
	if data, err := os.ReadFile(filepath.Join(tree, "notes.txt")); err != nil || strings.TrimSpace(string(data)) != "from the child" {
		t.Errorf("notes.txt in the child's worktree = %q, %v, want what the child wrote", data, err)
	}
	// The branch is in the parent's own repository, so its agent can merge it
	// without going through the hub, and the parent's tree does not list the
	// worktree as a change of its own.
	if content, err := git(t.Context(), dir, "show", child.Branch+":notes.txt"); err != nil || content != "from the child" {
		t.Errorf("the parent's repository reads %q, %v from the child's branch", content, err)
	}
	if status, err := git(t.Context(), dir, "status", "--porcelain", "--untracked-files=all"); err != nil || strings.Contains(status, ".eika") || strings.Contains(status, "notes.txt") {
		t.Errorf("parent status = %q, %v, want the child's worktree kept out of it", status, err)
	}
}

// TestAChildWorkspaceLivesInItsParentsContainer checks what the API does with
// a worktree workspace: its files and changes are its worktree's, it starts
// and stops with its holder, and deleting it takes the worktree but keeps
// the branch.
func TestAChildWorkspaceLivesInItsParentsContainer(t *testing.T) {
	a := newAPI(t)
	parent, child := spawnChild(t, a, "echo from the child > notes.txt")
	childWS := "/api/workspaces/" + child.WorkspaceID

	files := decodeBody[filesWire](t, request(t, a.Server, "GET", childWS+"/files?path=.", nil), 200)
	if !slices.ContainsFunc(files.Entries, func(e fileEntryWire) bool { return e.Path == "notes.txt" }) {
		t.Errorf("child files = %+v, want notes.txt at the worktree's root", files.Entries)
	}
	if content, err := a.host.branchShow(t.Context(), parent.ID, child.Branch, "notes.txt"); err != nil || content != "from the child" {
		t.Errorf("the child's branch reads %q, %v, want its change", content, err)
	}
	if rec := request(t, a.Server, "GET", childWS+"/files?path=../../README.md", nil); rec.Code != 403 {
		t.Errorf("reading past the worktree = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	for _, action := range []string{"stop", "start"} {
		if rec := request(t, a.Server, "POST", childWS+"/"+action, nil); rec.Code != 409 {
			t.Errorf("%s on the child = %d, want 409: %s", action, rec.Code, rec.Body.String())
		}
	}
	decodeBody[workspaceWire](t, request(t, a.Server, "POST", "/api/workspaces/"+parent.ID+"/stop", nil), 200)
	if got := decodeBody[workspaceWire](t, request(t, a.Server, "GET", childWS, nil), 200); got.State != "stopped" {
		t.Errorf("child state after its holder stopped = %q, want stopped", got.State)
	}
	rec := request(t, a.Server, "GET", childWS+"/files?path=.", nil)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), parent.ID) {
		t.Errorf("child files with its holder stopped = %d %s, want 409 naming the holder", rec.Code, rec.Body.String())
	}
	if rec := request(t, a.Server, "DELETE", childWS, nil); rec.Code != 409 {
		t.Errorf("deleting the child with its holder stopped = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	decodeBody[workspaceWire](t, request(t, a.Server, "POST", "/api/workspaces/"+parent.ID+"/start", nil), 200)
	if got := decodeBody[workspaceWire](t, request(t, a.Server, "GET", childWS, nil), 200); got.State != "running" {
		t.Errorf("child state after its holder started = %q, want running", got.State)
	}

	if rec := request(t, a.Server, "DELETE", childWS, nil); rec.Code != 204 {
		t.Fatalf("delete the child = %d: %s", rec.Code, rec.Body.String())
	}
	dir, err := a.host.dirOf(parent.ID)
	if err != nil {
		t.Fatalf("parent workspace directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(workspace.WorktreeDir(child.WorkspaceID)))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the child's worktree after deleting it: %v, want it gone", err)
	}
	if content, err := git(t.Context(), dir, "show", child.Branch+":notes.txt"); err != nil || content != "from the child" {
		t.Errorf("the child's branch after deleting it reads %q, %v, want its work kept", content, err)
	}
	if _, err := a.host.Inspect(t.Context(), parent.ID); err != nil {
		t.Errorf("the parent's container after deleting the child: %v", err)
	}
}

// TestDeletingAParentTakesItsChildWorkspaces checks that worktree workspaces
// go with the container they live in.
func TestDeletingAParentTakesItsChildWorkspaces(t *testing.T) {
	a := newAPI(t)
	parent, child := spawnChild(t, a, "echo from the child > notes.txt")
	if rec := request(t, a.Server, "DELETE", "/api/workspaces/"+parent.ID, nil); rec.Code != 204 {
		t.Fatalf("delete the parent = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, a.Server, "GET", "/api/workspaces/"+child.WorkspaceID, nil); rec.Code != 404 {
		t.Errorf("child workspace after its holder went = %d, want 404", rec.Code)
	}
	if rec := request(t, a.Server, "GET", "/api/sessions/"+child.SessionID, nil); rec.Code != 404 {
		t.Errorf("child session after its holder went = %d, want 404", rec.Code)
	}
}

// TestAGrandchildSharesTheRootContainer checks that a child's child works in
// the container its grandparent's workspace has, starting from the child's
// work.
func TestAGrandchildSharesTheRootContainer(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)

	// Each spawn waits, so the calls reach the provider in this order: the
	// parent spawns, the child writes and spawns, the grandchild writes and
	// ends, then the child and the parent end.
	a.script(
		spawnStep("c1", "worker", "write and delegate", true),
		providertest.Calls("", providertest.Call("c2", "bash", map[string]any{"command": "echo child > child.txt"})),
		spawnStep("c3", "helper", "write the rest", true),
		providertest.Calls("", providertest.Call("c4", "bash", map[string]any{"command": "echo grandchild > grand.txt"})),
		providertest.Text("wrote grand.txt"),
		providertest.Text("wrote child.txt"),
		providertest.Text("all done"),
	)
	a.postMessage(t, sess.ID, "hand it down", "", 202)
	if state := a.waitIdle(t, sess.ID); state.Run == nil || state.Run.State != "done" {
		t.Fatalf("parent run = %+v, want done", state.Run)
	}
	child := a.waitAgent(t, sess.ID)
	grand := a.waitAgent(t, child.SessionID)
	if grand.State != "done" || !strings.HasPrefix(grand.Branch, child.Branch+"-helper-") {
		t.Fatalf("grandchild = %+v, want done on a branch of %s", grand, child.Branch)
	}
	got := decodeBody[workspaceWire](t, request(t, a.Server, "GET", "/api/workspaces/"+grand.WorkspaceID, nil), 200)
	if got.WorktreeOf != ws.ID {
		t.Errorf("grandchild worktree_of = %q, want the root workspace %s", got.WorktreeOf, ws.ID)
	}
	root, err := a.host.dirOf(ws.ID)
	if err != nil {
		t.Fatalf("root workspace directory: %v", err)
	}
	// The grandchild started from the child's work in progress.
	for file, want := range map[string]string{"child.txt": "child", "grand.txt": "grandchild"} {
		if content, err := git(t.Context(), root, "show", grand.Branch+":"+file); err != nil || content != want {
			t.Errorf("%s on the grandchild's branch = %q, %v, want %q", file, content, err, want)
		}
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
	// spawner that gave up waiting when its context ended would commit
	// and stop the child's workspace while its agent loop was still writing
	// to it.
	if childRun := a.runState(t, final.SessionID); childRun.Active {
		t.Errorf("child run = %+v, want it finished before the child was reported on", childRun)
	}
	// An aborted child still commits: the work is not thrown away.
	content, err := a.host.branchShow(t.Context(), ws.ID, final.Branch, "notes.txt")
	if err != nil {
		t.Fatalf("read notes.txt from the child's branch: %v", err)
	}
	if strings.TrimSpace(content) != "half done" {
		t.Errorf("branch notes.txt = %q, want what the child managed to write", content)
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

	a.Close()

	// The row is final by the time Close returns: the store is about to go.
	stopped := a.agents(t, sess.ID)
	if len(stopped.Agents) != 1 || stopped.Agents[0].State == "running" {
		t.Fatalf("children after shutdown = %+v, want the child recorded as stopped", stopped.Agents)
	}
	if state := a.runState(t, child.SessionID); state.Active {
		t.Errorf("child run = %+v, want it finished", state)
	}
}
