package agent_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

func echo(word string) provider.ToolCall {
	return providertest.Call("call_0", "bash", map[string]any{"command": "echo " + word})
}

func TestEveryToolCallGetsAnIDOfItsOwnWhateverTheBackendCalledIt(t *testing.T) {
	// Local servers reuse ids: call_0 in every response, none at all. Three
	// parallel calls share one backend id and the fourth call repeats it in the
	// next response.
	noID := echo("three")
	noID.ID = ""
	f := newFixture(t, []providertest.Step{
		providertest.Calls("", echo("one"), echo("two"), noID),
		providertest.Calls("", echo("four")),
		providertest.Text("done"),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var calls []provider.ToolCall
	results := map[string]provider.Message{}
	for _, m := range f.session.Conversation.Messages() {
		calls = append(calls, m.ToolCalls...)
		if m.Role == provider.RoleTool {
			results[m.ToolCallID] = m
		}
	}
	if len(calls) != 4 {
		t.Fatalf("%d tool calls, want 4", len(calls))
	}
	seen := map[string]bool{}
	for i, c := range calls {
		if c.ID == "" || seen[c.ID] {
			t.Errorf("call %d has id %q: every call needs an id of its own", i+1, c.ID)
		}
		seen[c.ID] = true
	}
	wantWire := []string{"call_0", "call_0", "", "call_0"}
	words := []string{"one", "two", "three", "four"}
	for i, c := range calls {
		if c.WireID == nil || *c.WireID != wantWire[i] {
			t.Errorf("call %d: backend id = %v, want %q kept for the wire", i+1, c.WireID, wantWire[i])
		}
		if got := strings.TrimSpace(results[c.ID].Content); !strings.Contains(got, words[i]) {
			t.Errorf("call %d: its result is %q, want the output of echo %s", i+1, got, words[i])
		}
	}

	// The next request sends the calls back as they were stored, the backend's
	// ids included: that is the byte-for-byte replay the prefix depends on.
	requests := f.provider.Requests()
	if !reflect.DeepEqual(requests[1].Messages[1].ToolCalls, calls[:3]) {
		t.Errorf("the request carried calls %+v, want the stored %+v", requests[1].Messages[1].ToolCalls, calls[:3])
	}

	// Events and approvals use Eika's ids.
	var started []string
	for _, e := range f.events.all() {
		if e.Type != event.TypeToolCall {
			continue
		}
		var c event.ToolCall
		if err := e.DecodePayload(&c); err != nil {
			t.Fatal(err)
		}
		started = append(started, c.CallID)
	}
	want := []string{calls[0].ID, calls[1].ID, calls[2].ID, calls[3].ID}
	if !reflect.DeepEqual(started, want) {
		t.Errorf("tool.call events carry ids %v, want %v", started, want)
	}
}

func TestACallStoredBeforeIDsWereMintedIsReplayedUnchanged(t *testing.T) {
	// An old entry has the one id, which is both Eika's and the wire's.
	legacy := provider.ToolCall{ID: "toolu_01", Name: "bash", Arguments: `{"command":"echo old"}`}
	f := newFixture(t, []providertest.Step{
		providertest.Calls("", echo("new")),
		providertest.Text("done"),
	})
	history := []provider.Message{
		provider.UserMessage("before"),
		provider.AssistantMessage("", []provider.ToolCall{legacy}),
		provider.ToolResultMessage("toolu_01", "old", false),
		provider.AssistantMessage("ok", nil),
	}
	for _, m := range history {
		f.session.Conversation.Append(m)
	}
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("again")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	sent := f.provider.Requests()[0].Messages
	if !reflect.DeepEqual(sent[:4], history) {
		t.Errorf("the old messages were sent as %+v, want them unchanged", sent[:4])
	}
	if got := sent[1].ToolCalls[0]; got.WireID != nil || got.Wire() != "toolu_01" {
		t.Errorf("the old call = %+v, want one id that is also the wire's", got)
	}
	last := f.session.Conversation.Messages()
	newCall := last[len(last)-3].ToolCalls[0]
	if newCall.ID == "call_0" || newCall.ID == "toolu_01" || newCall.WireID == nil {
		t.Errorf("the new call = %+v, want a minted id and the backend's kept", newCall)
	}
}
