package agent_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// echoSteps scripts n model responses that each call bash once, with
// different commands, so that no call repeats the one before.
func echoSteps(n int) []providertest.Step {
	steps := make([]providertest.Step, n)
	for i := range steps {
		steps[i] = providertest.Calls("", providertest.Call("c", "bash", map[string]any{"command": "echo " + string(rune('a'+i%26))}))
	}
	return steps
}

// lastTurnEnd returns the stop reason of the last turn.end event.
func lastTurnEnd(t *testing.T, f *fixture) string {
	t.Helper()
	var reason string
	found := false
	for _, e := range f.events.all() {
		if e.Type != event.TypeTurnEnd {
			continue
		}
		var end event.TurnEnd
		if err := e.DecodePayload(&end); err != nil {
			t.Fatal(err)
		}
		reason, found = end.StopReason, true
	}
	if !found {
		t.Fatal("no turn.end event")
	}
	return reason
}

// everyCallIsAnswered fails the test if a tool call in msgs has no result.
func everyCallIsAnswered(t *testing.T, msgs []provider.Message) {
	t.Helper()
	answered := map[string]bool{}
	for _, m := range msgs {
		if m.Role == provider.RoleTool {
			answered[m.ToolCallID] = true
		}
	}
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if !answered[c.ID] {
				t.Errorf("tool call %s has no result: the conversation could not be sent to the model", c.ID)
			}
		}
	}
}

func TestARunStopsCleanlyAtItsModelCallLimitAndCanBeContinued(t *testing.T) {
	steps := append(echoSteps(5), providertest.Text("finished"))
	f := newFixtureWith(t, agent.Options{Limits: agent.Limits{MaxCalls: 3}}, steps)

	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("keep going")); err != nil {
		t.Fatalf("Run: %v, want a run that stopped at a limit to end without an error", err)
	}
	if got := f.provider.Calls(); got != 3 {
		t.Errorf("%d model calls, want the limit of 3", got)
	}
	if got := lastTurnEnd(t, f); got != agent.StopMaxCalls {
		t.Errorf("turn.end stop reason = %q, want %q: the user is told why", got, agent.StopMaxCalls)
	}
	msgs := f.session.Conversation.Messages()
	if last := msgs[len(msgs)-1]; last.Role != provider.RoleTool {
		t.Errorf("the conversation ends with a %s message, want the last tool result kept", last.Role)
	}
	everyCallIsAnswered(t, msgs)
	if stored := f.store.Messages(f.session.ID); len(stored) != len(msgs) {
		t.Errorf("%d messages stored for %d in the conversation: what happened must be kept", len(stored), len(msgs))
	}

	// The next run starts its own count and carries on from the stopped state.
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("continue")); err != nil {
		t.Fatalf("Run to continue: %v", err)
	}
	if got := f.provider.Calls(); got != 6 {
		t.Errorf("%d model calls after continuing, want the two more the script holds", got)
	}
	msgs = f.session.Conversation.Messages()
	if last := msgs[len(msgs)-1]; last.Content != "finished" {
		t.Errorf("the continued run ended with %+v", last)
	}
}

func TestAQueuedFollowUpWaitsForTheRunThatContinues(t *testing.T) {
	var a *agent.Agent
	queue := callTool{name: "queue", run: func(context.Context) string {
		a.FollowUp(provider.UserMessage("and then this"))
		return "queued"
	}}
	f := newFixtureWith(t, agent.Options{Limits: agent.Limits{MaxCalls: 1}}, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "queue", map[string]any{})),
	}, queue)
	a = f.agent
	if err := a.Run(context.Background(), f.session, provider.UserMessage("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	pending := a.PendingFollowUps()
	if len(pending) != 1 || pending[0].Content != "and then this" {
		t.Errorf("pending follow-ups = %+v, want the one the user queued, left for the next run", pending)
	}
	for _, m := range f.session.Conversation.Messages() {
		if m.Content == "and then this" {
			t.Error("the follow-up was delivered to a conversation whose run had stopped")
		}
	}
}

// sameCall scripts a response that calls the tool probe with the given
// argument text.
func sameCall(arguments string) providertest.Step {
	return providertest.Calls("", provider.ToolCall{ID: "c", Name: "probe", Arguments: provider.ToolArguments(arguments)})
}

func TestARepeatedCallGetsAWarningFromTheThirdAndIsNeverStopped(t *testing.T) {
	probe := callTool{name: "probe", run: func(context.Context) string { return "no change" }}
	f := newFixtureWith(t, agent.Options{}, []providertest.Step{
		// Spacing and key order do not make a call different.
		sameCall(`{"a":1,"b":2}`),
		sameCall(`{"b": 2, "a": 1}`),
		sameCall(`{ "a" : 1, "b" : 2 }`),
		// Other arguments are another call.
		sameCall(`{"a":2,"b":2}`),
		providertest.Text("giving up"),
	}, probe)
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("go")); err != nil {
		t.Fatalf("Run: %v, want a repeated call to leave the run alone", err)
	}
	var warned []bool
	for _, m := range f.session.Conversation.Messages() {
		if m.Role == provider.RoleTool {
			warned = append(warned, strings.HasPrefix(m.Content, "no change\n\n[Warning"))
		}
	}
	if !reflect.DeepEqual(warned, []bool{false, false, true, false}) {
		t.Errorf("warned = %v, want only the third identical call warned", warned)
	}
	if got := lastTurnEnd(t, f); got != "stop" {
		t.Errorf("stop reason = %q: a repeated call must never stop the run", got)
	}
}
