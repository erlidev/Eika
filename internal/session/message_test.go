package session_test

import (
	"encoding/json"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

func TestMessageEntryRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  provider.Message
		kind store.EntryKind
	}{
		{
			name: "user text",
			msg:  provider.UserMessage("fix the build"),
			kind: store.KindUser,
		},
		{
			name: "assistant text",
			msg: func() provider.Message {
				m := provider.AssistantMessageWithReasoning("on it", "private analysis", nil)
				m.Metrics = &provider.MessageMetrics{
					RunID:         "run-1",
					Usage:         provider.Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
					Context:       provider.Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
					GenerationMS:  500,
					ContextWindow: 8192,
				}
				return m
			}(),
			kind: store.KindAssistant,
		},
		{
			name: "assistant with tool calls",
			msg: provider.AssistantMessage("looking", []provider.ToolCall{
				{ID: "call_1", Name: "read", Arguments: provider.ToolArguments(`{"path":"go.mod","limit":20}`)},
				{ID: "call_2", Name: "bash", Arguments: provider.ToolArguments(`{"command":"go build ./..."}`)},
			}),
			kind: store.KindAssistant,
		},
		{
			name: "assistant with tool calls only",
			msg: provider.AssistantMessage("", []provider.ToolCall{
				{ID: "call_3", Name: "ls", Arguments: provider.ToolArguments(`{}`)},
			}),
			kind: store.KindAssistant,
		},
		{
			name: "assistant with an argument-less tool call",
			msg: provider.AssistantMessage("", []provider.ToolCall{
				{ID: "call_4", Name: "ls"},
			}),
			kind: store.KindAssistant,
		},
		{
			name: "tool result",
			msg:  provider.ToolResultMessage("call_1", "module github.com/erlidev/eika", false),
			kind: store.KindToolResult,
		},
		{
			name: "tool error",
			msg:  provider.ToolResultMessage("call_2", "exit status 1", true),
			kind: store.KindToolResult,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e, err := session.MessageEntry(c.msg, "abc123")
			if err != nil {
				t.Fatalf("MessageEntry: %v", err)
			}
			if e.Kind != c.kind {
				t.Errorf("kind = %q, want %q", e.Kind, c.kind)
			}
			if e.Commit != "abc123" {
				t.Errorf("commit = %q, want abc123", e.Commit)
			}
			got, ok, err := session.Message(e)
			if err != nil || !ok {
				t.Fatalf("Message: %v, ok=%v", err, ok)
			}
			if !sameMessage(got, c.msg) {
				t.Errorf("round trip = %+v, want %+v", got, c.msg)
			}
		})
	}
}

func TestMalformedToolArgumentsRoundTrip(t *testing.T) {
	t.Parallel()
	want := provider.ToolArguments(`{"path":`)
	msg := provider.AssistantMessage("", []provider.ToolCall{{
		ID: "call_1", Name: "read", Arguments: want,
	}})
	e, err := session.MessageEntry(msg, "")
	if err != nil {
		t.Fatalf("MessageEntry with malformed model arguments: %v", err)
	}
	got, ok, err := session.Message(e)
	if err != nil || !ok {
		t.Fatalf("Message: %v, ok=%v", err, ok)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Arguments != want {
		t.Errorf("arguments = %q, want exact malformed text %q", got.ToolCalls[0].Arguments, want)
	}
	var stored struct {
		ToolCalls []struct {
			Arguments          json.RawMessage `json:"arguments"`
			ArgumentsMalformed bool            `json:"arguments_malformed"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal(e.Payload, &stored); err != nil {
		t.Fatalf("decode stored payload: %v", err)
	}
	if len(stored.ToolCalls) != 1 || !stored.ToolCalls[0].ArgumentsMalformed {
		t.Fatalf("stored call = %+v, want the malformed marker", stored.ToolCalls)
	}
}

func TestJSONStringToolArgumentsRoundTripExactly(t *testing.T) {
	t.Parallel()
	want := provider.ToolArguments(`"value"`)
	msg := provider.AssistantMessage("", []provider.ToolCall{{
		ID: "call_1", Name: "accept_string", Arguments: want,
	}})
	e, err := session.MessageEntry(msg, "")
	if err != nil {
		t.Fatalf("MessageEntry with JSON string arguments: %v", err)
	}
	got, ok, err := session.Message(e)
	if err != nil || !ok {
		t.Fatalf("Message: %v, ok=%v", err, ok)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Arguments != want {
		t.Errorf("arguments = %q, want exact JSON string %q", got.ToolCalls[0].Arguments, want)
	}
	var stored struct {
		ToolCalls []struct {
			Arguments          json.RawMessage `json:"arguments"`
			ArgumentsMalformed bool            `json:"arguments_malformed"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal(e.Payload, &stored); err != nil {
		t.Fatalf("decode stored payload: %v", err)
	}
	if len(stored.ToolCalls) != 1 || stored.ToolCalls[0].ArgumentsMalformed || string(stored.ToolCalls[0].Arguments) != `"value"` {
		t.Errorf("stored call = %+v, want an unmarked JSON string", stored.ToolCalls)
	}
}

func TestMessageSkipsNonConversationEntries(t *testing.T) {
	t.Parallel()
	for _, kind := range []store.EntryKind{store.KindSystem, store.KindEvent} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			e := store.Entry{Kind: kind, Payload: json.RawMessage(`{"text":"subagent finished"}`)}
			if _, ok, err := session.Message(e); err != nil || ok {
				t.Fatalf("Message(%s) = ok %v, err %v; want ok false", kind, ok, err)
			}
		})
	}
}

func TestMessageEntryRejectsUnknownRole(t *testing.T) {
	t.Parallel()
	if _, err := session.MessageEntry(provider.Message{Role: "oracle"}, ""); err == nil {
		t.Fatal("MessageEntry accepted an unknown role")
	}
}

// sameMessage compares two messages, treating tool call arguments as the JSON
// they are: the store keeps the document, not the bytes it got, and a call
// that arrived without arguments comes back as an empty object.
func sameMessage(a, b provider.Message) bool {
	if a.Role != b.Role || a.Content != b.Content || a.Reasoning != b.Reasoning || a.ToolCallID != b.ToolCallID || a.IsError != b.IsError {
		return false
	}
	if (a.Metrics == nil) != (b.Metrics == nil) {
		return false
	}
	if a.Metrics != nil && b.Metrics != nil && *a.Metrics != *b.Metrics {
		return false
	}
	if len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	for i, call := range a.ToolCalls {
		other := b.ToolCalls[i]
		if call.ID != other.ID || call.Name != other.Name {
			return false
		}
		var left, right any
		if err := json.Unmarshal(arguments(call.Arguments), &left); err != nil {
			return false
		}
		if err := json.Unmarshal(arguments(other.Arguments), &right); err != nil {
			return false
		}
		if string(mustJSON(left)) != string(mustJSON(right)) {
			return false
		}
	}
	return true
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// arguments spells a call that carried no arguments as the empty object it is
// stored as.
func arguments(raw provider.ToolArguments) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
}
