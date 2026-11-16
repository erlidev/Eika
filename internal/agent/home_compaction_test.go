package agent_test

import (
	"context"
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

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	zone, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no zone data: %v", err)
	}
	return zone
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
