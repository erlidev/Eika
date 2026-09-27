package server

import (
	"time"

	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// messageRequest is the body of POST /api/sessions/{id}/messages.
type messageRequest struct {
	// Text is required unless Images carries at least one image.
	Text string `json:"text"`
	// Images are pictures attached to the message, for a model with image
	// input. Each is fitted within 1920 by 1080 pixels before it is stored.
	Images []messageImage `json:"images"`
	// Mode is run, steer, or follow_up. Empty means run.
	Mode string `json:"mode"`
	// Model names the model this run uses. Empty uses the default from the
	// settings, or the first model.
	Model string `json:"model"`
}

// messageImage is one image attached to a posted message.
type messageImage struct {
	// Data is the image file, base64 in JSON: PNG, JPEG, GIF, or WebP.
	Data []byte `json:"data"`
}

// queuedMessage is a message waiting in a run's queue.
type queuedMessage struct {
	Text string `json:"text"`
	// Images is how many images the message carries.
	Images int `json:"images"`
}

// asQueued renders queued messages on the wire, oldest first.
func asQueued(messages []provider.Message) []queuedMessage {
	out := make([]queuedMessage, 0, len(messages))
	for _, m := range messages {
		out = append(out, queuedMessage{Text: m.Content, Images: len(m.Images)})
	}
	return out
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
	PendingSteering  []queuedMessage `json:"pending_steering"`
	PendingFollowUps []queuedMessage `json:"pending_follow_ups"`
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
