package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestADefaultRunStopsAtFortyModelCalls(t *testing.T) {
	f := newFixture(t, echoSteps(50))
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.provider.Calls(); got != agent.DefaultMaxCalls || agent.DefaultMaxCalls != 40 {
		t.Errorf("%d model calls with default limits, want %d", got, agent.DefaultMaxCalls)
	}
}

func TestARetriedModelCallCountsOnce(t *testing.T) {
	rateLimited := &provider.Error{Op: "stream", StatusCode: 429, Retryable: true, Err: errors.New("slow down")}
	f := newFixtureWith(t, agent.Options{Limits: agent.Limits{MaxCalls: 1}}, []providertest.Step{
		providertest.Fail(rateLimited), providertest.Fail(rateLimited), providertest.Text("answer"),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := lastTurnEnd(t, f); got != "stop" {
		t.Errorf("stop reason = %q: a call that was retried is one call, and a limit of 1 allows it", got)
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

func TestARunStopsAtItsWallTimeLimit(t *testing.T) {
	clock := newStepClock(time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC))
	slow := callTool{name: "slow", run: func(context.Context) string {
		clock.Advance(24*time.Hour + time.Minute)
		return "that took a day"
	}}
	f := newFixtureWith(t, agent.Options{Clock: clock.Now}, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "slow", map[string]any{})),
		providertest.Text("never asked"),
	}, slow)
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.provider.Calls(); got != 1 {
		t.Errorf("%d model calls, want the one before the day was up", got)
	}
	if got := lastTurnEnd(t, f); got != agent.StopMaxWall {
		t.Errorf("turn.end stop reason = %q, want %q", got, agent.StopMaxWall)
	}
	everyCallIsAnswered(t, f.session.Conversation.Messages())
}

func TestARunInsideItsWallTimeIsNotStopped(t *testing.T) {
	clock := newStepClock(time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC))
	slow := callTool{name: "slow", run: func(context.Context) string {
		clock.Advance(23 * time.Hour)
		return "that took most of a day"
	}}
	f := newFixtureWith(t, agent.Options{Clock: clock.Now}, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "slow", map[string]any{})),
		providertest.Text("done"),
	}, slow)
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := lastTurnEnd(t, f); got != "stop" {
		t.Errorf("stop reason = %q, want the model's own", got)
	}
}

// sameCall scripts a response that calls the tool probe with the given
// argument text.
func sameCall(arguments string) providertest.Step {
	return providertest.Calls("", provider.ToolCall{ID: "c", Name: "probe", Arguments: provider.ToolArguments(arguments)})
}

func TestARepeatedCallGetsAWarningFromTheThirdAndIsNeverStopped(t *testing.T) {
	probe := callTool{name: "probe", run: func(context.Context) string { return "no change" }}
	other := callTool{name: "other", run: func(context.Context) string { return "different" }}
	f := newFixtureWith(t, agent.Options{}, []providertest.Step{
		// Spacing and key order do not make a call different.
		sameCall(`{"a":1,"b":2}`),
		sameCall(`{"b": 2, "a": 1}`),
		sameCall(`{ "a" : 1, "b" : 2 }`),
		sameCall(`{"a":1,"b":2}`),
		providertest.Calls("", providertest.Call("c", "other", map[string]any{})),
		sameCall(`{"a":1,"b":2}`),
		// Other arguments are another call.
		sameCall(`{"a":2,"b":2}`),
		providertest.Text("giving up"),
	}, probe, other)
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("go")); err != nil {
		t.Fatalf("Run: %v, want a repeated call to leave the run alone", err)
	}
	var results []string
	for _, m := range f.session.Conversation.Messages() {
		if m.Role == provider.RoleTool {
			results = append(results, m.Content)
		}
	}
	warned := func(i int) bool { return strings.Contains(results[i], "Warning: this is call") }
	want := []bool{false, false, true, true, false, false, false}
	if len(results) != len(want) {
		t.Fatalf("%d results, want %d: %q", len(results), len(want), results)
	}
	for i, w := range want {
		if warned(i) != w {
			t.Errorf("result %d = %q, warned = %v, want %v", i+1, results[i], warned(i), w)
		}
	}
	if !strings.Contains(results[2], "call 3 in a row to probe") || !strings.Contains(results[3], "call 4 in a row to probe") {
		t.Errorf("the warnings do not count the calls: %q and %q", results[2], results[3])
	}
	if !strings.HasPrefix(results[2], "no change\n\n[Warning") {
		t.Errorf("result 3 = %q, want the tool's own output first and the warning after it", results[2])
	}
	// The event a client shows carries the same text as the model reads.
	var seen int
	for _, e := range f.events.all() {
		if e.Type != event.TypeToolResult {
			continue
		}
		var r event.ToolResult
		if err := e.DecodePayload(&r); err != nil {
			t.Fatal(err)
		}
		if r.Content != results[seen] {
			t.Errorf("tool.result %d content = %q, want what the model reads, %q", seen+1, r.Content, results[seen])
		}
		seen++
	}
	if got := lastTurnEnd(t, f); got != "stop" {
		t.Errorf("stop reason = %q: a repeated call must never stop the run", got)
	}
}
