package agent_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider/providertest"
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
			Board: func(_ context.Context, loc *time.Location) (string, error) {
				if loc != zone {
					t.Errorf("board zone = %v, want the agent's", loc)
				}
				h.calls = append(h.calls, p.Calls())
				text, err := board(len(h.boards))
				h.boards = append(h.boards, text)
				return text, err
			},
		},
	})
	return h
}

// homeSummary is the summary a home compaction at the test clock stores.
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
	if got := c.store.compactions["s1"]; len(got) != 2 || got[0].Summary != first || got[1].Summary != second {
		t.Errorf("stored compactions = %+v, want the home summaries", got)
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
