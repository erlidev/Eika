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
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/tool"
)

// homeCompactor is an agent that compacts as the home session does: with a
// board, a clock and a zone. boards holds what each board read returned, and
// calls how many model calls were made before it.
type homeCompactor struct {
	compactor
	boards []string
	calls  []int
}

func newHomeCompactor(t *testing.T, p *providertest.Provider, keep int, board func(n int) (string, error)) *homeCompactor {
	t.Helper()
	zone := mustZone(t, "Europe/Berlin")
	h := &homeCompactor{compactor: compactor{store: newMemoryStore(), events: &recorder{}}}
	h.agent = agent.New(p, nil, agent.Options{
		Store:    h.store,
		Emitter:  h.events,
		Clock:    func() time.Time { return time.Date(2026, 11, 15, 13, 2, 0, 0, time.UTC) },
		Location: zone,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Compaction: agent.CompactionSettings{
			KeepRecentTokens: keep,
			Board: func(_ context.Context, loc *time.Location) (string, time.Time, error) {
				if loc != zone {
					t.Errorf("board zone = %v, want the agent's", loc)
				}
				h.calls = append(h.calls, p.Calls())
				text, err := board(len(h.boards))
				h.boards = append(h.boards, text)
				return text, boardRead, err
			},
		},
	})
	return h
}

// boardRead is when the test's board says it read its rows.
var boardRead = time.Date(2026, 11, 15, 13, 2, 0, 0, time.UTC)

// homeSummary is the summary a home compaction stores with a board read at
// boardRead.
func homeSummary(board, conversation string) string {
	return "[Subagents] as of Sun 2026-11-15 14:02 +01:00 (Europe/Berlin)\n" + board + "\n\n[Conversation]\n" + conversation
}

func TestHomeCompactionRollsTheConversationAndRebuildsTheBoard(t *testing.T) {
	p := providertest.New(providertest.Text("CONVERSATION-1"), providertest.Text("[Conversation]\nCONVERSATION-2"))
	boards := []string{"- plumber (worker): running", "- plumber (worker): idle\n  last report: Found two quotes."}
	c := newHomeCompactor(t, p, 150, func(n int) (string, error) { return boards[n], nil })
	s := sessionOf(history(4, 100))
	if err := c.agent.Compact(context.Background(), s, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	first := homeSummary(boards[0], "CONVERSATION-1")
	if got := s.Conversation.Messages()[0].Content; got != wrapped(first) {
		t.Errorf("summary message = %q, want %q", got, wrapped(first))
	}

	for _, m := range history(4, 100) {
		s.Conversation.Append(m)
	}
	if err := c.agent.Compact(context.Background(), s, ""); err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	// The update reads the previous summary, board and all, where the
	// conversation carries it; the model's label is not repeated.
	update := p.Requests()[1]
	if got := update.Messages[0].Content; got != wrapped(first) {
		t.Errorf("update request begins with %q, want the previous summary", got)
	}
	second := homeSummary(boards[1], "CONVERSATION-2")
	got := c.store.compactions["s1"]
	if len(got) != 2 || got[0].Summary != first || got[1].Summary != second {
		t.Fatalf("stored compactions = %+v, want the home summaries", got)
	}
	for i, c := range got {
		if c.BoardAsOf == nil || !c.BoardAsOf.Equal(boardRead) {
			t.Errorf("compaction %d board_as_of = %v, want the time the board was read", i, deref(c.BoardAsOf))
		}
	}
	// The board is read from rows once the model has written its part.
	if !reflect.DeepEqual(c.calls, []int{1, 2}) {
		t.Errorf("model calls made before each board read = %v, want [1 2]", c.calls)
	}
	var end event.CompactionEnd
	c.events.payloadOf(t, event.TypeCompactionEnd, &end)
	if end.Summary != first {
		t.Errorf("compaction.end summary = %q, want %q", end.Summary, first)
	}
}

func TestHomeCompactionKeepsOnlyTheConversationWhenATurnIsSplit(t *testing.T) {
	cases := []struct {
		name, previous, kept string
	}{
		{"a home summary", homeSummary("- plumber (worker): idle", "OLD"), "OLD"},
		// A summary from before the home session had sections is all
		// conversation.
		{"a summary without sections", "## Goal\nOLD", "## Goal\nOLD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := providertest.New(providertest.Text("PREFIX"))
			c := newHomeCompactor(t, p, 1500, func(int) (string, error) { return "- tiles (worker): running", nil })
			msgs := splitTurn(agent.SummaryMessage(tc.previous))
			s := sessionOf(append(msgs[:1:1], msgs[2:]...))
			if err := c.agent.Compact(context.Background(), s, ""); err != nil {
				t.Fatalf("Compact: %v", err)
			}
			want := homeSummary("- tiles (worker): running", tc.kept+"\n\n---\n\n**Turn Context (split turn):**\n\nPREFIX")
			if got := s.Conversation.Messages()[0].Content; got != wrapped(want) {
				t.Errorf("summary = %q, want %q", got, wrapped(want))
			}
		})
	}
}

func TestHomeCompactionThatFailsLeavesTheSession(t *testing.T) {
	cases := []struct {
		name   string
		step   providertest.Step
		board  error
		reads  int
		errMsg string
	}{
		{"the summary call fails", providertest.Fail(errors.New("the endpoint is down")), nil, 0, "the endpoint is down"},
		{"the board cannot be read", providertest.Text("CONVERSATION"), errors.New("the store is down"), 1, "the store is down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := providertest.New(tc.step)
			c := newHomeCompactor(t, p, 150, func(int) (string, error) { return "", tc.board })
			before := history(4, 100)
			s := sessionOf(before)
			err := c.agent.Compact(context.Background(), s, "")
			if err == nil || !strings.Contains(err.Error(), tc.errMsg) {
				t.Fatalf("Compact error = %v, want %q", err, tc.errMsg)
			}
			if len(c.boards) != tc.reads {
				t.Errorf("board read %d times, want %d", len(c.boards), tc.reads)
			}
			if !reflect.DeepEqual(s.Conversation.Messages(), before) {
				t.Errorf("conversation = %v, want it unchanged", tags(s.Conversation.Messages()))
			}
			if len(c.store.compactions["s1"]) != 0 || len(c.store.order["s1"]) != 0 {
				t.Errorf("a failed compaction stored %v", c.store.order["s1"])
			}
		})
	}
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	zone, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no zone data: %v", err)
	}
	return zone
}

// toolTurn is a turn of a user message, one tool call whose result costs
// about result tokens, and the answer, tagged from n.
func toolTurn(n, result int) []provider.Message {
	id := fmt.Sprintf("c%d", n)
	return []provider.Message{
		provider.UserMessage(filler(fmt.Sprintf("u%d", n), 100)),
		provider.AssistantMessage("", []provider.ToolCall{providertest.Call(id, "read", map[string]string{"path": id})}),
		provider.ToolResultMessage(id, filler(fmt.Sprintf("r%d", n), result), false),
		provider.AssistantMessage(filler(fmt.Sprintf("a%d", n), 100), nil),
	}
}

// shareCompactor is an agent that cuts as home does, keeping share of a
// window of window tokens.
func shareCompactor(p *providertest.Provider, window int, share float64, auto bool) compactor {
	return newCompactor(p, compactorOptions{window: window, compaction: agent.CompactionSettings{
		Auto: auto, KeepRecentShare: share,
	}})
}

func TestHomeCutKeepsWholeTurnsWithinTheShare(t *testing.T) {
	// The share keeps 1,500 tokens. The last turn fits whole; the one before
	// it would not, so the cut is at u4, where Pi's would split that turn at
	// its answer.
	msgs := append(append(history(2, 100), toolTurn(2, 600)...), toolTurn(6, 600)...)
	p := providertest.New(providertest.Text("SUMMARY"))
	c := shareCompactor(p, 10_000, 0.15, false)
	s := sessionOf(msgs)
	if err := c.agent.Compact(context.Background(), s, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if p.Calls() != 1 {
		t.Errorf("made %d summary requests, want one: no turn is split", p.Calls())
	}
	if got, want := tags(s.Conversation.Messages()), []string{"summary", "u6", "", "tool", "a6"}; !reflect.DeepEqual(got, want) {
		t.Errorf("conversation = %v, want %v", got, want)
	}
	if got := c.store.compactions["s1"]; len(got) != 1 || got[0].Kept != 4 {
		t.Errorf("stored compactions = %+v, want four kept", got)
	}
}

func TestHomeCutKeepsSeveralTurnsThatFit(t *testing.T) {
	p := providertest.New(providertest.Text("SUMMARY"))
	// 30% of 10,000 tokens keeps both tool turns, about 1,800 tokens, and
	// not the 2,000 before them.
	c := shareCompactor(p, 10_000, 0.3, false)
	s := sessionOf(append(append(history(2, 1000), toolTurn(2, 600)...), toolTurn(6, 600)...))
	if err := c.agent.Compact(context.Background(), s, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got, want := tags(s.Conversation.Messages()), []string{"summary", "u2", "", "tool", "a2", "u6", "", "tool", "a6"}; !reflect.DeepEqual(got, want) {
		t.Errorf("conversation = %v, want %v", got, want)
	}
}

func TestHomeCutSplitsATurnLargerThanTheShare(t *testing.T) {
	// The share keeps 1,500 tokens and the last turn holds 2,000 of tool
	// output, so Pi's split-turn summary takes over.
	p := providertest.New(providertest.Text("HISTORY"), providertest.Text("PREFIX"))
	c := shareCompactor(p, 10_000, 0.15, false)
	s := sessionOf(splitTurn(provider.UserMessage(filler("u0", 100))))
	if err := c.agent.Compact(context.Background(), s, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if p.Calls() != 2 {
		t.Errorf("made %d summary requests, want the history's and the turn's", p.Calls())
	}
	msgs := s.Conversation.Messages()
	if got, want := tags(msgs), []string{"summary", "", "tool", "a3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("conversation = %v, want %v", got, want)
	}
	if want := wrapped("HISTORY\n\n---\n\n**Turn Context (split turn):**\n\nPREFIX"); msgs[0].Content != want {
		t.Errorf("summary = %q, want %q", msgs[0].Content, want)
	}
}

func TestHomeCutInSmallWindows(t *testing.T) {
	cases := []struct {
		name   string
		window int
		msgs   []provider.Message
		want   []string
	}{
		// 300 tokens keep the last turn of about 210.
		{"the last turn fits", 2000, history(6, 100), []string{"summary", "u4", "a5"}},
		// 600 tokens fit no part of the last turn's 2,000 of tool output;
		// Pi's cut splits it and keeps the answer alone.
		{"no turn fits", 4000, splitTurn(provider.UserMessage(filler("u0", 100))), []string{"summary", "a3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := providertest.New(providertest.Text("HISTORY"), providertest.Text("PREFIX"))
			c := shareCompactor(p, tc.window, agent.DefaultKeepRecentShare, false)
			s := sessionOf(tc.msgs)
			if !c.agent.Compactable(s) {
				t.Fatal("Compactable = false, want a cut")
			}
			if err := c.agent.Compact(context.Background(), s, ""); err != nil {
				t.Fatalf("Compact: %v", err)
			}
			if got := tags(s.Conversation.Messages()); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("conversation = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHomeCutLeavesASingleTurnThatFits(t *testing.T) {
	cases := map[string][]provider.Message{
		"one turn":                   history(2, 100),
		"a summary and one turn":     append([]provider.Message{agent.SummaryMessage("OLD")}, history(2, 100)...),
		"one turn with a tool round": toolTurn(0, 300),
	}
	for name, msgs := range cases {
		t.Run(name, func(t *testing.T) {
			p := providertest.New()
			c := shareCompactor(p, 10_000, agent.DefaultKeepRecentShare, false)
			s := sessionOf(msgs)
			if c.agent.Compactable(s) {
				t.Error("Compactable = true for a conversation whose one turn fits the share")
			}
			if err := c.agent.Compact(context.Background(), s, ""); !errors.Is(err, agent.ErrNothingToCompact) {
				t.Errorf("Compact error = %v, want ErrNothingToCompact", err)
			}
		})
	}
}

func TestHomeCutAfterAnOverflow(t *testing.T) {
	overflow := errors.New(`400 Bad Request: {"error":{"message":"prompt is too long: 213462 tokens > 200000 maximum"}}`)
	p := providertest.New(providertest.Fail(overflow), providertest.Text("SUMMARY"), providertest.Text("answer"))
	// 15% of 20,000 tokens keeps u8 and a9; the message waiting to be sent
	// stays after them.
	c := shareCompactor(p, 20_000, agent.DefaultKeepRecentShare, true)
	s := sessionOf(history(10, 1000))
	if err := c.agent.Run(context.Background(), s, provider.UserMessage("next")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := c.store.compactions["s1"]; len(got) != 1 || got[0].Reason != event.CompactOverflow || got[0].Kept != 2 {
		t.Errorf("stored compactions = %+v", got)
	}
	if got, want := tags(p.Requests()[2].Messages), []string{"summary", "u8", "a9", "next"}; !reflect.DeepEqual(got, want) {
		t.Errorf("request after recovery = %v, want %v", got, want)
	}
}

func TestCompactionSwapsInTheRefreshedPromptAndTools(t *testing.T) {
	var ran []string
	tools := func(name string) *tool.Registry {
		r, err := tool.NewRegistry(callTool{name: name, run: func(context.Context) string {
			ran = append(ran, name)
			return "ok"
		}})
		if err != nil {
			t.Fatalf("NewRegistry: %v", err)
		}
		return r
	}
	oldPrompt, newPrompt := "Old definition.", "New definition."
	p := providertest.New(
		providertest.Text("SUMMARY"),
		providertest.Calls("", providertest.Call("c1", "fresh", nil)),
		providertest.Text("answer"),
	)
	st := newMemoryStore()
	a := agent.New(p, tools("stale"), agent.Options{
		Executor:      computer(t, nil),
		Store:         st,
		ContextWindow: 12_000,
		BasePrompt:    &oldPrompt,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Compaction:    agent.CompactionSettings{Auto: true, KeepRecentShare: 0.25},
		RefreshSnapshot: func(_ context.Context, _ string, record func(agent.Snapshot) agent.Compaction) (agent.Snapshot, error) {
			snap := agent.Snapshot{Preamble: "Time zone\nUTC", Prompt: &newPrompt, Tools: tools("fresh")}
			if err := st.AppendCompaction(context.Background(), "s1", record(snap)); err != nil {
				return agent.Snapshot{}, err
			}
			return snap, nil
		},
	})
	s := sessionOf(history(10, 1000))
	s.Preamble = "Time zone\nUTC"
	if err := a.Run(context.Background(), s, provider.UserMessage("next")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	requests := p.Requests()
	if len(requests) != 3 {
		t.Fatalf("made %d requests, want the summary, the call and the answer", len(requests))
	}
	names := func(req provider.Request) []string {
		var out []string
		for _, def := range req.Tools {
			out = append(out, def.Name)
		}
		return out
	}
	// The summary shares the prefix the run sent; every call after the
	// compaction is built from the new snapshot.
	if summary := requests[0]; !strings.HasPrefix(summary.System, oldPrompt) || !reflect.DeepEqual(names(summary), []string{"stale"}) {
		t.Errorf("summary request system %q, tools %v, want the old definition", summary.System, names(summary))
	}
	for i, req := range requests[1:] {
		if !strings.HasPrefix(req.System, newPrompt) || !reflect.DeepEqual(names(req), []string{"fresh"}) {
			t.Errorf("request %d system %q, tools %v, want the new definition", i+2, req.System, names(req))
		}
	}
	if !reflect.DeepEqual(ran, []string{"fresh"}) {
		t.Errorf("tools run = %v, want the refreshed one", ran)
	}
	if s.Prompt == nil || *s.Prompt != newPrompt || !reflect.DeepEqual(s.Tools, []string{"fresh"}) {
		t.Errorf("session snapshot = %v, %v, want the new definition", deref(s.Prompt), s.Tools)
	}
}

func TestTokensAfterAreMeasuredAgainstTheRefreshedSnapshot(t *testing.T) {
	compactWith := func(t *testing.T, snap agent.Snapshot) (agent.Compaction, event.CompactionEnd) {
		t.Helper()
		oldPrompt := "Old definition."
		st, events := newMemoryStore(), &recorder{}
		a := agent.New(providertest.New(providertest.Text("SUMMARY")), registry(t), agent.Options{
			Store:      st,
			Emitter:    events,
			BasePrompt: &oldPrompt,
			Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			Compaction: agent.CompactionSettings{KeepRecentTokens: 150},
			RefreshSnapshot: func(_ context.Context, _ string, record func(agent.Snapshot) agent.Compaction) (agent.Snapshot, error) {
				if err := st.AppendCompaction(context.Background(), "s1", record(snap)); err != nil {
					return agent.Snapshot{}, err
				}
				return snap, nil
			},
		})
		s := sessionOf(history(4, 100))
		s.Preamble = "Time zone\nUTC"
		if err := a.Compact(context.Background(), s, ""); err != nil {
			t.Fatalf("Compact: %v", err)
		}
		var end event.CompactionEnd
		events.payloadOf(t, event.TypeCompactionEnd, &end)
		stored := st.compactions["s1"]
		if len(stored) != 1 {
			t.Fatalf("stored compactions = %+v", stored)
		}
		return stored[0], end
	}
	same, _ := compactWith(t, agent.Snapshot{Preamble: "Time zone\nUTC"})
	bigPrompt := filler("prompt", 2000)
	bigger, end := compactWith(t, agent.Snapshot{Preamble: filler("preamble", 2000), Prompt: &bigPrompt})
	if grown := bigger.TokensAfter - same.TokensAfter; grown < 3500 {
		t.Errorf("tokens after = %d with the larger snapshot and %d with the same, want about 4,000 more",
			bigger.TokensAfter, same.TokensAfter)
	}
	if end.TokensAfter != bigger.TokensAfter {
		t.Errorf("compaction.end tokens after = %d, want the stored %d", end.TokensAfter, bigger.TokensAfter)
	}
	none, err := tool.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	smaller, _ := compactWith(t, agent.Snapshot{Preamble: "", Tools: none})
	if smaller.TokensAfter >= same.TokensAfter {
		t.Errorf("tokens after = %d with no preamble and no tools, want fewer than the %d with them",
			smaller.TokensAfter, same.TokensAfter)
	}
}
