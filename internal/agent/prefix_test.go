package agent_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// Every request the loop sends repeats or extends the one before it, with the
// same system prompt and tools, so that the endpoint's cache holds all of it:
// across a tool round, a retry, a steering message and a follow-up. A
// compaction's summary request reuses the old prefix, and the request after it
// starts a new chain from the summary.
func TestEveryRequestExtendsThePreviousOne(t *testing.T) {
	var f *fixture
	poke := callTool{name: "poke", run: func(context.Context) string {
		f.agent.Steer(provider.UserMessage("steer"))
		f.agent.FollowUp(provider.UserMessage(filler("follow", 1000)))
		return "poked"
	}}
	overloaded := &provider.Error{Op: "stream", StatusCode: 503, Retryable: true, Err: errors.New("overloaded")}
	f = newFixtureWith(t, agent.Options{Compaction: agent.CompactionSettings{KeepRecentTokens: 1500}}, []providertest.Step{
		providertest.Calls("looking", providertest.Call("c1", "poke", map[string]any{})),
		providertest.Fail(overloaded),
		providertest.Text("a1 steered"),
		providertest.Text(filler("a2", 1000)),
		providertest.Text("SUMMARY"),
		providertest.Text("a3 after"),
	}, poke)

	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("u0 start")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The steering message comes after the tool result, the follow-up after the
	// turn it waited for: nothing queued is lost or delivered twice.
	if got := strings.Join(roles(f.session.Conversation.Messages()), ","); got != "user,assistant,tool,user,assistant,user,assistant" {
		t.Fatalf("conversation roles = %s", got)
	}
	if err := f.agent.Compact(context.Background(), f.session, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("u3 next")); err != nil {
		t.Fatalf("Run after compaction: %v", err)
	}

	reqs := f.provider.Requests()
	if len(reqs) != 6 {
		t.Fatalf("made %d requests, want 6", len(reqs))
	}
	extends := func(i int, prev, next provider.Request, prefix []provider.Message) {
		t.Helper()
		if next.System != prev.System || !reflect.DeepEqual(next.Tools, prev.Tools) {
			t.Errorf("request %d changed the system prompt or tools", i+1)
		}
		if len(prefix) > len(next.Messages) || !reflect.DeepEqual(next.Messages[:len(prefix)], prefix) {
			t.Errorf("request %d does not begin with the messages of request %d:\n got  %s\n want %s",
				i+1, i, jsonOf(next.Messages), jsonOf(prefix))
		}
	}
	// The run: a tool round, its retry, the steered answer and the follow-up.
	for i := 1; i <= 3; i++ {
		extends(i, reqs[i-1], reqs[i], reqs[i-1].Messages)
	}
	if !reflect.DeepEqual(reqs[2].Messages, reqs[1].Messages) {
		t.Error("the retry did not repeat the failed request")
	}
	// The summary request is the run's request up to the cut, then its prompt.
	summary := reqs[4]
	if summary.System != reqs[3].System || !reflect.DeepEqual(summary.Tools, reqs[3].Tools) {
		t.Error("the summary request changed the system prompt or tools")
	}
	head := summary.Messages[:len(summary.Messages)-1]
	if len(head) == 0 || !reflect.DeepEqual(reqs[3].Messages[:len(head)], head) {
		t.Errorf("the summary request does not reuse the run's prefix: %s", jsonOf(summary.Messages))
	}
	// After the compaction a new chain starts from the summary and what it kept.
	after := reqs[5]
	if !after.Messages[0].Summary || len(after.Messages) != 4 || after.System != reqs[3].System {
		t.Errorf("the request after the compaction = %s, want the summary, the follow-up turn and the new message",
			jsonOf(after.Messages))
	}
}
