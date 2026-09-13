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
			msg:  provider.AssistantMessage("on it", nil),
			kind: store.KindAssistant,
		},
		{
			name: "assistant with tool calls",
			msg: provider.AssistantMessage("looking", []provider.ToolCall{
				{ID: "call_1", Name: "read", Arguments: json.RawMessage(`{"path":"go.mod","limit":20}`)},
				{ID: "call_2", Name: "bash", Arguments: json.RawMessage(`{"command":"go build ./..."}`)},
			}),
			kind: store.KindAssistant,
		},
		{
			name: "assistant with tool calls only",
			msg: provider.AssistantMessage("", []provider.ToolCall{
				{ID: "call_3", Name: "ls", Arguments: json.RawMessage(`{}`)},
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
	if a.Role != b.Role || a.Content != b.Content || a.ToolCallID != b.ToolCallID || a.IsError != b.IsError {
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
func arguments(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
