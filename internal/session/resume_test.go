package session_test

import (
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// A run can continue from the end of a path only when no tool call on it is
// unanswered and it does not end at a preamble. A note answers nothing, and a
// result for a call that is not on the path cancels nothing.
func TestPathResumable(t *testing.T) {
	entry := func(m provider.Message) store.Entry {
		t.Helper()
		e, err := session.MessageEntry(m)
		if err != nil {
			t.Fatalf("record %s message: %v", m.Role, err)
		}
		return e
	}
	text := func(kind store.EntryKind, s string) store.Entry {
		t.Helper()
		e, err := session.TextEntry(kind, s)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	compaction, err := session.CompactionEntry(agent.Compaction{Summary: "summary"})
	if err != nil {
		t.Fatal(err)
	}
	path := []store.Entry{
		text(store.KindPreamble, "first"),
		entry(provider.UserMessage("fix the build")),
		entry(provider.AssistantMessage("looking", []provider.ToolCall{
			{ID: "call_1", Name: "read", Arguments: provider.ToolArguments(`{"path":"go.mod"}`)},
			{ID: "call_2", Name: "grep", Arguments: provider.ToolArguments(`{"pattern":"func"}`)},
		})),
		{Kind: store.KindSystem, Payload: []byte(`{"text":"computer restarted"}`)},
		entry(provider.ToolResultMessage("call_1", "module eika", false)),
		entry(provider.ToolResultMessage("call_elsewhere", "nothing", false)),
		entry(provider.ToolResultMessage("call_2", "func main", false)),
		entry(provider.AssistantMessage("here is the fix", nil)),
		text(store.KindPreamble, "replacement"),
		compaction,
	}
	// want[i] is whether a run can continue from path[:i].
	want := []bool{true, false, true, false, false, false, false, true, true, false, true}
	for i, w := range want {
		if got := session.PathResumable(path[:i]); got != w {
			t.Errorf("PathResumable(path[:%d]) = %v, want %v", i, got, w)
		}
	}
}
