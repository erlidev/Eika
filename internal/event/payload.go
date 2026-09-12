package event

import "encoding/json"

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

// RunError is the payload of a run.error event: the run failed and stopped.
// Retryable reports whether the failure was one the loop retries, meaning the
// retry budget ran out.
type RunError struct {
	RunID     string `json:"run_id"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
