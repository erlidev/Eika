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
	sampling   provider.Sampling
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
		Sampling:      o.sampling,
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

func TestCompactReusesTheRunsRequestAsItsPrefix(t *testing.T) {
	var steps []providertest.Step
	for i := range 4 {
		steps = append(steps, providertest.Text(filler(fmt.Sprintf("a%d", 2*i+1), 1000)))
	}
	steps = append(steps, providertest.Text("SUMMARY-1"), providertest.Text(filler("a9", 1000)), providertest.Text("SUMMARY-2"))
	p := providertest.New(steps...)
	c := newCompactor(p, compactorOptions{
		compaction: agent.CompactionSettings{KeepRecentTokens: 1500},
		tools:      registry(t),
	})
	s := agent.NewSession("s1", "w1")
	for i := range 4 {
		if err := c.agent.Run(context.Background(), s, filler(fmt.Sprintf("u%d", 2*i), 1000)); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}

	if err := c.agent.Compact(context.Background(), s, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	requests := p.Requests()
	last, summary := requests[3], requests[4]
	// The summary request is the last run request up to the cut, then the
	// prompt: the endpoint's cache holds all of it but the prompt.
	if summary.System != last.System || !reflect.DeepEqual(summary.Tools, last.Tools) || summary.Model != last.Model {
		t.Errorf("summary request system, tools, or model differ from the run's")
	}
	if !reflect.DeepEqual(summary.Messages[:6], last.Messages[:6]) || len(summary.Messages) != 7 {
		t.Errorf("summary request messages = %v, want the run's first six and the prompt", tags(summary.Messages))
	}
	if lastContent(summary) != agent.SummaryPrompt {
		t.Errorf("summary prompt = %q, want the built-in prompt", lastContent(summary))
	}
	if got, want := tags(s.Conversation.Messages()), []string{"summary", "u6", "a7"}; !reflect.DeepEqual(got, want) {
		t.Errorf("conversation after compaction = %v, want %v", got, want)
	}
	if got := s.Conversation.Messages()[0].Content; got != wrapped("SUMMARY-1") {
		t.Errorf("summary message = %q", got)
	}

	// The next run sends the summary in place of what it covers, and a
	// second compaction updates the summary, focused as it was asked.
	if err := c.agent.Run(context.Background(), s, filler("u8", 1000)); err != nil {
		t.Fatalf("Run after compaction: %v", err)
	}
	if got, want := tags(p.Requests()[5].Messages), []string{"summary", "u6", "a7", "u8"}; !reflect.DeepEqual(got, want) {
		t.Errorf("request after compaction = %v, want %v", got, want)
	}
	if err := c.agent.Compact(context.Background(), s, "the test plan"); err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	update := p.Requests()[6]
	if got, want := tags(update.Messages), []string{"summary", "u6", "a7", "The"}; !reflect.DeepEqual(got[:3], want[:3]) {
		t.Errorf("update request = %v, want %v and the prompt", got, want[:3])
	}
	if want := agent.UpdatePrompt + "\n\nAdditional focus: the test plan"; lastContent(update) != want {
		t.Errorf("update prompt = %q, want the update prompt with the focus", lastContent(update))
	}
	if got, want := tags(s.Conversation.Messages()), []string{"summary", "u8", "a9"}; !reflect.DeepEqual(got, want) {
		t.Errorf("conversation after second compaction = %v, want %v", got, want)
	}

	compactions := c.store.compactions["s1"]
	if len(compactions) != 2 || compactions[0].Kept != 2 || compactions[1].Kept != 2 ||
		compactions[1].Summary != "SUMMARY-2" || compactions[0].Reason != event.CompactManual {
		t.Errorf("stored compactions = %+v", compactions)
	}
	if got := strings.Join(c.store.order["s1"], ","); got != strings.Repeat("message,", 8)+"compaction,message,message,compaction" {
		t.Errorf("store order = %s", got)
	}
	var end event.CompactionEnd
	c.events.payloadOf(t, event.TypeCompactionEnd, &end)
	if end.Summary != "SUMMARY-1" || end.Kept != 2 || end.Error != "" || end.TokensAfter >= end.TokensBefore {
		t.Errorf("compaction.end = %+v", end)
	}
	for _, e := range c.events.all() {
		var delta event.MessageDelta
		if e.Type == event.TypeMessageDelta && e.DecodePayload(&delta) == nil && strings.HasPrefix(delta.Text, "SUMMARY") {
			t.Errorf("the summary streamed to the client as %s", e.Type)
		}
	}
}

func TestCompactUsesCustomPrompts(t *testing.T) {
	p := providertest.New(providertest.Text("SUMMARY"))
	c := newCompactor(p, compactorOptions{compaction: agent.CompactionSettings{
		KeepRecentTokens: 150,
		Prompts:          agent.CompactionPrompts{Summary: "Summarize in one line."},
	}})
	if err := c.agent.Compact(context.Background(), sessionOf(history(4, 100)), ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got := lastContent(p.Requests()[0]); got != "Summarize in one line." {
		t.Errorf("summary prompt = %q, want the custom prompt", got)
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

func TestCompactSummarizesASplitTurnApart(t *testing.T) {
	p := providertest.New(providertest.Text("HISTORY"), providertest.Text("PREFIX"))
	c := newCompactor(p, compactorOptions{compaction: agent.CompactionSettings{KeepRecentTokens: 1500}})
	s := sessionOf(splitTurn(provider.UserMessage(filler("u0", 100))))
	if err := c.agent.Compact(context.Background(), s, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	requests := p.Requests()
	if len(requests) != 2 {
		t.Fatalf("made %d summary requests, want one for the history and one for the turn", len(requests))
	}
	if got, want := tags(requests[0].Messages), []string{"u0", "a1", "The"}; !reflect.DeepEqual(got, want) {
		t.Errorf("history request = %v, want %v", got, want)
	}
	if got := len(requests[1].Messages); got != 6 || lastContent(requests[1]) != agent.TurnPrefixPrompt {
		t.Errorf("turn request sent %d messages ending %q, want the five before the cut and the turn prompt", got, lastContent(requests[1]))
	}
	msgs := s.Conversation.Messages()
	if got, want := tags(msgs), []string{"summary", "", "tool", "a3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("conversation = %v, want %v", got, want)
	}
	if want := wrapped("HISTORY\n\n---\n\n**Turn Context (split turn):**\n\nPREFIX"); msgs[0].Content != want {
		t.Errorf("summary = %q, want %q", msgs[0].Content, want)
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

func TestCompactFallsBackToATranscript(t *testing.T) {
	cases := []struct {
		name   string
		window int
		msgs   []provider.Message
		steps  []providertest.Step
	}{
		{
			name:  "when the model answers with a tool call",
			msgs:  history(6, 1000),
			steps: []providertest.Step{providertest.Calls("", providertest.Call("c1", "read", nil)), providertest.Text("SUMMARY")},
		},
		{
			// The cut keeps u4 on, and the 18,500 tokens of tool output
			// before it leave no room for a summary in a 20,000 token
			// window; the transcript cuts that output short.
			name:   "when the prefix leaves no room for the summary",
			window: 20_000,
			msgs: []provider.Message{
				provider.UserMessage("u0 look"),
				provider.AssistantMessage("", []provider.ToolCall{providertest.Call("c1", "read", nil)}),
				provider.ToolResultMessage("c1", filler("r1", 18_500), false),
				provider.AssistantMessage("a3 done", nil),
				provider.UserMessage(filler("u4", 5200)),
				provider.AssistantMessage("a5 ok", nil),
			},
			steps: []providertest.Step{providertest.Text("SUMMARY")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := providertest.New(tc.steps...)
			c := newCompactor(p, compactorOptions{window: tc.window, compaction: agent.CompactionSettings{KeepRecentTokens: 2000}})
			s := sessionOf(tc.msgs)
			if err := c.agent.Compact(context.Background(), s, ""); err != nil {
				t.Fatalf("Compact: %v", err)
			}
			transcript := p.Requests()[len(tc.steps)-1]
			if transcript.System != agent.TranscriptSystemPrompt || len(transcript.Tools) != 0 || len(transcript.Messages) != 1 {
				t.Fatalf("last request is not a transcript: system %q, %d tools, %d messages",
					transcript.System, len(transcript.Tools), len(transcript.Messages))
			}
			text := transcript.Messages[0].Content
			if !strings.HasPrefix(text, "<conversation>\n[User]: u0") || !strings.HasSuffix(text, agent.SummaryPrompt) {
				t.Errorf("transcript = %.80q ... %.80q", text, text[len(text)-80:])
			}
			if tc.window > 0 && !strings.Contains(text, "more characters truncated") {
				t.Errorf("transcript kept the whole tool result")
			}
			if s.Conversation.Messages()[0].Content != wrapped("SUMMARY") {
				t.Errorf("summary = %q", s.Conversation.Messages()[0].Content)
			}
		})
	}
}

func TestCompactRetriesWithoutThinkingWhenThinkingUsesTheBudget(t *testing.T) {
	thinking := providertest.Stream(
		provider.Event{Kind: provider.KindReasoningDelta, ReasoningDelta: "let me consider every message in turn"},
		provider.Done("length"),
	)
	p := providertest.New(thinking, providertest.Text("SUMMARY"))
	c := newCompactor(p, compactorOptions{
		compaction: agent.CompactionSettings{KeepRecentTokens: 150, FallbackEffort: provider.EffortNone},
		sampling:   provider.Sampling{ReasoningEffort: ptr("high"), MaxOutput: ptr(32_000)},
	})
	s := sessionOf(history(4, 100))
	if err := c.agent.Compact(context.Background(), s, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	requests := p.Requests()
	if len(requests) != 2 {
		t.Fatalf("made %d requests, want 2", len(requests))
	}
	for i, want := range []string{"high", provider.EffortNone} {
		sampling := requests[i].Sampling
		if e := sampling.ReasoningEffort; e == nil || *e != want {
			t.Errorf("request %d effort = %v, want %s", i, e, want)
		}
		// The budget is 80 percent of the reserve, below the run's own
		// max output.
		if m := sampling.MaxOutput; m == nil || *m != agent.DefaultReserveTokens*8/10 {
			t.Errorf("request %d max output = %v, want %d", i, m, agent.DefaultReserveTokens*8/10)
		}
	}
	if s.Conversation.Messages()[0].Content != wrapped("SUMMARY") {
		t.Errorf("summary = %q", s.Conversation.Messages()[0].Content)
	}
}

func TestCompactUsesACutOffSummaryOnTheLastAttempt(t *testing.T) {
	cut := func(text string) providertest.Step {
		return providertest.Stream(provider.TextDelta(text), provider.Done("length"))
	}
	p := providertest.New(cut("## Goal\nship it"), cut("## Go"))
	c := newCompactor(p, compactorOptions{
		compaction: agent.CompactionSettings{KeepRecentTokens: 150, FallbackEffort: "low"},
		sampling:   provider.Sampling{ReasoningEffort: ptr("high")},
	})
	s := sessionOf(history(4, 100))
	if err := c.agent.Compact(context.Background(), s, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got := s.Conversation.Messages()[0].Content; got != wrapped("## Goal\nship it") {
		t.Errorf("summary = %q, want the longer partial one", got)
	}
}

func TestCompactWithNothingToSummarize(t *testing.T) {
	p := providertest.New()
	c := newCompactor(p, compactorOptions{})
	s := sessionOf(history(2, 100))
	if c.agent.Compactable(s) {
		t.Error("Compactable = true for a conversation smaller than what is kept")
	}
	if err := c.agent.Compact(context.Background(), s, ""); !errors.Is(err, agent.ErrNothingToCompact) {
		t.Fatalf("Compact error = %v, want ErrNothingToCompact", err)
	}
	if p.Calls() != 0 || len(c.events.all()) != 0 || s.Conversation.Len() != 2 {
		t.Errorf("a compaction with nothing to do made %d calls and %d events", p.Calls(), len(c.events.all()))
	}
}

func TestCompactThatFailsLeavesTheConversation(t *testing.T) {
	p := providertest.New(providertest.Fail(errors.New("the endpoint is down")))
	c := newCompactor(p, compactorOptions{compaction: agent.CompactionSettings{KeepRecentTokens: 150}})
	s := sessionOf(history(4, 100))
	err := c.agent.Compact(context.Background(), s, "")
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

func TestRunCompactsBeforeACallThatWouldPassTheThreshold(t *testing.T) {
	// A 12,000 token window caps the reserve at 3,000, so the 10,000 tokens
	// of history pass the threshold of 9,000.
	p := providertest.New(providertest.Text("SUMMARY"), providertest.Text("answer"))
	c := newCompactor(p, compactorOptions{window: 12_000, compaction: agent.CompactionSettings{Auto: true, KeepRecentTokens: 2000}})
	s := sessionOf(history(10, 1000))
	if err := c.agent.Run(context.Background(), s, "next"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	requests := p.Requests()
	if len(requests) != 2 {
		t.Fatalf("made %d requests, want a summary and the answer", len(requests))
	}
	if got, want := tags(requests[0].Messages), []string{"u0", "a1", "u2", "a3", "u4", "a5", "u6", "a7", "The"}; !reflect.DeepEqual(got, want) {
		t.Errorf("summary request = %v, want %v", got, want)
	}
	if got, want := tags(requests[1].Messages), []string{"summary", "u8", "a9", "next"}; !reflect.DeepEqual(got, want) {
		t.Errorf("answer request = %v, want %v", got, want)
	}
	// The compaction is stored before the message it did not cover, which
	// the model had not seen yet.
	if got := strings.Join(c.store.order["s1"], ","); got != "compaction,message,message" {
		t.Errorf("store order = %s", got)
	}
	if got := c.store.compactions["s1"]; len(got) != 1 || got[0].Reason != event.CompactThreshold || got[0].Kept != 2 {
		t.Errorf("stored compactions = %+v", got)
	}
	if got, want := tags(s.Conversation.Messages()), []string{"summary", "u8", "a9", "next", "answer"}; !reflect.DeepEqual(got, want) {
		t.Errorf("conversation = %v, want %v", got, want)
	}
	if got := c.events.types(); got[0] != event.TypeTurnStart || got[1] != event.TypeCompactionStart || got[2] != event.TypeCompactionEnd {
		t.Errorf("events = %v, want the compaction inside the turn", got)
	}
}

func TestRunGoesOnWhenAThresholdCompactionFails(t *testing.T) {
	p := providertest.New(providertest.Fail(errors.New("the endpoint is down")), providertest.Text("answer"))
	c := newCompactor(p, compactorOptions{window: 12_000, compaction: agent.CompactionSettings{Auto: true, KeepRecentTokens: 2000}})
	s := sessionOf(history(10, 1000))
	if err := c.agent.Run(context.Background(), s, "next"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(p.Requests()[1].Messages); got != 11 {
		t.Errorf("answer request sent %d messages, want the whole conversation", got)
	}
}

func TestRunRecoversFromAContextOverflow(t *testing.T) {
	overflow := errors.New(`400 Bad Request: {"error":{"message":"prompt is too long: 213462 tokens > 200000 maximum"}}`)
	silent := providertest.Stream(
		provider.Event{Kind: provider.KindUsage, Usage: provider.Usage{InputTokens: 99_500, TotalTokens: 99_500}},
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
		{name: "a second overflow fails the run", auto: true, steps: []providertest.Step{providertest.Fail(overflow), providertest.Text("SUMMARY"), providertest.Fail(overflow)}, wantErr: true, wantCalls: 3},
		{name: "without automatic compaction an overflow fails the run", steps: []providertest.Step{providertest.Fail(overflow)}, wantErr: true, wantCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := providertest.New(tc.steps...)
			c := newCompactor(p, compactorOptions{window: 100_000, compaction: agent.CompactionSettings{Auto: tc.auto, KeepRecentTokens: 2000}})
			s := sessionOf(history(10, 1000))
			err := c.agent.Run(context.Background(), s, "next")
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

func TestRunLowersMaxOutputToTheRoomLeft(t *testing.T) {
	p := providertest.New(providertest.Text("answer"))
	c := newCompactor(p, compactorOptions{window: 5000, sampling: provider.Sampling{MaxOutput: ptr(4000)}})
	s := sessionOf(history(2, 1000))
	if err := c.agent.Run(context.Background(), s, "next"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	m := p.Requests()[0].Sampling.MaxOutput
	if m == nil || *m >= 4000 || *m < 2500 {
		t.Errorf("max output = %v, want the room the window has left", deref(m))
	}
}

func TestCompactedKeepsTheNewestMessagesWithoutTheirMeasurements(t *testing.T) {
	before := history(5, 10)
	before[3].Metrics = &provider.MessageMetrics{RunID: "r1", Context: provider.Usage{TotalTokens: 9000}}
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
		if !c.agent.Steer("steer") {
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
	c = newCompactor(p, compactorOptions{window: 12_000, tools: tools, executor: workspace(t, nil), compaction: agent.CompactionSettings{Auto: true, KeepRecentTokens: 2000}})
	s := sessionOf(history(6, 1000))
	if err := c.agent.Run(context.Background(), s, "next"); err != nil {
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
