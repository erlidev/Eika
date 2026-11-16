package session_test

import (
	"encoding/json"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

func TestEntryStoredBeforeReasoningItemsStillReads(t *testing.T) {
	t.Parallel()
	e := store.Entry{Kind: store.KindAssistant, Payload: json.RawMessage(`{"role":"assistant","content":"hi","reasoning":"think","metrics":{"run_id":"r","usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4},"context":{"input_tokens":3,"output_tokens":1,"total_tokens":4},"generation_ms":1,"context_window":8}}`)}
	got, ok, err := session.Message(e)
	if err != nil || !ok {
		t.Fatalf("Message: %v, ok=%v", err, ok)
	}
	if got.Content != "hi" || got.Reasoning != "think" || got.ReasoningItems != nil {
		t.Errorf("message = %+v, want the text and reasoning it was stored with and no items", got)
	}
}

func TestMessagesFoldCompactions(t *testing.T) {
	t.Parallel()
	entry := func(m provider.Message) store.Entry {
		e, err := session.MessageEntry(m)
		if err != nil {
			t.Fatalf("MessageEntry: %v", err)
		}
		return e
	}
	compaction := func(summary string, kept int) store.Entry {
		e, err := session.CompactionEntry(agent.Compaction{Summary: summary, Kept: kept, TokensBefore: 9000, Reason: "threshold"})
		if err != nil {
			t.Fatalf("CompactionEntry: %v", err)
		}
		return e
	}
	path := []store.Entry{
		entry(provider.UserMessage("one")),
		entry(provider.AssistantMessage("two", nil)),
		{Kind: store.KindSystem, Payload: json.RawMessage(`{"text":"a note"}`)},
		entry(provider.UserMessage("three")),
		compaction("first", 1),
		entry(provider.AssistantMessage("four", nil)),
		entry(provider.UserMessage("five")),
		compaction("second", 2),
		entry(provider.AssistantMessage("six", nil)),
	}
	got, err := session.Messages(path)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	// The second compaction summarizes the first summary and three, and
	// keeps four and five.
	want := []provider.Message{
		agent.SummaryMessage("second"),
		provider.AssistantMessage("four", nil),
		provider.UserMessage("five"),
		provider.AssistantMessage("six", nil),
	}
	if len(got) != len(want) {
		t.Fatalf("Messages = %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content || got[i].Summary != want[i].Summary {
			t.Errorf("message %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if _, ok, err := session.Message(path[4]); ok || err != nil {
		t.Errorf("Message(compaction) = ok %v, err %v; want it outside the conversation", ok, err)
	}
	c, err := session.CompactionOf(path[7])
	if err != nil || c.Summary != "second" || c.Kept != 2 || c.TokensBefore != 9000 {
		t.Errorf("CompactionOf = %+v, %v", c, err)
	}
}
