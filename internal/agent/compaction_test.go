package agent_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/tool"
)

// filler returns text that costs about tokens tokens, starting with tag so a
// test can tell messages apart.
func filler(tag string, tokens int) string {
	return tag + " " + strings.Repeat("x", tokens*4)
}

// history returns n alternating user and assistant messages of about tokens
// tokens each, tagged u0, a1, u2, and so on by their index.
func history(n, tokens int) []provider.Message {
	out := make([]provider.Message, 0, n)
	for i := range n {
		if i%2 == 0 {
			out = append(out, provider.UserMessage(filler(fmt.Sprintf("u%d", i), tokens)))
		} else {
			out = append(out, provider.AssistantMessage(filler(fmt.Sprintf("a%d", i), tokens), nil))
		}
	}
	return out
}

// compactor is an agent that compacts, over the given tools, with a store
// and a recorder for its events.
type compactor struct {
	agent  *agent.Agent
	store  *memoryStore
	events *recorder
}

// compactorOptions are what a compaction test varies.
type compactorOptions struct {
	window     int
	compaction agent.CompactionSettings
	tools      *tool.Registry
	executor   executor.Executor
}

func newCompactor(p provider.Provider, o compactorOptions) compactor {
	c := compactor{store: newMemoryStore(), events: &recorder{}}
	c.agent = agent.New(p, o.tools, agent.Options{
		Executor:      o.executor,
		Store:         c.store,
		Emitter:       c.events,
		ContextWindow: o.window,
		Compaction:    o.compaction,
		RetryBackoff:  time.Millisecond,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return c
}

// sessionOf returns a session whose conversation is msgs.
func sessionOf(msgs []provider.Message) *agent.Session {
	s := agent.NewSession("s1", "w1")
	s.Conversation = agent.NewConversation(msgs...)
	return s
}

// wrapped is the content of the message that carries summary.
func wrapped(summary string) string {
	return agent.SummaryMessage(summary).Content
}

// tags lists the tag each message starts with, or "summary" for the
// message that carries a summary.
func tags(msgs []provider.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		switch {
		case m.Summary:
			out = append(out, "summary")
		case m.Role == provider.RoleTool:
			out = append(out, "tool")
		default:
			tag, _, _ := strings.Cut(m.Content, " ")
			out = append(out, tag)
		}
	}
	return out
}

// deref renders a pointer's value for a failure message.
func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// lastContent is the content of the last message a request sent.
func lastContent(req provider.Request) string {
	return req.Messages[len(req.Messages)-1].Content
}

// A manual compaction replaces what it covers with a summary, a second one
// updates that summary, and each is stored where it happened.
func TestCompactTwiceUpdatesTheSummary(t *testing.T) {
	var steps []providertest.Step
	for i := range 4 {
		steps = append(steps, providertest.Text(filler(fmt.Sprintf("a%d", 2*i+1), 1000)))
	}
	steps = append(steps, providertest.Text("SUMMARY-1"), providertest.Text(filler("a9", 1000)), providertest.Text("SUMMARY-2"))
	p := providertest.New(steps...)
	c := newCompactor(p, compactorOptions{compaction: agent.CompactionSettings{KeepRecentTokens: 1500}})
	s := agent.NewSession("s1", "w1")
	run := func(text string) {
		t.Helper()
		if err := c.agent.Run(context.Background(), s, provider.UserMessage(filler(text, 1000))); err != nil {
			t.Fatalf("Run %s: %v", text, err)
		}
	}
	for i := range 4 {
		run(fmt.Sprintf("u%d", 2*i))
	}
	if err := c.agent.Compact(context.Background(), s, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got, want := tags(s.Conversation.Messages()), []string{"summary", "u6", "a7"}; !reflect.DeepEqual(got, want) {
		t.Errorf("conversation after compaction = %v, want %v", got, want)
	}
	run("u8")
	if err := c.agent.Compact(context.Background(), s, "the test plan"); err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	update := p.Requests()[6]
	if got := tags(update.Messages); got[0] != "summary" || lastContent(update) != agent.UpdatePrompt+"\n\nAdditional focus: the test plan" {
		t.Errorf("update request = %v ending %q, want the old summary first and the focused update prompt", got, lastContent(update))
	}
	if got, want := tags(s.Conversation.Messages()), []string{"summary", "u8", "a9"}; !reflect.DeepEqual(got, want) {
		t.Errorf("conversation after second compaction = %v, want %v", got, want)
	}
	if got := s.Conversation.Messages()[0].Content; got != wrapped("SUMMARY-2") {
		t.Errorf("summary message = %q", got)
	}
	if got := strings.Join(c.store.order["s1"], ","); got != strings.Repeat("message,", 8)+"compaction,message,message,compaction" {
		t.Errorf("store order = %s", got)
	}
}

// splitTurn is a conversation whose last turn is too large to keep whole:
// the cut falls at its second tool call, a2.
func splitTurn(first provider.Message) []provider.Message {
	call := func(id string) provider.Message {
		return provider.AssistantMessage("", []provider.ToolCall{providertest.Call(id, "read", map[string]string{"path": id})})
	}
	return []provider.Message{
		first,
		provider.AssistantMessage(filler("a1", 100), nil),
		provider.UserMessage(filler("u2", 100)),
		call("c1"),
		provider.ToolResultMessage("c1", filler("r1", 1000), false),
		call("c2"),
		provider.ToolResultMessage("c2", filler("r2", 1000), false),
		provider.AssistantMessage(filler("a3", 100), nil),
	}
}

// Pi drops the summary a conversation begins with when the cut splits the
// first turn after it; the summary must survive.
func TestCompactKeepsThePreviousSummaryWhenOnlyATurnIsSplit(t *testing.T) {
	p := providertest.New(providertest.Text("PREFIX"))
	c := newCompactor(p, compactorOptions{compaction: agent.CompactionSettings{KeepRecentTokens: 1500}})
	msgs := splitTurn(agent.SummaryMessage("OLD"))
	s := sessionOf(append(msgs[:1:1], msgs[2:]...))
	if err := c.agent.Compact(context.Background(), s, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if p.Calls() != 1 {
		t.Errorf("made %d summary requests, want only the turn's", p.Calls())
	}
	if want := wrapped("OLD\n\n---\n\n**Turn Context (split turn):**\n\nPREFIX"); s.Conversation.Messages()[0].Content != want {
		t.Errorf("summary = %q, want %q", s.Conversation.Messages()[0].Content, want)
	}
}

func TestIdleCompactionRequiresFilledWindow(t *testing.T) {
	s := sessionOf(history(4, 100))
	p := providertest.New(providertest.Text("summary"))
	large := newCompactor(p, compactorOptions{window: 100000})
	small := newCompactor(p, compactorOptions{window: 100})
	if large.agent.IdleCompactable(context.Background(), s, 60) || !small.agent.IdleCompactable(context.Background(), s, 60) {
		t.Fatal("idle compaction did not respect the context window")
	}
}

func TestCompactThatFailsLeavesTheConversation(t *testing.T) {
	p := providertest.New(providertest.Fail(errors.New("the endpoint is down")))
	c := newCompactor(p, compactorOptions{compaction: agent.CompactionSettings{KeepRecentTokens: 150}})
	s := sessionOf(history(4, 100))
	err := c.agent.CompactIdle(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "the endpoint is down") {
		t.Fatalf("Compact error = %v, want the endpoint's", err)
	}
	if got, want := tags(s.Conversation.Messages()), tags(history(4, 100)); !reflect.DeepEqual(got, want) {
		t.Errorf("conversation = %v, want it unchanged", got)
	}
	if len(c.store.compactions["s1"]) != 0 {
		t.Error("a failed compaction was stored")
	}
	var end event.CompactionEnd
	c.events.payloadOf(t, event.TypeCompactionEnd, &end)
	if !strings.Contains(end.Error, "the endpoint is down") {
		t.Errorf("compaction.end error = %q", end.Error)
	}
}

func TestRunRecoversFromAContextOverflow(t *testing.T) {
	overflow := errors.New(`400 Bad Request: {"error":{"message":"prompt is too long: 213462 tokens > 200000 maximum"}}`)
	silent := providertest.Stream(
		provider.Event{Kind: provider.KindUsage, Usage: provider.Usage{InputTokens: provider.Reported(99_500), TotalTokens: provider.Reported(99_500)}},
		provider.Done("length"),
	)
	cases := []struct {
		name      string
		auto      bool
		steps     []providertest.Step
		wantErr   bool
		wantCalls int
	}{
		{name: "an overflow is compacted and sent again", auto: true, steps: []providertest.Step{providertest.Fail(overflow), providertest.Text("SUMMARY"), providertest.Text("answer")}, wantCalls: 3},
		{name: "a silent overflow is compacted and sent again", auto: true, steps: []providertest.Step{silent, providertest.Text("SUMMARY"), providertest.Text("answer")}, wantCalls: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := providertest.New(tc.steps...)
			c := newCompactor(p, compactorOptions{window: 100_000, compaction: agent.CompactionSettings{Auto: tc.auto, KeepRecentTokens: 2000}})
			s := sessionOf(history(10, 1000))
			err := c.agent.Run(context.Background(), s, provider.UserMessage("next"))
			if (err != nil) != tc.wantErr {
				t.Fatalf("Run error = %v, want an error %t", err, tc.wantErr)
			}
			if p.Calls() != tc.wantCalls {
				t.Errorf("made %d calls, want %d", p.Calls(), tc.wantCalls)
			}
			if !tc.auto {
				return
			}
			if got := c.store.compactions["s1"]; len(got) != 1 || got[0].Reason != event.CompactOverflow {
				t.Errorf("stored compactions = %+v", got)
			}
			if got, want := tags(p.Requests()[2].Messages), []string{"summary", "u8", "a9", "next"}; !reflect.DeepEqual(got, want) {
				t.Errorf("request after recovery = %v, want %v", got, want)
			}
		})
	}
}

func TestCompactedKeepsTheNewestMessagesWithoutTheirMeasurements(t *testing.T) {
	before := history(5, 10)
	before[3].Metrics = &provider.MessageMetrics{RunID: "r1", Context: provider.Usage{TotalTokens: provider.Reported(9000)}}
	got := agent.Compacted(before, agent.Compaction{Summary: "S", Kept: 2})
	if tags(got)[0] != "summary" || !reflect.DeepEqual(tags(got)[1:], []string{"a3", "u4"}) {
		t.Fatalf("compacted = %v, want the summary, a3, and u4", tags(got))
	}
	if got[1].Metrics != nil {
		t.Error("a kept message still carries the measurement of a larger context")
	}
	if before[3].Metrics == nil {
		t.Error("Compacted changed the messages it was given")
	}
	if got := agent.Compacted(before, agent.Compaction{Summary: "S", Kept: 99}); len(got) != 6 {
		t.Errorf("a compaction that keeps more than there is kept %d messages, want all 5 and the summary", len(got)-1)
	}
}

func TestRunCompactsInTheMiddleOfATurn(t *testing.T) {
	// The tool's 3,000 token result takes the conversation past the
	// threshold of 9,000, while a steering message waits to be delivered.
	var c compactor
	big := callTool{name: "big", run: func(context.Context) string {
		if !c.agent.Steer(provider.UserMessage("steer")) {
			t.Error("Steer rejected a message during a tool call")
		}
		return filler("r", 3000)
	}}
	tools, err := tool.NewRegistry(big)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	p := providertest.New(
		providertest.Calls("", providertest.Call("c1", "big", nil)),
		providertest.Text("HISTORY"),
		providertest.Text("PREFIX"),
		providertest.Text("answer"),
	)
	c = newCompactor(p, compactorOptions{window: 12_000, tools: tools, executor: computer(t, nil), compaction: agent.CompactionSettings{Auto: true, KeepRecentTokens: 2000}})
	s := sessionOf(history(6, 1000))
	if err := c.agent.Run(context.Background(), s, provider.UserMessage("next")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	requests := p.Requests()
	if len(requests) != 4 {
		t.Fatalf("made %d requests, want the call, two summaries, and the answer", len(requests))
	}
	// Only tool output would stay within the 2,000 tokens to keep, so the
	// cut is at the call, and the turn's start is summarized apart.
	if got, want := tags(requests[3].Messages), []string{"summary", "", "tool", "steer"}; !reflect.DeepEqual(got, want) {
		t.Errorf("answer request = %v, want %v", got, want)
	}
	if got := strings.Join(c.store.order["s1"], ","); got != "message,message,message,compaction,message,message" {
		t.Errorf("store order = %s", got)
	}
	if got := c.store.compactions["s1"]; len(got) != 1 || got[0].Kept != 2 {
		t.Errorf("stored compactions = %+v", got)
	}
}

func TestConcurrentMessagesDuringPreambleRefresh(t *testing.T) {
	p := providertest.New(providertest.Text("summary"))
	started := make(chan struct{})
	ready := make(chan struct{})
	done := make(chan struct{})
	a := agent.New(p, nil, agent.Options{
		Compaction: agent.CompactionSettings{KeepRecentTokens: 150},
		RefreshSnapshot: func(_ context.Context, _ string, _ func(agent.Snapshot) agent.Compaction) (agent.Snapshot, error) {
			close(started)
			<-ready
			return agent.Snapshot{Preamble: "Time zone\nUTC\n\nLocale\nen"}, nil
		},
	})
	go func() {
		defer close(done)
		<-started
		a.Steer(provider.UserMessage("first"))
		close(ready)
		for range 1000 {
			a.Steer(provider.UserMessage("steer"))
			a.FollowUp(provider.UserMessage("follow up"))
		}
	}()
	s := sessionOf(history(4, 100))
	err := a.Compact(context.Background(), s, "")
	<-done
	if err != nil {
		t.Fatal(err)
	}
}

type cancelledSummary struct {
	provider.Provider
	started chan struct{}
}

func (p cancelledSummary) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCancelledIdleCompactionLeavesStoredContext(t *testing.T) {
	p := cancelledSummary{Provider: providertest.New(), started: make(chan struct{})}
	c := newCompactor(p, compactorOptions{compaction: agent.CompactionSettings{KeepRecentTokens: 150}})
	original := history(4, 100)
	s := sessionOf(original)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.agent.CompactIdle(ctx, s) }()
	<-p.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("compaction: %v", err)
	}
	if !reflect.DeepEqual(s.Conversation.Messages(), original) || len(c.store.compactions["s1"]) != 0 {
		t.Fatal("cancelled summary changed the conversation or stored a compaction")
	}
}
