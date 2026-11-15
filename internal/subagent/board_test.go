package subagent_test

import (
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
)

// boardRows are fixture children of one home session, in creation order as
// the store lists them.
func boardRows(since time.Time) []store.Session {
	at := func(minutes int) time.Time { return since.Add(time.Duration(minutes) * time.Minute) }
	return []store.Session{
		{Name: "old-idle", Agent: "researcher", State: store.SessionIdle, LastActiveAt: at(-90), Result: "Done long ago."},
		{Name: "plumber", Agent: "worker", ProjectID: "p1", State: store.SessionWaiting, LastActiveAt: at(30),
			WaitingReason: "Question ID: question-1\nWhich quote should I accept?\n\nOptions (JSON): [\"A\",\"B\"]\nFree text allowed: false",
			Result:        "Found two quotes.\n\nOutputs:\n- quotes.md"},
		{Name: "flights-lisbon", Agent: "researcher", State: store.SessionIdle, LastActiveAt: at(45),
			Result: "Cheapest is TAP at 212 EUR on 2026-12-03.\nDetails follow."},
		{Name: "tiles", Agent: "worker", ProjectID: "gone", State: store.SessionRunning, LastActiveAt: at(50)},
		{Name: "old-failed", Agent: "worker", State: store.SessionFailed, LastActiveAt: at(-10)},
		{Name: "backup", Agent: "worker", State: store.SessionCancelled, LastActiveAt: at(10)},
		{Name: "long", Agent: "researcher", State: store.SessionIdle, LastActiveAt: at(10), Result: strings.Repeat("é", 310)},
	}
}

func TestBoardGolden(t *testing.T) {
	since := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("no zone data: %v", err)
	}
	got := subagent.Board(boardRows(since), map[string]string{"p1": "kitchen"}, since, berlin)
	want := `- tiles (worker, project gone): running, last active 2026-11-15 13:50
- plumber (worker, project kitchen): waiting, last active 2026-11-15 13:30
  waiting on: Question ID: question-1
    Which quote should I accept?
    Options (JSON): ["A","B"]
    Free text allowed: false
  last report: Found two quotes.
- flights-lisbon (researcher): idle, last active 2026-11-15 13:45
  last report: Cheapest is TAP at 212 EUR on 2026-12-03.
- backup (worker): cancelled, last active 2026-11-15 13:10
- long (researcher): idle, last active 2026-11-15 13:10
  last report: ` + strings.Repeat("é", 300) + `…
2 other subagents have had no activity since the last compaction; subagent_status lists them.`
	if got != want {
		t.Errorf("board =\n%s\nwant\n%s", got, want)
	}
}

func TestBoardBeforeTheFirstCompactionListsEveryChild(t *testing.T) {
	since := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	got := subagent.Board(boardRows(since), nil, time.Time{}, time.UTC)
	if strings.Contains(got, "other subagent") || !strings.Contains(got, "- old-idle (researcher): idle") || !strings.Contains(got, "- old-failed (worker): failed") {
		t.Errorf("board =\n%s\nwant every child and no count", got)
	}
}

func TestBoardCountsWhatItLeavesOut(t *testing.T) {
	since := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		children []store.Session
		want     string
	}{
		{"no children", nil, "None."},
		{"one quiet child", []store.Session{{Name: "a", Agent: "worker", State: store.SessionIdle, LastActiveAt: since}},
			"1 other subagent has had no activity since the last compaction; subagent_status lists it."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subagent.Board(tc.children, nil, since, time.UTC); got != tc.want {
				t.Errorf("board = %q, want %q", got, tc.want)
			}
		})
	}
}
