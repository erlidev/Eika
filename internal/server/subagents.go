package server

import (
	"context"
	"net/http"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
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
	// Branch is the branch the child works on.
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
