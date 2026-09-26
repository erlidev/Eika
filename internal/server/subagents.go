package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
	"github.com/erlidev/eika/internal/workspace"
)

// maxAgentDepth bounds how far the agents route walks down the tree. The
// spawner's own depth limit is lower; this one is what keeps a handler finite
// whatever the rows say.
const maxAgentDepth = 8

// childrenOnly is the depth that reads one session's own children and stops.
const childrenOnly = 0

// agentBody is one child agent on the wire, with the children it spawned in
// turn.
type agentBody struct {
	ID              string `json:"id"`
	ParentSessionID string `json:"parent_session_id"`
	SessionID       string `json:"session_id"`
	WorkspaceID     string `json:"workspace_id"`
	// Name is what the parent called the child; it is the child workspace's
	// name as well.
	Name string `json:"name"`
	// Branch is the branch the child works on and pushes to the hub.
	Branch string `json:"branch"`
	// State is running, done, error, or aborted.
	State      string    `json:"state"`
	CreatedAt  time.Time `json:"created_at"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	// Result is what the child reported: its summary, commit, and diffstat.
	// It is absent while the child is still running.
	Result *builtin.AgentResult `json:"result,omitempty"`
	// Agents are the children of this child.
	Agents []agentBody `json:"agents"`
}

// agentsResponse is the body of GET /api/sessions/{id}/agents.
type agentsResponse struct {
	SessionID string      `json:"session_id"`
	Agents    []agentBody `json:"agents"`
}

// mergeRequest is the body of POST /api/workspaces/{id}/merge.
type mergeRequest struct {
	// SourceWorkspaceID names the workspace whose branch is merged. Its
	// branch is used and, when it is running, pushed to the hub first.
	SourceWorkspaceID string `json:"source_workspace_id"`
	// Branch names a branch in the hub directly, for a source workspace that
	// is gone.
	Branch string `json:"branch"`
	// Strategy is merge or rebase. Empty means merge.
	Strategy string `json:"strategy"`
}

// mergeResponse is the body of POST /api/workspaces/{id}/merge. A merge that
// conflicts is a result, not a failed request: the tree is left conflicted on
// purpose, for the user or the agent to resolve.
type mergeResponse struct {
	WorkspaceID string `json:"workspace_id"`
	Branch      string `json:"branch"`
	Strategy    string `json:"strategy"`
	// Merged reports whether the branch is in. False means conflicts.
	Merged bool `json:"merged"`
	// Commit is the workspace's HEAD after the merge.
	Commit string `json:"commit,omitempty"`
	// Conflicts lists the paths git could not resolve.
	Conflicts []string `json:"conflicts"`
	// Message is git's own output, which is what the user needs to read.
	Message string `json:"message"`
}

// The merge strategies the API accepts.
const (
	strategyMerge  = "merge"
	strategyRebase = "rebase"
)

// RunChild runs a child session to the end and reports how it ended. It is
// the subagent.Runner the spawner drives children with: a child run is an
// ordinary run, with the same tools, events, and entries, bound to the
// context of the parent run that asked for it.
func (s *Server) RunChild(ctx context.Context, sessionID, message, model string) error {
	return s.runs.runChild(ctx, sessionID, message, model)
}

// handleSessionAgents returns the tree of children a session spawned.
func (s *Server) handleSessionAgents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.Store.Session(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	agents, err := s.agentsOf(r.Context(), id, maxAgentDepth)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, agentsResponse{SessionID: id, Agents: agents})
}

// agentsOf reads the children of one session and, below each of them, the
// children they spawned in turn, until levelsBelow levels have been read.
// Zero reads the session's own children and stops.
func (s *Server) agentsOf(ctx context.Context, sessionID string, levelsBelow int) ([]agentBody, error) {
	rows, err := s.deps.Store.Subagents(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	results := map[string]builtin.AgentResult{}
	if s.deps.Subagents != nil {
		reported, err := s.deps.Subagents.List(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		for _, result := range reported {
			results[result.ID] = result
		}
	}
	out := make([]agentBody, 0, len(rows))
	for _, row := range rows {
		body := agentBody{
			ID:              row.ID,
			ParentSessionID: row.ParentSessionID,
			SessionID:       row.ChildSessionID,
			WorkspaceID:     row.ChildWorkspaceID,
			State:           string(row.State),
			CreatedAt:       row.CreatedAt,
			FinishedAt:      row.FinishedAt,
			Agents:          []agentBody{},
		}
		if result, ok := results[row.ID]; ok {
			body.Name, body.Branch = result.Name, result.Branch
			if row.State != store.RunRunning {
				body.Result = &result
			}
		}
		// The workspace row is what a harness that restarted has left of a
		// child's name and branch.
		if body.Name == "" || body.Branch == "" {
			if ws, err := s.deps.Store.Workspace(ctx, row.ChildWorkspaceID); err == nil {
				body.Name, body.Branch = ws.Name, ws.Branch
			}
		}
		if levelsBelow > 0 {
			if body.Agents, err = s.agentsOf(ctx, row.ChildSessionID, levelsBelow-1); err != nil {
				return nil, err
			}
		}
		out = append(out, body)
	}
	return out, nil
}

// handleAbortSubagent stops one child agent and returns what became of it.
func (s *Server) handleAbortSubagent(w http.ResponseWriter, r *http.Request) {
	if s.deps.Subagents == nil {
		s.fail(w, r, conflictf("this harness cannot spawn agents"))
		return
	}
	id := r.PathValue("id")
	row, err := s.deps.Store.Subagent(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.deps.Subagents.Abort(r.Context(), id); err != nil {
		s.fail(w, r, conflictf("%v", err))
		return
	}
	agents, err := s.agentsOf(r.Context(), row.ParentSessionID, childrenOnly)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, agent := range agents {
		if agent.ID == id {
			s.log.Info("subagent aborted", "subagent_id", id, "session_id", row.ChildSessionID)
			writeJSON(w, s.log, http.StatusOK, agent)
			return
		}
	}
	s.fail(w, r, notFoundf("read subagent %s: not found", id))
}

// handleMergeWorkspace brings another workspace's branch into this one,
// through the hub, by merging or rebasing it inside the workspace.
func (s *Server) handleMergeWorkspace(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[mergeRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	strategy := strings.TrimSpace(req.Strategy)
	if strategy == "" {
		strategy = strategyMerge
	}
	if strategy != strategyMerge && strategy != strategyRebase {
		s.fail(w, r, invalidf("strategy must be %q or %q", strategyMerge, strategyRebase))
		return
	}
	target, err := s.deps.Store.Workspace(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	project, err := s.deps.Store.Project(r.Context(), target.ProjectID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	branch, err := s.sourceBranch(r.Context(), target, project, req)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	host, err := s.deps.Workspaces.Inspect(r.Context(), target.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if host.State != workspace.StateRunning {
		s.fail(w, r, conflictf("workspace %s is %s, not running", target.ID, host.State))
		return
	}
	if err := s.deps.Workspaces.Fetch(r.Context(), host, project.Name, branch); err != nil {
		s.fail(w, r, invalidf("fetch %s from the hub: %v", branch, err))
		return
	}
	ex, err := s.deps.Workspaces.Executor(host)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	args := []string{strategy, "FETCH_HEAD"}
	if strategy == strategyMerge {
		args = append(args, "-m", "merge "+branch)
	}
	body := mergeResponse{
		WorkspaceID: target.ID,
		Branch:      branch,
		Strategy:    strategy,
		Conflicts:   []string{},
	}
	out, mergeErr := gitOutput(r.Context(), ex, args...)
	body.Message = strings.TrimSpace(out)
	if mergeErr != nil {
		// The tree stays as git left it, conflict markers and all, so that
		// the user or the agent in this workspace resolves it there.
		body.Message = mergeErr.Error()
		conflicts, err := gitOutput(r.Context(), ex, "diff", "--name-only", "--diff-filter=U")
		if err == nil {
			body.Conflicts = strings.Fields(conflicts)
		}
		s.log.Info("workspace merge conflicted", "workspace_id", target.ID, "branch", branch)
		writeJSON(w, s.log, http.StatusOK, body)
		return
	}
	body.Merged = true
	body.Commit = s.head(r.Context(), host)
	s.log.Info("workspace merged", "workspace_id", target.ID, "branch", branch, "strategy", strategy)
	writeJSON(w, s.log, http.StatusOK, body)
}

// sourceBranch resolves which branch a merge brings in and makes sure the hub
// has it: a running source workspace pushes first, so that the branch in the
// hub is what the user sees in that workspace.
func (s *Server) sourceBranch(ctx context.Context, target store.Workspace, project store.Project, req mergeRequest) (string, error) {
	branch := strings.TrimSpace(req.Branch)
	if req.SourceWorkspaceID == "" {
		if branch == "" {
			return "", invalidf("source_workspace_id or branch is required")
		}
		return branch, nil
	}
	source, err := s.deps.Store.Workspace(ctx, req.SourceWorkspaceID)
	if err != nil {
		return "", err
	}
	if source.ProjectID != target.ProjectID {
		return "", invalidf("workspace %s belongs to another project", source.ID)
	}
	if source.ID == target.ID {
		return "", invalidf("workspace %s cannot merge itself", source.ID)
	}
	if branch == "" {
		branch = source.Branch
	}
	host, err := s.deps.Workspaces.Inspect(ctx, source.ID)
	if err != nil || host.State != workspace.StateRunning {
		// A stopped source cannot push; what it last pushed is what there is.
		return branch, nil
	}
	// The source pushes the branch it is actually on. An explicit branch says
	// which ref to merge, not what to push: pushing the workspace's HEAD to
	// somebody else's branch is not what the caller asked for.
	if err := s.deps.Workspaces.Push(ctx, host, project.Name, source.Branch); err != nil {
		return "", invalidf("push %s from workspace %s: %v", source.Branch, source.ID, err)
	}
	return branch, nil
}

// forkWorkspace creates the workspace a fork-with-workspace runs in: a clone
// from the hub at the commit the fork entry recorded, on a branch of its own.
// The session's workspace pushes first, so that the hub has that commit.
func (s *Server) forkWorkspace(ctx context.Context, sess store.Session, entryID string) (store.Workspace, error) {
	entry, err := s.deps.Store.Entry(ctx, entryID)
	if err != nil {
		return store.Workspace{}, err
	}
	if entry.SessionID != sess.ID {
		return store.Workspace{}, notFoundf("read entry %s of session %s: not found", entryID, sess.ID)
	}
	if entry.Commit == "" {
		return store.Workspace{}, invalidf("entry %s recorded no commit, so there is nothing to clone", entryID)
	}
	source, err := s.deps.Store.Workspace(ctx, sess.WorkspaceID)
	if err != nil {
		return store.Workspace{}, err
	}
	project, err := s.deps.Store.Project(ctx, source.ProjectID)
	if err != nil {
		return store.Workspace{}, err
	}
	sourceHost, err := s.deps.Workspaces.Inspect(ctx, source.ID)
	if err != nil {
		return store.Workspace{}, err
	}
	if sourceHost.State != workspace.StateRunning {
		return store.Workspace{}, conflictf("workspace %s is %s, not running", source.ID, sourceHost.State)
	}
	if err := s.deps.Workspaces.Push(ctx, sourceHost, project.Name, source.Branch); err != nil {
		return store.Workspace{}, err
	}

	id := store.NewID()
	// The branch is not a path under the source's branch: git holds either
	// refs/heads/<branch> or refs/heads/<branch>/<something>, never both.
	branch := source.Branch + "-fork-" + id[:8]
	// A fork is confined as its source is, with none of its ports.
	sandbox := childSandbox(source.Sandbox)
	host, err := s.deps.Workspaces.Create(ctx, workspace.Spec{
		ID:          id,
		Image:       source.Image,
		Project:     project.Name,
		Confinement: confinement(sandbox),
	})
	if err != nil {
		return store.Workspace{}, err
	}
	if err := s.deps.Workspaces.Start(ctx, &host); err != nil {
		s.discard(ctx, host)
		return store.Workspace{}, err
	}
	base, err := s.deps.Workspaces.CloneAt(ctx, host, project.Name, branch, entry.Commit)
	if err != nil {
		s.discard(ctx, host)
		return store.Workspace{}, err
	}
	ws, err := s.deps.Store.CreateWorkspace(ctx, store.Workspace{
		ID:                host.ID,
		ProjectID:         project.ID,
		Name:              source.Name + " fork",
		Branch:            branch,
		BaseCommit:        base,
		Image:             host.Image,
		State:             string(host.State),
		ContainerID:       host.ContainerID,
		ParentWorkspaceID: source.ID,
		Sandbox:           sandbox,
	})
	if err != nil {
		s.discard(ctx, host)
		return store.Workspace{}, err
	}
	s.log.Info("fork workspace created", "workspace_id", ws.ID, "session_id", sess.ID,
		"branch", branch, "commit", entry.Commit)
	return ws, nil
}
