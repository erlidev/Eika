package event

import (
	"encoding/json"
	"time"
)

// Payloads of the events an agent run emits. Each struct is the body of the
// event type named in its doc comment; the wire contract is documented in
// docs/api/events.md and mirrored in web/src/api/events.ts.

// TurnStart is the payload of a turn.start event: one assistant turn has
// begun, triggered by the user message in Message.
type TurnStart struct {
	RunID       string `json:"run_id"`
	SessionID   string `json:"session_id"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Message     string `json:"message"`
}

// MessageDelta is the payload of a message.delta event: the next piece of
// assistant text in the turn.
type MessageDelta struct {
	RunID string `json:"run_id"`
	Text  string `json:"text"`
}

// ToolCall is the payload of a tool.call event: the assistant asked for a tool
// to run with the given arguments.
type ToolCall struct {
	RunID     string          `json:"run_id"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolOutput is the payload of a tool.output event: incremental output from a
// tool that is still running, such as a command's stdout.
type ToolOutput struct {
	RunID  string `json:"run_id"`
	CallID string `json:"call_id"`
	Text   string `json:"text"`
}

// ToolResult is the payload of a tool.result event: a tool call finished.
// Content is what the model sees; Details is optional structured data for the
// user interface.
type ToolResult struct {
	RunID      string          `json:"run_id"`
	CallID     string          `json:"call_id"`
	Name       string          `json:"name"`
	Content    string          `json:"content"`
	IsError    bool            `json:"is_error"`
	Details    json.RawMessage `json:"details,omitempty"`
	DurationMS int64           `json:"duration_ms"`
}

// TurnEnd is the payload of a turn.end event: the assistant produced no
// further tool calls and the turn is complete.
type TurnEnd struct {
	RunID      string `json:"run_id"`
	StopReason string `json:"stop_reason,omitempty"`
	Usage      Usage  `json:"usage"`
}

// Usage reports the tokens a turn cost, summed over every model call it made.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// QuestionAsked is the payload of a question.asked event: a run called the
// ask_user tool and waits until the answer arrives at
// POST /api/questions/{id}/answer or the run is aborted.
type QuestionAsked struct {
	RunID      string `json:"run_id"`
	SessionID  string `json:"session_id"`
	CallID     string `json:"call_id"`
	QuestionID string `json:"question_id"`
	Question   string `json:"question"`
	// Options are the answers the user may pick from, empty when the answer
	// is free text.
	Options []string `json:"options,omitempty"`
	// AllowFreeText reports whether an answer outside Options is accepted.
	AllowFreeText bool `json:"allow_free_text"`
}

// WorkspaceState is the payload of a workspace.state event: a workspace
// reached a new lifecycle state.
type WorkspaceState struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id,omitempty"`
	State       string `json:"state"`
}

// SessionMessage is the payload of a session.message event: one entry of a
// session as it is stored. It is what a replay sends, so that a client which
// connects late sees the conversation it missed in the same stream as the
// live events.
type SessionMessage struct {
	SessionID string `json:"session_id"`
	EntryID   string `json:"entry_id"`
	ParentID  string `json:"parent_id,omitempty"`
	// Kind is the entry kind: user, assistant, tool_call, tool_result,
	// system, or event.
	Kind string `json:"kind"`
	// Commit is the workspace HEAD commit the entry was produced at.
	Commit    string    `json:"commit,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Message is the entry's stored payload: a provider message for the
	// conversation kinds.
	Message json.RawMessage `json:"message"`
}

// BusDropped is the payload of a bus.dropped event: the client read too
// slowly and the harness dropped events for it. It reaches that client only.
type BusDropped struct {
	Dropped int `json:"dropped"`
}

// RunError is the payload of a run.error event: the run failed and stopped.
// Retryable reports whether the failure was one the loop retries, meaning the
// retry budget ran out.
type RunError struct {
	RunID     string `json:"run_id"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
