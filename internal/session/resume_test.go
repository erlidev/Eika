package session_test

import (
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// entryOf records a message the way a run does, so a test path is built from
// the same conversion the agent loop writes through.
func entryOf(t *testing.T, m provider.Message) store.Entry {
	t.Helper()
	e, err := session.MessageEntry(m, "")
	if err != nil {
		t.Fatalf("record %s message: %v", m.Role, err)
	}
	return e
}

func TestPathResumableFollowsUnansweredToolCalls(t *testing.T) {
	t.Parallel()

	ask := provider.AssistantMessage("looking", []provider.ToolCall{
		{ID: "call_1", Name: "read", Arguments: provider.ToolArguments(`{"path":"go.mod"}`)},
		{ID: "call_2", Name: "grep", Arguments: provider.ToolArguments(`{"pattern":"func"}`)},
	})
	path := []store.Entry{
		entryOf(t, provider.UserMessage("fix the build")),
		entryOf(t, ask),
		entryOf(t, provider.ToolResultMessage("call_1", "module eika", false)),
		entryOf(t, provider.ToolResultMessage("call_2", "func main", false)),
		entryOf(t, provider.AssistantMessage("here is the fix", nil)),
	}

	cases := []struct {
		name string
		upTo int
		want bool
	}{
		{name: "the user's message", upTo: 1, want: true},
		{name: "an assistant turn with two calls out", upTo: 2, want: false},
		{name: "one of two results back", upTo: 3, want: false},
		{name: "the result that answers the last call", upTo: 4, want: true},
		{name: "the answer the results led to", upTo: 5, want: true},
		{name: "the empty path of a session with no entries", upTo: 0, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := session.PathResumable(path[:c.upTo]); got != c.want {
				t.Errorf("PathResumable(path[:%d]) = %v, want %v", c.upTo, got, c.want)
			}
		})
	}
}

func TestPathResumableIgnoresEntriesOutsideTheConversation(t *testing.T) {
	t.Parallel()

	// A note and an event are shown in the user interface and never sent to
	// the model, so neither answers a call nor asks for one.
	note := store.Entry{Kind: store.KindSystem, Payload: []byte(`{"text":"workspace restarted"}`)}
	call := entryOf(t, provider.AssistantMessage("", []provider.ToolCall{
		{ID: "call_1", Name: "bash", Arguments: provider.ToolArguments(`{"command":"ls"}`)},
	}))

	if !session.PathResumable([]store.Entry{note}) {
		t.Error("a system note alone is not resumable, want resumable")
	}
	if session.PathResumable([]store.Entry{call, note}) {
		t.Error("a note after an unanswered call is resumable, want not resumable")
	}
	if !session.PathResumable([]store.Entry{call, note, entryOf(t, provider.ToolResultMessage("call_1", "go.mod", false))}) {
		t.Error("the result that answers the call is not resumable, want resumable")
	}
}

func TestPathResumableAcceptsAnUnknownToolResult(t *testing.T) {
	t.Parallel()

	// A result whose call is not on this path answers nothing, and it must
	// not cancel a call that is still out: forking there would produce a
	// conversation the endpoint rejects.
	call := entryOf(t, provider.AssistantMessage("", []provider.ToolCall{
		{ID: "call_1", Name: "bash", Arguments: provider.ToolArguments(`{"command":"ls"}`)},
	}))
	stray := entryOf(t, provider.ToolResultMessage("call_elsewhere", "nothing", false))

	if session.PathResumable([]store.Entry{call, stray}) {
		t.Error("a stray result cleared an unanswered call, want not resumable")
	}
}
