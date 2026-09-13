package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/erlidev/eika/internal/tool"
)

// maxAgentWait bounds how many children one wait_agents call may name, which
// is the same bound the spawner puts on how many children a session runs.
const maxAgentWait = 16

// Subagents spawns child agents and reports on them. It is how the agent
// tools reach the spawner without the tool package knowing what a workspace
// is; internal/subagent is the one implementation and the server injects it.
//
// Every method acts on behalf of one parent session, named in the request or
// the argument, so a run can only see and wait for its own children.
type Subagents interface {
	// Spawn starts a child and returns when it has finished.
	Spawn(ctx context.Context, req SpawnRequest) (AgentResult, error)
	// Start starts a child and returns as soon as it is running.
	Start(ctx context.Context, req SpawnRequest) (AgentResult, error)
	// Wait returns when each named child of the session has finished.
	Wait(ctx context.Context, parentSessionID string, ids []string) ([]AgentResult, error)
	// List reports the children of a session, running ones included.
	List(ctx context.Context, parentSessionID string) ([]AgentResult, error)
}

// SpawnRequest asks for one child agent.
type SpawnRequest struct {
	// ParentSessionID is the session that spawns the child.
	ParentSessionID string `json:"parent_session_id"`
	// Name is what the parent calls the child. It also names the child's
	// branch, so it is restricted to what a branch name may hold.
	Name string `json:"name"`
	// Task is the first user message the child is given.
	Task string `json:"task"`
	// BranchSuffix overrides the part of the child's branch name taken from
	// Name.
	BranchSuffix string `json:"branch_suffix,omitempty"`
	// Model overrides the model the child runs on.
	Model string `json:"model,omitempty"`
}

// AgentResult is what became of one child agent. It is the tool result the
// parent sees, the row the API reports, and the JSON stored on the subagent.
type AgentResult struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	// Branch is the branch the child worked on and pushed to the hub.
	Branch string `json:"branch"`
	// State is running, done, error, or aborted.
	State string `json:"state"`
	// Commit is the child's head commit after it committed what it left in
	// the tree.
	Commit string `json:"commit,omitempty"`
	// Summary is the child's final assistant message.
	Summary string `json:"summary,omitempty"`
	// DiffStat is `git diff --stat` between the parent's base commit and the
	// child's head.
	DiffStat string `json:"diff_stat,omitempty"`
	// Error says why a child that did not finish cleanly stopped.
	Error string `json:"error,omitempty"`
}

// Done reports whether the child has stopped, whatever it ended as.
func (r AgentResult) Done() bool { return r.State != "" && r.State != "running" }

// Report renders a result as the parent's model sees it.
func (r AgentResult) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "agent %s (%s) is %s\nbranch: %s\n", r.Name, r.ID, r.State, r.Branch)
	if r.Commit != "" {
		fmt.Fprintf(&b, "commit: %s\n", r.Commit)
	}
	fmt.Fprintf(&b, "workspace: %s\nsession: %s\n", r.WorkspaceID, r.SessionID)
	if r.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", r.Error)
	}
	if r.Summary != "" {
		fmt.Fprintf(&b, "\nsummary:\n%s\n", strings.TrimSpace(r.Summary))
	}
	if r.DiffStat != "" {
		fmt.Fprintf(&b, "\nchanges:\n%s\n", strings.TrimSpace(r.DiffStat))
	}
	return b.String()
}

// spawnAgentTool hands a task to a child agent working in a workspace of its
// own, branched from this one.
type spawnAgentTool struct {
	agents Subagents
}

// spawnAgentArgs are the parameters of a spawn_agent call.
type spawnAgentArgs struct {
	Name         string `json:"name"`
	Task         string `json:"task"`
	BranchSuffix string `json:"branch_suffix"`
	Model        string `json:"model"`
	// Wait blocks until the child finishes. It defaults to true, so a model
	// that says nothing gets one child at a time and its result.
	Wait *bool `json:"wait"`
}

// Name identifies the tool to the model.
func (spawnAgentTool) Name() string { return "spawn_agent" }

// Description tells the model what the tool does.
func (spawnAgentTool) Description() string {
	return "Hand a self-contained task to a child agent. " +
		"The child works in its own sandbox, on the same image as this one, cloned from this " +
		"workspace at its current commit, " +
		"on its own branch, and reports back a summary, its branch, its commit, and a diffstat. " +
		"Use it for work that can be described once and checked afterwards. " +
		"Set wait to false to start several children and then call wait_agents."
}

// Schema describes the parameters of a spawn_agent call.
func (spawnAgentTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {"type": "string", "description": "A short name for the child, letters, digits, dashes and underscores. It names its branch."},
    "task": {"type": "string", "description": "The whole task, as the child's first message. It sees none of this conversation."},
    "branch_suffix": {"type": "string", "description": "Override the branch part taken from the name."},
    "model": {"type": "string", "description": "The model the child runs on. Empty uses the default."},
    "wait": {"type": "boolean", "description": "Wait for the child to finish. Defaults to true."}
  },
  "required": ["name", "task"],
  "additionalProperties": false
}`)
}

// Call spawns the child and either waits for it or reports that it is running.
func (t spawnAgentTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[spawnAgentArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if t.agents == nil {
		return tool.Errorf("spawn_agent: this harness cannot spawn agents"), nil
	}
	if strings.TrimSpace(args.Task) == "" {
		return tool.Errorf("spawn_agent: task is required"), nil
	}
	req := SpawnRequest{
		ParentSessionID: c.SessionID,
		Name:            strings.TrimSpace(args.Name),
		Task:            args.Task,
		BranchSuffix:    strings.TrimSpace(args.BranchSuffix),
		Model:           strings.TrimSpace(args.Model),
	}
	if args.Wait != nil && !*args.Wait {
		result, err := t.agents.Start(ctx, req)
		if err != nil {
			return tool.Errorf("spawn_agent: %v", err), nil
		}
		return agentResult(result), nil
	}
	result, err := t.agents.Spawn(ctx, req)
	if err != nil {
		return tool.Errorf("spawn_agent: %v", err), nil
	}
	return agentResult(result), nil
}

// waitAgentsTool blocks until the named children have finished.
type waitAgentsTool struct {
	agents Subagents
}

// waitAgentsArgs are the parameters of a wait_agents call.
type waitAgentsArgs struct {
	IDs []string `json:"ids"`
}

// Name identifies the tool to the model.
func (waitAgentsTool) Name() string { return "wait_agents" }

// Description tells the model what the tool does.
func (waitAgentsTool) Description() string {
	return "Wait for children started with spawn_agent and wait false, and report what each of them did."
}

// Schema describes the parameters of a wait_agents call.
func (waitAgentsTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "ids": {
      "type": "array",
      "items": {"type": "string"},
      "description": "The agent ids spawn_agent returned."
    }
  },
  "required": ["ids"],
  "additionalProperties": false
}`)
}

// Call waits for every named child of this session.
func (t waitAgentsTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[waitAgentsArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if t.agents == nil {
		return tool.Errorf("wait_agents: this harness cannot spawn agents"), nil
	}
	if len(args.IDs) == 0 {
		return tool.Errorf("wait_agents: ids is required"), nil
	}
	if len(args.IDs) > maxAgentWait {
		return tool.Errorf("wait_agents: at most %d agents can be waited for at once", maxAgentWait), nil
	}
	results, err := t.agents.Wait(ctx, c.SessionID, args.IDs)
	if err != nil {
		return tool.Errorf("wait_agents: %v", err), nil
	}
	return agentResults(results), nil
}

// listAgentsTool reports the children of this session.
type listAgentsTool struct {
	agents Subagents
}

// Name identifies the tool to the model.
func (listAgentsTool) Name() string { return "list_agents" }

// Description tells the model what the tool does.
func (listAgentsTool) Description() string {
	return "List the children this session has spawned, with what became of each of them."
}

// Schema describes the parameters of a list_agents call.
func (listAgentsTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}, "additionalProperties": false}`)
}

// Call lists the children of the calling session.
func (t listAgentsTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	if _, err := decodeArgs[struct{}](raw); err != nil {
		return tool.Errorf("%v", err), nil
	}
	if t.agents == nil {
		return tool.Errorf("list_agents: this harness cannot spawn agents"), nil
	}
	results, err := t.agents.List(ctx, c.SessionID)
	if err != nil {
		return tool.Errorf("list_agents: %v", err), nil
	}
	if len(results) == 0 {
		return tool.Text("this session has spawned no agents"), nil
	}
	return agentResults(results), nil
}

// agentResult renders one child as a tool result. A child that failed is an
// error the model can act on: the work is on its branch either way.
func agentResult(r AgentResult) tool.Result {
	details, err := json.Marshal(r)
	if err != nil {
		return tool.Errorf("encode agent result: %v", err)
	}
	return tool.Result{
		Content: r.Report(),
		IsError: r.State == "error" || r.State == "aborted",
		Details: details,
	}
}

// agentResults renders several children as one tool result.
func agentResults(results []AgentResult) tool.Result {
	reports := make([]string, 0, len(results))
	failed := false
	for _, r := range results {
		reports = append(reports, r.Report())
		failed = failed || r.State == "error" || r.State == "aborted"
	}
	details, err := json.Marshal(results)
	if err != nil {
		return tool.Errorf("encode agent results: %v", err)
	}
	return tool.Result{
		Content: strings.Join(reports, "\n"),
		IsError: failed,
		Details: details,
	}
}

var (
	_ tool.Tool = spawnAgentTool{}
	_ tool.Tool = waitAgentsTool{}
	_ tool.Tool = listAgentsTool{}
)
