package subagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
)

type memory struct {
	mu    sync.Mutex
	rows  map[string]store.Session
	fail  bool
	fails int
}

func newMemory() *memory {
	return &memory{rows: map[string]store.Session{"home": {ID: "home", Owner: "local", Kind: store.SessionHome}, "other": {ID: "other", Owner: "local", Kind: store.SessionHome}}}
}
func (m *memory) Session(_ context.Context, id string) (store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[id]
	if !ok {
		return row, store.ErrNotFound
	}
	return row, nil
}
func (m *memory) CreateSession(_ context.Context, row store.Session) (store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, old := range m.rows {
		if old.ParentSessionID == row.ParentSessionID && old.Name == row.Name {
			return store.Session{}, store.ErrConflict
		}
	}
	row.ID = fmt.Sprint(len(m.rows))
	if row.State == "" {
		row.State = store.SessionIdle
	}
	m.rows[row.ID] = row
	return row, nil
}
func (m *memory) Subagent(_ context.Context, parent, name string) (store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.rows {
		if row.Kind == store.SessionSubagent && row.ParentSessionID == parent && row.Name == name {
			return row, nil
		}
	}
	return store.Session{}, store.ErrNotFound
}
func (m *memory) Subagents(_ context.Context, parent string) ([]store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []store.Session{}
	for _, row := range m.rows {
		if row.ParentSessionID == parent {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func (m *memory) SetSubagentState(_ context.Context, id string, state store.SessionState, reason string) (store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		m.fails++
		return store.Session{}, errors.New("storage unavailable")
	}
	row := m.rows[id]
	row.State = state
	row.WaitingReason = reason
	m.rows[id] = row
	return row, nil
}

func (m *memory) SetSubagentResult(_ context.Context, id, result string) (store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return store.Session{}, errors.New("storage unavailable")
	}
	row := m.rows[id]
	row.Result = result
	row.ReportNoticeID = "notice-" + id
	m.rows[id] = row
	return row, nil
}

// fg is the priority the lifecycle tests start and resume children at.
const fg = inference.Foreground

func child() store.Session {
	return store.Session{Name: "plumber", Agent: "researcher", Brief: "Find a plumber"}
}

// service is a service whose children report when their run completes, as a
// child that calls the report tool does; serviceWith leaves that to the test.
func service(t *testing.T, st *memory, run subagent.Run) *subagent.Service {
	t.Helper()
	var s *subagent.Service
	s = serviceWith(t, st, subagent.Options{Run: func(ctx context.Context, row store.Session, text string, p inference.Priority) error {
		err := run(ctx, row, text, p)
		if err == nil {
			err = s.Report(ctx, row.ID, subagent.Report{Summary: "done"})
		}
		return err
	}})
	return s
}

func serviceWith(t *testing.T, st *memory, o subagent.Options) *subagent.Service {
	t.Helper()
	o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	if o.Final == nil {
		o.Final = func(context.Context, store.Session) (string, error) { return "", nil }
	}
	s := subagent.New(t.Context(), st, o)
	t.Cleanup(s.Close)
	return s
}
func awaitState(t *testing.T, st *memory, want store.SessionState) store.Session {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		row, err := st.Subagent(t.Context(), "home", "plumber")
		if err == nil && row.State == want {
			return row
		}
		select {
		case <-deadline.C:
			t.Fatalf("state = %s, want %s", row.State, want)
		case <-tick.C:
		}
	}
}

func TestDuplicateStartConcurrentResumeAndCancel(t *testing.T) {
	st := newMemory()
	started := make(chan string, 32)
	s := service(t, st, func(ctx context.Context, row store.Session, text string, _ inference.Priority) error {
		started <- row.ID + ":" + text
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancelRequest := context.WithCancel(t.Context())
	first, _, err := s.Start(ctx, "home", child(), fg)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancelRequest()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			input := child()
			input.Brief = "replacement"
			row, started, err := s.Start(t.Context(), "home", input, fg)
			if err != nil || started || row.ID != first.ID || row.Brief != first.Brief {
				t.Errorf("duplicate: %+v %v %v", row, started, err)
			}
			row, accepted, err := s.Message(t.Context(), "home", "plumber", "do not accept", fg, "")
			if err != nil || accepted || row.State != store.SessionRunning {
				t.Errorf("running message: %+v %v %v", row, accepted, err)
			}
		})
	}
	wg.Wait()
	row, err := s.Cancel(t.Context(), "home", "plumber")
	if err != nil || row.State != store.SessionCancelled || row.WaitingReason != "" {
		t.Fatalf("cancel: %+v %v", row, err)
	}
	for range 20 {
		wg.Go(func() {
			if _, _, err := s.Message(t.Context(), "home", "plumber", "follow up", fg, ""); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := <-started; got != first.ID+":follow up" {
		t.Fatal(got)
	}
	select {
	case got := <-started:
		t.Fatalf("duplicate run: %s", got)
	default:
	}
	if _, err := s.Cancel(t.Context(), "other", "plumber"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	s.Close()
	row = awaitState(t, st, store.SessionCancelled)
	if row.ID != first.ID {
		t.Fatal(row)
	}
	if _, _, err := s.Message(t.Context(), "home", "plumber", "closed", fg, ""); err == nil {
		t.Fatal("resumed after close")
	}
}

// A subagent cannot start another subagent or one of Eika's own kind, and
// children are scoped to their parent.
func TestStartScope(t *testing.T) {
	st := newMemory()
	s := service(t, st, func(context.Context, store.Session, string, inference.Priority) error { return nil })
	recursive := child()
	recursive.Agent = "eika"
	if _, _, err := s.Start(t.Context(), "home", recursive, fg); err == nil {
		t.Fatal("accepted a child of Eika's own kind")
	}
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
		t.Fatal(err)
	}
	first := awaitState(t, st, store.SessionIdle)
	if _, _, err := s.Start(t.Context(), first.ID, child(), fg); err == nil {
		t.Fatal("accepted a child of a subagent")
	}
	other, _, err := s.Start(t.Context(), "other", child(), fg)
	if err != nil || other.ID == first.ID {
		t.Fatalf("parent scope: %+v %v", other, err)
	}
}

// A run whose final state write fails leaves running stored with no run
// behind it; the next message must resume it rather than strand it.
func TestStrandedChildResumesAfterFinalWriteFails(t *testing.T) {
	st := newMemory()
	runs := make(chan string, 4)
	finish := make(chan struct{})
	s := service(t, st, func(ctx context.Context, _ store.Session, text string, _ inference.Priority) error {
		runs <- text
		if text == child().Brief {
			<-finish
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
		t.Fatal(err)
	}
	<-runs
	st.mu.Lock()
	st.fail = true
	st.mu.Unlock()
	close(finish)
	deadline := time.Now().Add(3 * time.Second)
	for {
		st.mu.Lock()
		fails := st.fails
		st.mu.Unlock()
		if fails > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("final write was not attempted")
		}
		time.Sleep(time.Millisecond)
	}
	st.mu.Lock()
	st.fail = false
	st.mu.Unlock()
	if row, _ := st.Subagent(t.Context(), "home", "plumber"); row.State != store.SessionRunning {
		t.Fatalf("stored state = %s, want running", row.State)
	}
	if _, _, err := s.Message(t.Context(), "home", "plumber", "again", fg, ""); err != nil {
		t.Fatal(err)
	}
	if got := <-runs; got != "again" {
		t.Fatalf("resumed with %q", got)
	}
	if row, err := s.Cancel(t.Context(), "home", "plumber"); err != nil || row.State != store.SessionCancelled {
		t.Fatalf("cancel resumed child: %+v %v", row, err)
	}
}

func (m *memory) result(name string) string {
	row, _ := m.Subagent(context.Background(), "home", name)
	return row.Result
}

// A report is stored as the child's result and Eika is told of it; a run
// that ends without one is asked once to draft it, and then its final
// message stands in.
func TestEachAssignmentEndsWithAReport(t *testing.T) {
	for _, tc := range []struct {
		name     string
		replies  []string // what each run does: "report" calls the tool
		final    string
		wantRuns int
		want     string
	}{
		{"reports", []string{"report"}, "ignored", 1, "Found two plumbers\n\nOutputs:\n- notes.md\n\nFollow-ups:\n- call both"},
		{"falls back to the final message", []string{"", ""}, "  Two plumbers found.\n", 2, "Two plumbers found."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newMemory()
			reported := make(chan store.Session, 4)
			var mu sync.Mutex
			runs := 0
			var s *subagent.Service
			s = serviceWith(t, st, subagent.Options{
				Run: func(ctx context.Context, row store.Session, _ string, _ inference.Priority) error {
					mu.Lock()
					n := runs
					runs++
					mu.Unlock()
					if tc.replies[n] == "report" {
						return s.Report(ctx, row.ID, subagent.Report{Summary: " Found two plumbers ", Outputs: []string{"notes.md"}, FollowUps: []string{"call both"}})
					}
					return nil
				},
				Final:    func(context.Context, store.Session) (string, error) { return tc.final, nil },
				Reported: func(_ context.Context, row store.Session) { reported <- row },
			})
			row, _, err := s.Start(t.Context(), "home", child(), fg)
			if err != nil {
				t.Fatal(err)
			}
			got := <-reported
			if got.State != store.SessionIdle || got.Result != tc.want {
				t.Errorf("reported %q in state %s, want %q", got.Result, got.State, tc.want)
			}
			mu.Lock()
			if runs != tc.wantRuns {
				t.Errorf("runs = %d, want %d", runs, tc.wantRuns)
			}
			mu.Unlock()
			if err := s.Report(t.Context(), row.ID, subagent.Report{Summary: "late"}); err == nil || st.result("plumber") != tc.want {
				t.Fatal("a report after the assignment ended was accepted")
			}
		})
	}
}

func TestMaxRunningReservesSlotsUnderConcurrentStarts(t *testing.T) {
	st := newMemory()
	running := make(chan string, 64)
	s := serviceWith(t, st, subagent.Options{
		Limit: func(context.Context) int { return 3 },
		Run: func(ctx context.Context, row store.Session, _ string, _ inference.Priority) error {
			running <- row.Name
			<-ctx.Done()
			return ctx.Err()
		},
	})
	var wg sync.WaitGroup
	var started, refused atomic.Int32
	for i := range 30 {
		wg.Go(func() {
			c := child()
			c.Name = fmt.Sprintf("agent-%d", i)
			_, ok, err := s.Start(t.Context(), "home", c, fg)
			switch {
			case err == nil && ok:
				started.Add(1)
			case errors.Is(err, subagent.ErrTooManyRunning):
				refused.Add(1)
			default:
				t.Errorf("start %d: %v %v", i, ok, err)
			}
		})
	}
	wg.Wait()
	if started.Load() != 3 || refused.Load() != 27 {
		t.Fatalf("started %d, refused %d; want 3 and 27", started.Load(), refused.Load())
	}
	// A refused start leaves no child behind.
	if rows, _ := s.Status(t.Context(), "home", ""); len(rows) != 3 {
		t.Fatalf("children = %d, want 3", len(rows))
	}
}

func TestCompletedReportAnnouncesItsID(t *testing.T) {
	st := newMemory()
	bus := event.NewBus(nil)
	sub := bus.Subscribe(event.TopicGlobal)
	defer sub.Close()
	var svc *subagent.Service
	svc = serviceWith(t, st, subagent.Options{Events: bus, Run: func(ctx context.Context, row store.Session, _ string, _ inference.Priority) error {
		return svc.Report(ctx, row.ID, subagent.Report{Summary: "done"})
	}})
	row, _, err := svc.Start(t.Context(), "home", child(), fg)
	if err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case e := <-sub.Events():
			var update event.SessionUpdated
			if err := json.Unmarshal(e.Payload, &update); err != nil {
				t.Fatal(err)
			}
			if update.ReportID != "" {
				if update.SessionID != row.ID || update.ReportID != "notice-"+row.ID {
					t.Fatalf("unexpected report update: %+v", update)
				}
				stored, err := st.Session(t.Context(), row.ID)
				if err != nil || stored.State != store.SessionIdle {
					t.Fatalf("report announced before idle: %+v, %v", stored, err)
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no report update")
		}
	}
}

// A run that fails is told to its owner with the reason, and no report is
// invented for it.
func TestFailedRunIsEndedWithItsReason(t *testing.T) {
	st := newMemory()
	ended := make(chan store.Session, 2)
	s := serviceWith(t, st, subagent.Options{
		Run: func(context.Context, store.Session, string, inference.Priority) error {
			return errors.New("call model: the request needs about 35199 tokens, which does not fit the configured window of 32768")
		},
		Reported: func(context.Context, store.Session) { t.Error("a failed run reported") },
		Ended:    func(_ context.Context, row store.Session) { ended <- row },
	})
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
		t.Fatal(err)
	}
	got := <-ended
	if got.State != store.SessionFailed || !strings.Contains(got.WaitingReason, "35199 tokens") {
		t.Errorf("ended = %s %q, want failed with the reason", got.State, got.WaitingReason)
	}
	if got.Result != "" {
		t.Errorf("result = %q, want none", got.Result)
	}
}
