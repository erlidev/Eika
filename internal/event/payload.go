package event

import (
	"encoding/json"
	"time"

	"github.com/erlidev/eika/internal/provider"
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

// ReasoningDelta is the payload of a reasoning.delta event: the next piece of
// the model's reasoning for the turn. It is streamed apart from the answer so
// a client can show it as its own block or keep it collapsed.
type ReasoningDelta struct {
	RunID string `json:"run_id"`
	Text  string `json:"text"`
}

// MessageReset is the payload of a message.reset event: the current model
// attempt failed and any text deltas from it must be discarded.
type MessageReset struct {
	RunID string `json:"run_id"`
}

// ToolCall is the payload of a tool.call event: the assistant asked for a tool
// to run with the given arguments.
type ToolCall struct {
	RunID     string                 `json:"run_id"`
	CallID    string                 `json:"call_id"`
	Name      string                 `json:"name"`
	Arguments provider.ToolArguments `json:"arguments"`
}

// MarshalJSON marks safely quoted malformed arguments so a decoder cannot
// confuse them with a valid top-level JSON string.
func (c ToolCall) MarshalJSON() ([]byte, error) {
	arguments, malformed := c.Arguments.JSON()
	return json.Marshal(struct {
		RunID              string          `json:"run_id"`
		CallID             string          `json:"call_id"`
		Name               string          `json:"name"`
		Arguments          json.RawMessage `json:"arguments"`
		ArgumentsMalformed bool            `json:"arguments_malformed,omitempty"`
	}{
		RunID:              c.RunID,
		CallID:             c.CallID,
		Name:               c.Name,
		Arguments:          arguments,
		ArgumentsMalformed: malformed,
	})
}

// UnmarshalJSON restores exact arguments using arguments_malformed.
func (c *ToolCall) UnmarshalJSON(data []byte) error {
	var wire struct {
		RunID              string          `json:"run_id"`
		CallID             string          `json:"call_id"`
		Name               string          `json:"name"`
		Arguments          json.RawMessage `json:"arguments"`
		ArgumentsMalformed bool            `json:"arguments_malformed"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	arguments, err := provider.DecodeToolArguments(wire.Arguments, wire.ArgumentsMalformed)
	if err != nil {
		return err
	}
	*c = ToolCall{
		RunID:     wire.RunID,
		CallID:    wire.CallID,
		Name:      wire.Name,
		Arguments: arguments,
	}
	return nil
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

// TurnProgress is the payload of a turn.progress event: the usage a provider
// has reported for the turn so far, with the time spent generating it. Both
// numbers are measured, never estimated, so a client divides one difference
// by the other to get a decode rate that is true of the endpoint. An endpoint
// that reports usage only when a response ends produces one of these per model
// call; one that reports it per chunk produces many.
type TurnProgress struct {
	RunID string `json:"run_id"`
	Usage Usage  `json:"usage"`
	// Context is the most recent model call's own usage: the prompt it sent
	// plus the response it produced. Usage is what the turn cost, which is
	// larger; this is what fills the model's context window.
	Context Usage `json:"context"`
	// GenerationMS is how long the turn has spent inside model responses,
	// measured from each response's first streamed token.
	GenerationMS int64 `json:"generation_ms"`
	// ContextWindow is the configured window of the model that produced this
	// measurement. It keeps the usage bound to the model that reported it.
	ContextWindow int `json:"context_window"`
	// Timings is how fast the most recent model call ran, split into reading
	// the prompt and generating the answer. It is absent until something
	// measures it, and its prompt half is absent unless the endpoint reports
	// its own.
	Timings *provider.Timings `json:"timings,omitempty"`
}

// TurnEnd is the payload of a turn.end event: the assistant produced no
// further tool calls and the turn is complete.
type TurnEnd struct {
	RunID      string `json:"run_id"`
	StopReason string `json:"stop_reason,omitempty"`
	Usage      Usage  `json:"usage"`
	// Context is the last model call's own usage, which is how much of the
	// model's context window the conversation now fills.
	Context Usage `json:"context"`
	// GenerationMS is the turn's total time inside model responses, the
	// denominator of its decode rate.
	GenerationMS int64 `json:"generation_ms"`
	// ContextWindow is the configured window of the model that ran the turn.
	ContextWindow int `json:"context_window"`
	// Timings is how fast the turn's last model call ran.
	Timings *provider.Timings `json:"timings,omitempty"`
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

// MCPServer is the payload of an mcp.server event, on the global topic: an
// MCP server's state changed, or what it serves did. A client refetches the
// server's details.
type MCPServer struct {
	ServerID string `json:"server_id"`
	Name     string `json:"name"`
	// State is disabled, idle, connecting, connected, unauthorized, or
	// error.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// MCPElicitation is the payload of an mcp.elicitation event, on the session
// topic: an MCP server asked the user for input during a tool call, which
// waits until the answer arrives at POST /api/elicitations/{id}/answer or
// the run ends.
type MCPElicitation struct {
	ElicitationID string `json:"elicitation_id"`
	RunID         string `json:"run_id"`
	SessionID     string `json:"session_id"`
	CallID        string `json:"call_id"`
	// Server is the name of the MCP server that asks.
	Server string `json:"server"`
	// Mode is form, for fields to fill in, or url, for a page to visit.
	Mode    string `json:"mode"`
	Message string `json:"message"`
	// RequestedSchema is the form's flat JSON Schema, in form mode.
	RequestedSchema json.RawMessage `json:"requested_schema,omitempty"`
	// URL is the page to visit, in url mode.
	URL string `json:"url,omitempty"`
}

// SessionTitle is the payload of a session.title event, on the global
// topic: an untitled session was named after its first message. Every
// sidebar lists every session, so the event goes to all of them.
type SessionTitle struct {
	SessionID string `json:"session_id"`
	// WorkspaceID is the session's workspace, absent for a chat.
	WorkspaceID string `json:"workspace_id,omitempty"`
	Title       string `json:"title"`
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

// SubagentStarted is the payload of a subagent.started event: a run spawned a
// child agent, which works in a workspace and a session of its own. It is
// emitted on the parent session's topic.
type SubagentStarted struct {
	SubagentID       string `json:"subagent_id"`
	ParentSessionID  string `json:"parent_session_id"`
	ChildSessionID   string `json:"child_session_id"`
	ChildWorkspaceID string `json:"child_workspace_id"`
	// Name is what the parent called the child; it also names its branch.
	Name string `json:"name"`
	// Branch is the branch the child works on in its own workspace.
	Branch string `json:"branch"`
	// BaseCommit is the parent commit the child's workspace was cloned at.
	BaseCommit string `json:"base_commit,omitempty"`
	// Task is the first user message the child was given.
	Task string `json:"task"`
}

// SubagentFinished is the payload of a subagent.finished event: a child agent
// ended, its work is committed and pushed, and the parent has its result. It
// is emitted on the parent session's topic.
type SubagentFinished struct {
	SubagentID       string `json:"subagent_id"`
	ParentSessionID  string `json:"parent_session_id"`
	ChildSessionID   string `json:"child_session_id"`
	ChildWorkspaceID string `json:"child_workspace_id"`
	Name             string `json:"name"`
	Branch           string `json:"branch"`
	// State is how the child ended: done, error, or aborted.
	State string `json:"state"`
	// Commit is the child's head commit after it committed what it left in
	// the tree, empty when it made none.
	Commit string `json:"commit,omitempty"`
	// Summary is the child's final assistant message.
	Summary string `json:"summary,omitempty"`
	// DiffStat is `git diff --stat` between the parent's base commit and the
	// child's head.
	DiffStat string `json:"diff_stat,omitempty"`
	// Error says why a child that did not finish cleanly stopped.
	Error string `json:"error,omitempty"`
}
