package provider

import (
	"context"
	"encoding/json"
	"fmt"
)

// Provider streams one model response for a request. The returned channel is
// closed after a Done or Error event; a caller that stops reading early must
// cancel ctx so the provider's goroutine exits.
type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

// Lister is implemented by a Provider whose endpoint can say which models it
// serves. The setup screens use it to offer models to choose from; the models
// of a provider without it are entered by name.
type Lister interface {
	Models(ctx context.Context) ([]ModelInfo, error)
}

// ModelInfo is one model an endpoint reports that it serves.
type ModelInfo struct {
	// ID is the identifier a request names the model by.
	ID string `json:"id"`
	// ContextWindow is the total token budget the endpoint reports, zero
	// when it does not say.
	ContextWindow int `json:"context_window,omitempty"`
	// MaxOutput is the most tokens one response may have, zero when the
	// endpoint does not say.
	MaxOutput int `json:"max_output,omitempty"`
}

// MaxReasoningEffortLen bounds one reasoning_effort value.
const MaxReasoningEffortLen = 32

// ValidReasoningEffort reports whether effort is a value Request accepts for
// ReasoningEffort. Compatible endpoints disagree on the vocabulary — OpenAI
// takes minimal through high, others take none, xhigh, max, or a word of
// their own — so this checks the shape a request field may carry rather than
// a fixed list, and the user configures which words a model offers. The empty
// value leaves the choice to the endpoint.
func ValidReasoningEffort(effort string) bool {
	if len(effort) > MaxReasoningEffortLen {
		return false
	}
	for _, r := range effort {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// EffortNone is the reasoning effort that turns thinking off. It is the word
// the Chat Completions field itself uses for that, and a model's
// ThinkingSwitch says which request field carries it.
const EffortNone = "none"

// ThinkingSwitch is the request field that turns a model's thinking off when
// its reasoning effort is EffortNone. Endpoints disagree on which field that
// is, and one that does not know a field may reject the whole request, so a
// model names the one field its endpoint understands.
type ThinkingSwitch string

// The fields that can turn thinking off.
const (
	// SwitchReasoningEffort sends reasoning_effort "none", the standard
	// field: OpenAI, Gemini, Groq, OpenRouter, Ollama, LM Studio, and recent
	// vLLM and llama.cpp. It is what the empty value means.
	SwitchReasoningEffort ThinkingSwitch = "reasoning_effort"
	// SwitchTemplate sends chat_template_kwargs with enable_thinking and
	// thinking false instead, which a server that renders the model's own
	// chat template (vLLM, SGLang, llama.cpp) hands to it. Qwen, GLM, and
	// Hunyuan templates read the first name, DeepSeek templates the second.
	SwitchTemplate ThinkingSwitch = "chat_template_kwargs"
	// SwitchThinking sends thinking {"type": "disabled"} instead, the object
	// the DeepSeek, Z.ai, Moonshot, and Anthropic compatible APIs take.
	SwitchThinking ThinkingSwitch = "thinking"
)

// ValidThinkingSwitch reports whether s is a ThinkingSwitch Request accepts.
// The empty value means SwitchReasoningEffort.
func ValidThinkingSwitch(s ThinkingSwitch) bool {
	switch s {
	case "", SwitchReasoningEffort, SwitchTemplate, SwitchThinking:
		return true
	}
	return false
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
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// Reasoning is the model's reasoning for this message. It is kept apart
	// from Content because it is not the assistant's answer: a client shows
	// it as its own block, and only a provider configured to preserve
	// thinking replays it on a later request.
	Reasoning  string     `json:"reasoning,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	IsError    bool       `json:"is_error,omitempty"`
	// Metrics records the measured usage of an assistant response for session
	// replay. Providers do not receive this metadata.
	Metrics *MessageMetrics `json:"metrics,omitempty"`
}

// MessageMetrics is the measured state after one assistant response. It is
// stored with the response so a session replay can restore its context meter.
type MessageMetrics struct {
	RunID         string `json:"run_id"`
	Usage         Usage  `json:"usage"`
	Context       Usage  `json:"context"`
	GenerationMS  int64  `json:"generation_ms"`
	ContextWindow int    `json:"context_window"`
	// Timings is how fast the model call that produced this message ran, and
	// is absent when nothing measured it.
	Timings *Timings `json:"timings,omitempty"`
}

// ToolArguments is the exact argument text received from a model. ToolCall
// adds an explicit marker when malformed text must be safely quoted in JSON.
type ToolArguments string

// MarshalJSON keeps valid JSON unchanged and safely quotes malformed text.
func (a ToolArguments) MarshalJSON() ([]byte, error) {
	raw, _ := a.JSON()
	return raw, nil
}

// UnmarshalJSON preserves a valid JSON value exactly. A containing wire type
// must use DecodeToolArguments with its malformed marker to restore malformed
// text.
func (a *ToolArguments) UnmarshalJSON(data []byte) error {
	decoded, err := DecodeToolArguments(data, false)
	if err != nil {
		return err
	}
	*a = decoded
	return nil
}

// JSON returns a safe JSON representation and whether the exact argument text
// was malformed. Empty arguments become an empty object.
func (a ToolArguments) JSON() (json.RawMessage, bool) {
	raw := json.RawMessage(a)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if json.Valid(raw) {
		return append(json.RawMessage(nil), raw...), false
	}
	quoted, _ := json.Marshal(string(a))
	return quoted, true
}

// DecodeToolArguments restores argument text from a safe JSON representation
// and its explicit malformed marker.
func DecodeToolArguments(raw json.RawMessage, malformed bool) (ToolArguments, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if malformed {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", fmt.Errorf("decode malformed tool arguments: %w", err)
		}
		return ToolArguments(text), nil
	}
	if !json.Valid(raw) {
		return "", fmt.Errorf("decode tool arguments: invalid JSON")
	}
	return ToolArguments(append([]byte(nil), raw...)), nil
}

// ToolCall is a request from the model to run one tool.
type ToolCall struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Arguments ToolArguments `json:"arguments"`
}

// MarshalJSON safely represents malformed arguments and marks them so a
// later decode cannot confuse them with a valid top-level JSON string.
func (c ToolCall) MarshalJSON() ([]byte, error) {
	arguments, malformed := c.Arguments.JSON()
	return json.Marshal(struct {
		ID                 string          `json:"id"`
		Name               string          `json:"name"`
		Arguments          json.RawMessage `json:"arguments"`
		ArgumentsMalformed bool            `json:"arguments_malformed,omitempty"`
	}{
		ID:                 c.ID,
		Name:               c.Name,
		Arguments:          arguments,
		ArgumentsMalformed: malformed,
	})
}

// UnmarshalJSON restores exact argument text using arguments_malformed.
func (c *ToolCall) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID                 string          `json:"id"`
		Name               string          `json:"name"`
		Arguments          json.RawMessage `json:"arguments"`
		ArgumentsMalformed bool            `json:"arguments_malformed"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	arguments, err := DecodeToolArguments(wire.Arguments, wire.ArgumentsMalformed)
	if err != nil {
		return err
	}
	*c = ToolCall{ID: wire.ID, Name: wire.Name, Arguments: arguments}
	return nil
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

// AssistantMessageWithReasoning returns an assistant message and the private
// reasoning data a compatible provider needs to continue the conversation.
func AssistantMessageWithReasoning(text, reasoning string, calls []ToolCall) Message {
	return Message{Role: RoleAssistant, Content: text, Reasoning: reasoning, ToolCalls: calls}
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
	// Model is the identifier of the model on the provider's endpoint.
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
	// ReasoningEffort selects how much a reasoning model thinks. Its
	// vocabulary belongs to the endpoint; ValidReasoningEffort bounds the
	// shape. Empty leaves it to the model, and EffortNone turns thinking
	// off through ThinkingSwitch.
	ReasoningEffort string
	// ThinkingSwitch is the field that carries EffortNone; empty means
	// SwitchReasoningEffort. Every other effort goes in reasoning_effort.
	ThinkingSwitch ThinkingSwitch
	// PreserveThinking asks a compatible Chat Completions endpoint to return
	// reasoning data and replays that data with later assistant messages.
	PreserveThinking bool
}

// Usage reports the tokens one response cost.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// TimingSource says who measured a set of Timings. The two are not equally
// exact: an endpoint times its own phases from the inside, while the harness
// can only time the stream that reaches it.
type TimingSource string

// The parties that can measure a response's speed.
const (
	// TimedByEndpoint means the endpoint reported the generation phase itself.
	TimedByEndpoint TimingSource = "endpoint"
	// TimedByHarness means the harness timed the generation phase, from the
	// response's first streamed token to its last.
	TimedByHarness TimingSource = "harness"
)

// Timings is how long one model call spent in each of its two phases and how
// many tokens each phase moved, so that a client can state a rate rather than
// a duration. Reading the prompt and generating the answer run at speeds that
// differ by orders of magnitude, so the two are never summed.
//
// A phase nobody measured is left zero. The prompt phase is reported only by
// an endpoint that measures it: from outside, the time an endpoint spends
// reading a prompt cannot be told apart from the time it spends queueing.
type Timings struct {
	// PromptTokens and PromptMS are the prompt the endpoint read and how long
	// reading it took.
	PromptTokens int     `json:"prompt_tokens,omitempty"`
	PromptMS     float64 `json:"prompt_ms,omitempty"`
	// DecodeTokens and DecodeMS are the tokens generated and the time spent
	// generating them.
	DecodeTokens int     `json:"decode_tokens,omitempty"`
	DecodeMS     float64 `json:"decode_ms,omitempty"`
	// Source says who measured the generation phase.
	Source TimingSource `json:"source,omitempty"`
}

// HasPrompt reports whether the prompt phase yields a rate.
func (t Timings) HasPrompt() bool { return t.PromptTokens > 0 && t.PromptMS > 0 }

// HasDecode reports whether the generation phase yields a rate.
func (t Timings) HasDecode() bool { return t.DecodeTokens > 0 && t.DecodeMS > 0 }

// Known reports whether either phase was measured.
func (t Timings) Known() bool { return t.HasPrompt() || t.HasDecode() }

// EventKind names the shape of a streaming event.
type EventKind string

// The kinds of event a provider streams. Every stream ends with exactly one
// KindDone or KindError.
const (
	// KindTextDelta carries the next piece of assistant text.
	KindTextDelta EventKind = "text_delta"
	// KindReasoningDelta carries model reasoning apart from assistant-visible
	// text. A client may show it whether or not a later request preserves it.
	KindReasoningDelta EventKind = "reasoning_delta"
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
	// ReasoningDelta is the next piece of preserved reasoning.
	ReasoningDelta string
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
	// Timings is what the endpoint said about its own speed, on a KindUsage
	// event. It is the zero value for an endpoint that says nothing.
	Timings Timings
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
