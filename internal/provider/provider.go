package provider

import (
	"context"
	"encoding/json"
)

// Provider streams one model response for a request. The returned channel is
// closed after a Done or Error event; a caller that stops reading early must
// cancel ctx so the provider's goroutine exits.
type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

// Role is the author of a conversation message.
type Role string

// The roles a conversation message can carry.
const (
	// RoleUser is a message from the user, including steering and follow-up.
	RoleUser Role = "user"
	// RoleAssistant is a model response: text, tool calls, or both.
	RoleAssistant Role = "assistant"
	// RoleTool is the result of one tool call.
	RoleTool Role = "tool"
)

// Message is one entry of a conversation. Which fields carry meaning depends
// on Role: ToolCalls belongs to an assistant message, ToolCallID and IsError
// to a tool result.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	IsError    bool       `json:"is_error,omitempty"`
}

// ToolCall is a request from the model to run one tool.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// UserMessage returns a user message carrying text.
func UserMessage(text string) Message {
	return Message{Role: RoleUser, Content: text}
}

// AssistantMessage returns an assistant message carrying text, tool calls, or
// both.
func AssistantMessage(text string, calls []ToolCall) Message {
	return Message{Role: RoleAssistant, Content: text, ToolCalls: calls}
}

// ToolResultMessage returns the result of the tool call with the given id.
func ToolResultMessage(callID, content string, isError bool) Message {
	return Message{Role: RoleTool, ToolCallID: callID, Content: content, IsError: isError}
}

// ToolDef describes one tool to the model. Schema is a JSON Schema object
// describing the tool's parameters.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// Request is one call to a model.
type Request struct {
	// Model is the model identifier the provider was configured with.
	Model string
	// System is the system prompt. An empty system prompt is omitted.
	System string
	// Messages is the conversation so far, oldest first.
	Messages []Message
	// Tools are the tools the model may call.
	Tools []ToolDef
	// MaxTokens bounds the generated response. Zero leaves it to the model.
	MaxTokens int
	// Temperature overrides the model default when it is not nil.
	Temperature *float64
	// ReasoningEffort selects how much a reasoning model thinks: one of
	// "minimal", "low", "medium", "high". Empty leaves it to the model.
	ReasoningEffort string
}

// Usage reports the tokens one response cost.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// EventKind names the shape of a streaming event.
type EventKind string

// The kinds of event a provider streams. Every stream ends with exactly one
// KindDone or KindError.
const (
	// KindTextDelta carries the next piece of assistant text.
	KindTextDelta EventKind = "text_delta"
	// KindToolCallDelta carries the next fragment of a tool call's arguments.
	KindToolCallDelta EventKind = "tool_call_delta"
	// KindToolCall carries one fully assembled tool call.
	KindToolCall EventKind = "tool_call"
	// KindUsage carries the token usage of the response.
	KindUsage EventKind = "usage"
	// KindDone reports that the response finished.
	KindDone EventKind = "done"
	// KindError reports that the response failed.
	KindError EventKind = "error"
)

// Event is one item of a response stream. Kind decides which fields are set.
type Event struct {
	Kind EventKind
	// Text is the delta of a KindTextDelta event.
	Text string
	// Index identifies the tool call a KindToolCallDelta event belongs to.
	Index int
	// ToolCallID and ToolName identify the tool call a KindToolCallDelta
	// event belongs to; providers may send them only on the first fragment.
	ToolCallID string
	ToolName   string
	// ArgumentsDelta is the next fragment of a tool call's JSON arguments.
	ArgumentsDelta string
	// ToolCall is the assembled call of a KindToolCall event.
	ToolCall ToolCall
	// Usage is the token usage of a KindUsage event.
	Usage Usage
	// StopReason is why generation ended, on a KindDone event.
	StopReason string
	// Err is the failure of a KindError event.
	Err error
}

// TextDelta returns a KindTextDelta event.
func TextDelta(text string) Event { return Event{Kind: KindTextDelta, Text: text} }

// Done returns a KindDone event with the given stop reason.
func Done(stopReason string) Event { return Event{Kind: KindDone, StopReason: stopReason} }

// Errorf returns a KindError event carrying err.
func Errorf(err error) Event { return Event{Kind: KindError, Err: err} }
