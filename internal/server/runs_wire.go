package server

import (
	"time"

	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// messageRequest is the body of POST /api/sessions/{id}/messages.
type messageRequest struct {
	Text string `json:"text"`
	// Mode is run, steer, or follow_up. Empty means run.
	Mode string `json:"mode"`
	// Model names the model this run uses. Empty uses the default from the
	// settings, or the first model.
	Model string `json:"model"`
}

// runBody is one agent run on the wire.
type runBody struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	State      string    `json:"state"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	Error      string    `json:"error,omitempty"`
}

// runStateResponse is the body of GET /api/sessions/{id}/run: what the
// session is doing and what is waiting to be delivered to it.
type runStateResponse struct {
	SessionID string `json:"session_id"`
	// Active reports whether a run is going right now. A run row with
	// Active false is the last run that finished.
	Active bool     `json:"active"`
	Run    *runBody `json:"run,omitempty"`
	// Queued messages, oldest first, that the run has not delivered yet.
	PendingSteering  []string `json:"pending_steering"`
	PendingFollowUps []string `json:"pending_follow_ups"`
	// Questions the run is waiting on an answer for.
	Questions []builtin.Question `json:"questions"`
	// Elicitations are what MCP servers asked the user during the run's
	// tool calls, which wait on an answer.
	Elicitations []mcp.Elicitation `json:"elicitations"`
}

// asRun renders a stored run on the wire.
func asRun(r store.Run) runBody {
	return runBody{
		ID:         r.ID,
		SessionID:  r.SessionID,
		State:      string(r.State),
		StartedAt:  r.StartedAt,
		FinishedAt: r.FinishedAt,
		Error:      r.Error,
	}
}
