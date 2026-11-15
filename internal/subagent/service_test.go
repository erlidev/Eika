package subagent_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
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
	if m.fail && row.State == store.SessionRunning {
		return store.Session{}, errors.New("storage unavailable")
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
	m.rows[id] = row
	return row, nil
}

type events struct {
	mu   sync.Mutex
	rows []event.Event
}

func (e *events) Emit(_ context.Context, row event.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rows = append(e.rows, row)
}

// fg is the priority the lifecycle tests start and resume children at.
const fg = inference.Foreground

func child() store.Session {
	return store.Session{Name: "plumber", Agent: "researcher", Brief: "Find a plumber"}
}

// service is a service whose children report when their run completes, as a
// child that calls the report tool does; serviceWith leaves that to the test.
func service(t *testing.T, st *memory, run subagent.Run, out event.Emitter) *subagent.Service {
	t.Helper()
	var s *subagent.Service
	s = serviceWith(t, st, subagent.Options{Events: out, Run: func(ctx context.Context, row store.Session, text string, p inference.Priority) error {
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
	out := &events{}
	started := make(chan string, 32)
	s := service(t, st, func(ctx context.Context, row store.Session, text string, _ inference.Priority) error {
		started <- row.ID + ":" + text
		<-ctx.Done()
		return ctx.Err()
	}, out)
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
	if _, err := s.Waiting(t.Context(), "home", "plumber", "test wait"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Waiting(t.Context(), "home", "plumber", "test wait"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Waiting(t.Context(), "home", "plumber", "changed reason"); err != nil {
		t.Fatal(err)
	}
	row, _, err := s.Message(t.Context(), "home", "plumber", "not an answer yet", fg, "")
	if err != nil || row.State != store.SessionWaiting || row.WaitingReason != "changed reason" {
		t.Fatalf("waiting: %+v %v", row, err)
	}
	if _, err := s.Waiting(t.Context(), "home", "plumber", ""); err != nil {
		t.Fatal(err)
	}
	row, err = s.Cancel(t.Context(), "home", "plumber")
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
	out.mu.Lock()
	defer out.mu.Unlock()
	if len(out.rows) != 7 {
		t.Fatalf("events = %d, want 7", len(out.rows))
	}
	for _, e := range out.rows {
		var payload event.SessionUpdated
		if e.Type != event.TypeSessionUpdated || e.Topic != event.TopicGlobal || e.DecodePayload(&payload) != nil || payload.SessionID != first.ID {
			t.Fatalf("event: %+v", e)
		}
	}
}

func TestScriptedProviderResumesSameContext(t *testing.T) {
	st := newMemory()
	p := providertest.New(providertest.Text("first result"), providertest.Text("second result"))
	sessions := map[string]*agent.Session{}
	s := service(t, st, func(ctx context.Context, row store.Session, text string, _ inference.Priority) error {
		current := sessions[row.ID]
		if current == nil {
			current = &agent.Session{ID: row.ID, Conversation: agent.NewConversation()}
			sessions[row.ID] = current
		}
		loop := agent.New(p, nil, agent.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		return loop.Run(ctx, current, provider.UserMessage(text))
	}, nil)
	row, _, err := s.Start(t.Context(), "home", child(), fg)
	if err != nil {
		t.Fatal(err)
	}
	awaitState(t, st, store.SessionIdle)
	next, _, err := s.Message(t.Context(), "home", "plumber", "follow up", fg, "")
	if err != nil || next.ID != row.ID {
		t.Fatalf("resume: %+v %v", next, err)
	}
	awaitState(t, st, store.SessionIdle)
	s.Close()
	requests := p.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests = %d", len(requests))
	}
	if !reflect.DeepEqual(requests[0].Messages, requests[1].Messages[:len(requests[0].Messages)]) {
		t.Fatal("resume lost the original context")
	}
	found := false
	for _, m := range requests[1].Messages {
		if m.Content == "first result" {
			found = true
		}
	}
	if !found {
		t.Fatal("resume lost prior response")
	}
}

func TestFailedChildResumesAndStateWriteFailureIsReturned(t *testing.T) {
	st := newMemory()
	s := service(t, st, func(context.Context, store.Session, string, inference.Priority) error {
		return errors.New("provider unavailable")
	}, nil)
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
		t.Fatal(err)
	}
	awaitState(t, st, store.SessionFailed)
	st.mu.Lock()
	st.fail = true
	st.mu.Unlock()
	if _, _, err := s.Message(t.Context(), "home", "plumber", "retry", fg, ""); err == nil {
		t.Fatal("ignored state write error")
	}
	st.mu.Lock()
	st.fail = false
	st.mu.Unlock()
	if _, _, err := s.Message(t.Context(), "home", "plumber", "retry", fg, ""); err != nil {
		t.Fatal(err)
	}
	awaitState(t, st, store.SessionFailed)
}

func TestStartValidationAndScope(t *testing.T) {
	st := newMemory()
	s := service(t, st, func(context.Context, store.Session, string, inference.Priority) error { return nil }, nil)
	for _, tc := range []struct {
		name   string
		change func(*store.Session)
	}{
		{"empty name", func(c *store.Session) { c.Name = " " }},
		{"invalid UTF8", func(c *store.Session) { c.Brief = string([]byte{255}) }},
		{"NUL", func(c *store.Session) { c.Name = "a\x00b" }},
		{"recursive agent", func(c *store.Session) { c.Agent = "eika" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := child()
			tc.change(&c)
			if _, _, err := s.Start(t.Context(), "home", c, fg); err == nil {
				t.Fatal("accepted invalid child")
			}
		})
	}
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
		t.Fatal(err)
	}
	first := awaitState(t, st, store.SessionIdle)
	if _, _, err := s.Start(t.Context(), first.ID, child(), fg); err == nil {
		t.Fatal("accepted recursive parent")
	}
	other, _, err := s.Start(t.Context(), "other", child(), fg)
	if err != nil || other.ID == first.ID {
		t.Fatalf("parent scope: %+v %v", other, err)
	}
	rows, err := s.Status(t.Context(), "home", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("board: %+v %v", rows, err)
	}
}

func TestCancelReturnsFinalStateWriteError(t *testing.T) {
	st := newMemory()
	started := make(chan struct{}, 1)
	s := service(t, st, func(ctx context.Context, _ store.Session, _ string, _ inference.Priority) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
		t.Fatal(err)
	}
	<-started
	st.mu.Lock()
	st.fail = true
	st.mu.Unlock()
	if _, err := s.Cancel(t.Context(), "home", "plumber"); err == nil {
		t.Fatal("ignored final state write error")
	}
	// The stored running state has no active run, so a later message resumes it.
	st.mu.Lock()
	st.fail = false
	st.mu.Unlock()
	row, _, err := s.Message(t.Context(), "home", "plumber", "retry", fg, "")
	if err != nil || row.State != store.SessionRunning {
		t.Fatalf("stranded child did not resume: %+v %v", row, err)
	}
}

func TestStrandedChildResumesAndCancelsAfterFinalWriteFails(t *testing.T) {
	for _, tc := range []string{"message", "cancel"} {
		t.Run(tc, func(t *testing.T) {
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
			}, nil)
			if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
				t.Fatal(err)
			}
			<-runs
			st.mu.Lock()
			st.fail = true
			st.mu.Unlock()
			close(finish)
			// The run ends and its final write fails: running stays stored.
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
			if tc == "cancel" {
				row, err := s.Cancel(t.Context(), "home", "plumber")
				if err != nil || row.State != store.SessionCancelled {
					t.Fatalf("cancel stranded child: %+v %v", row, err)
				}
				awaitState(t, st, store.SessionCancelled)
				return
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
		})
	}
}

func TestStartRetriesAfterInitialWriteFails(t *testing.T) {
	st := newMemory()
	st.fail = true
	started := make(chan string, 1)
	s := service(t, st, func(_ context.Context, _ store.Session, text string, _ inference.Priority) error {
		started <- text
		return nil
	}, nil)
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err == nil {
		t.Fatal("accepted failed initial write")
	}
	if _, err := st.Subagent(t.Context(), "home", "plumber"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed write left a child: %v", err)
	}
	st.mu.Lock()
	st.fail = false
	st.mu.Unlock()
	row, _, err := s.Start(t.Context(), "home", child(), fg)
	if err != nil || row.State != store.SessionRunning {
		t.Fatalf("retry: %+v %v", row, err)
	}
	if got := <-started; got != child().Brief {
		t.Fatalf("brief = %q", got)
	}
	awaitState(t, st, store.SessionIdle)
}

type gatedScript struct {
	provider *providertest.Provider
	started  chan struct{}
	release  chan struct{}
}

func (p *gatedScript) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	stream, err := p.provider.Stream(ctx, req)
	p.started <- struct{}{}
	select {
	case <-p.release:
		return stream, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type stateEvent struct {
	event  event.Event
	state  store.SessionState
	reason string
}

func TestScriptedProviderLifecycleAndGlobalEvents(t *testing.T) {
	st := newMemory()
	p := &gatedScript{
		provider: providertest.New(providertest.Text("done"), providertest.Fail(errors.New("model failed")), providertest.Text("cancelled")),
		started:  make(chan struct{}, 3), release: make(chan struct{}),
	}
	var mu sync.Mutex
	var updates []stateEvent
	emitter := event.EmitterFunc(func(ctx context.Context, e event.Event) {
		var payload event.SessionUpdated
		if err := e.DecodePayload(&payload); err != nil {
			t.Error(err)
			return
		}
		row, err := st.Session(ctx, payload.SessionID)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		updates = append(updates, stateEvent{event: e, state: row.State, reason: row.WaitingReason})
		mu.Unlock()
	})
	conversation := agent.NewConversation()
	s := service(t, st, func(ctx context.Context, row store.Session, text string, _ inference.Priority) error {
		loop := agent.New(p, nil, agent.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		return loop.Run(ctx, &agent.Session{ID: row.ID, Conversation: conversation}, provider.UserMessage(text))
	}, emitter)
	first, _, err := s.Start(t.Context(), "home", child(), fg)
	if err != nil {
		t.Fatal(err)
	}
	<-p.started
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			input := child()
			input.Brief = "replacement"
			row, _, err := s.Start(t.Context(), "home", input, fg)
			if err != nil || row.ID != first.ID || row.Brief != first.Brief || row.State != store.SessionRunning {
				t.Errorf("duplicate start: %+v %v", row, err)
			}
		})
	}
	wg.Wait()
	if p.provider.Calls() != 1 {
		t.Fatalf("duplicate starts made %d model calls", p.provider.Calls())
	}
	p.release <- struct{}{}
	awaitState(t, st, store.SessionIdle)
	duplicate, _, err := s.Start(t.Context(), "home", child(), fg)
	if err != nil || duplicate.ID != first.ID || duplicate.State != store.SessionIdle || p.provider.Calls() != 1 {
		t.Fatalf("completed duplicate: %+v %v, calls %d", duplicate, err, p.provider.Calls())
	}
	for range 20 {
		wg.Go(func() {
			row, _, err := s.Message(t.Context(), "home", "plumber", "follow up", fg, "")
			if err != nil || row.ID != first.ID || row.State != store.SessionRunning {
				t.Errorf("resume: %+v %v", row, err)
			}
		})
	}
	wg.Wait()
	<-p.started
	if p.provider.Calls() != 2 {
		t.Fatalf("concurrent resumes made %d model calls", p.provider.Calls())
	}
	if _, err := s.Waiting(t.Context(), "home", "plumber", "waiting for input"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Waiting(t.Context(), "home", "plumber", ""); err != nil {
		t.Fatal(err)
	}
	p.release <- struct{}{}
	awaitState(t, st, store.SessionFailed)
	if _, _, err := s.Message(t.Context(), "home", "plumber", "try again", fg, ""); err != nil {
		t.Fatal(err)
	}
	<-p.started
	cancelled, err := s.Cancel(t.Context(), "home", "plumber")
	if err != nil || cancelled.State != store.SessionCancelled {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	if p.provider.Calls() != 3 {
		t.Fatalf("model calls = %d", p.provider.Calls())
	}
	s.Close()
	mu.Lock()
	defer mu.Unlock()
	want := []struct {
		state  store.SessionState
		reason string
	}{
		{store.SessionRunning, ""}, {store.SessionRunning, ""}, {store.SessionIdle, ""}, {store.SessionRunning, ""},
		{store.SessionWaiting, "waiting for input"}, {store.SessionRunning, ""},
		{store.SessionFailed, ""}, {store.SessionRunning, ""}, {store.SessionCancelled, ""},
	}
	if len(updates) != len(want) {
		t.Fatalf("updates = %+v, want %d", updates, len(want))
	}
	for i, update := range updates {
		var payload event.SessionUpdated
		if update.event.Type != event.TypeSessionUpdated || update.event.Topic != event.TopicGlobal || update.event.DecodePayload(&payload) != nil || payload.SessionID != first.ID || update.state != want[i].state || update.reason != want[i].reason {
			t.Errorf("update %d: %+v, want %+v", i, update, want[i])
		}
	}
}

func TestRunsGetTheirPriorityAndCancelledRunsAreRecordedCancelled(t *testing.T) {
	st := newMemory()
	type turn struct {
		text     string
		priority inference.Priority
	}
	turns := make(chan turn, 2)
	s := service(t, st, func(_ context.Context, _ store.Session, text string, p inference.Priority) error {
		turns <- turn{text, p}
		// A run stopped from outside the service, as the user's abort is.
		return fmt.Errorf("run aborted: %w", subagent.ErrCancelled)
	}, nil)
	row, started, err := s.Start(t.Context(), "home", child(), inference.Background)
	if err != nil || !started || row.State != store.SessionRunning {
		t.Fatalf("start: %+v %v %v", row, started, err)
	}
	if got := <-turns; got != (turn{child().Brief, inference.Background}) {
		t.Fatalf("start turn = %+v", got)
	}
	awaitState(t, st, store.SessionCancelled)
	if _, accepted, err := s.Message(t.Context(), "home", "plumber", "again", inference.Foreground, ""); err != nil || !accepted {
		t.Fatalf("message: %v %v", accepted, err)
	}
	if got := <-turns; got != (turn{"again", inference.Foreground}) {
		t.Fatalf("message turn = %+v", got)
	}
	awaitState(t, st, store.SessionCancelled)
}

func TestACancelledContextInsideTheRunIsAFailure(t *testing.T) {
	st := newMemory()
	// A provider that gives up with context.Canceled has not been cancelled
	// by anyone: the child failed.
	s := service(t, st, func(context.Context, store.Session, string, inference.Priority) error {
		return fmt.Errorf("provider: %w", context.Canceled)
	}, nil)
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
		t.Fatal(err)
	}
	awaitState(t, st, store.SessionFailed)
}

func (m *memory) result(name string) string {
	row, _ := m.Subagent(context.Background(), "home", name)
	return row.Result
}

func TestReportRendersAndReplacesTheLastReport(t *testing.T) {
	st := newMemory()
	out := &events{}
	release := make(chan struct{})
	var s *subagent.Service
	s = serviceWith(t, st, subagent.Options{Events: out, Run: func(ctx context.Context, row store.Session, _ string, _ inference.Priority) error {
		<-release
		if err := s.Report(ctx, row.ID, subagent.Report{Summary: " Found two plumbers\ncheaper one first ", Outputs: []string{"notes.md"}, FollowUps: []string{"call both", "book one"}}); err != nil {
			return err
		}
		return s.Report(ctx, row.ID, subagent.Report{Summary: "Found two plumbers\ncheaper one first", Outputs: []string{"notes.md"}, FollowUps: []string{"call both", "book one"}})
	}})
	row, _, err := s.Start(t.Context(), "home", child(), fg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Report(t.Context(), "other", subagent.Report{Summary: "x"}); err == nil {
		t.Fatal("reported for a session with no assignment")
	}
	close(release)
	awaitState(t, st, store.SessionIdle)
	want := "Found two plumbers\ncheaper one first\n\nOutputs:\n- notes.md\n\nFollow-ups:\n- call both\n- book one"
	if got := st.result("plumber"); got != want {
		t.Fatalf("result = %q, want %q", got, want)
	}
	if err := s.Report(t.Context(), row.ID, subagent.Report{Summary: "late"}); err == nil {
		t.Fatal("reported after the assignment ended")
	}
	if got := st.result("plumber"); got != want {
		t.Fatalf("late report changed the result to %q", got)
	}
}

func TestReportValidation(t *testing.T) {
	st := newMemory()
	started := make(chan struct{})
	s := serviceWith(t, st, subagent.Options{Run: func(ctx context.Context, _ store.Session, _ string, _ inference.Priority) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}})
	row, _, err := s.Start(t.Context(), "home", child(), fg)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	many := make([]string, 21)
	for i := range many {
		many[i] = "x"
	}
	for name, r := range map[string]subagent.Report{
		"empty summary":      {Summary: "  "},
		"NUL":                {Summary: "a\x00b"},
		"invalid UTF-8":      {Summary: string([]byte{0xff})},
		"long summary":       {Summary: strings.Repeat("a", 32<<10+1)},
		"too many outputs":   {Summary: "s", Outputs: many},
		"too many followups": {Summary: "s", FollowUps: many},
		"empty output":       {Summary: "s", Outputs: []string{""}},
		"long follow-up":     {Summary: "s", FollowUps: []string{strings.Repeat("a", 1025)}},
	} {
		if err := s.Report(t.Context(), row.ID, r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if got := st.result("plumber"); got != "" {
		t.Fatalf("invalid reports stored %q", got)
	}
	if err := s.Report(t.Context(), row.ID, subagent.Report{Summary: strings.Repeat("a", 32<<10), Outputs: many[:20]}); err != nil {
		t.Fatalf("report at the limits: %v", err)
	}
}

func TestRunWithoutReportDraftsOnceThenTakesTheFinalMessage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		replies  []string // what each run does: "report" calls the tool
		final    string
		finalErr error
		wantRuns []string
		want     string
	}{
		{"reports", []string{"report"}, "ignored", nil, []string{"Find a plumber"}, "reported"},
		{"drafts after silence", []string{"", "report"}, "ignored", nil, []string{"Find a plumber", "Draft your report now with the report tool."}, "reported"},
		{"falls back to the final message", []string{"", ""}, "  Two plumbers found.\n", nil, []string{"Find a plumber", "Draft your report now with the report tool."}, "Two plumbers found."},
		{"no final message", []string{"", ""}, "", nil, []string{"Find a plumber", "Draft your report now with the report tool."}, "The subagent ended without a report or a final message."},
		{"unreadable final message", []string{"", ""}, "ignored", errors.New("storage unavailable"), []string{"Find a plumber", "Draft your report now with the report tool."}, "The subagent ended without a report or a final message."},
		{"clips a long final message", []string{"", ""}, strings.Repeat("é", 40<<10), nil, []string{"Find a plumber", "Draft your report now with the report tool."}, strings.Repeat("é", 32<<10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newMemory()
			var mu sync.Mutex
			var runs []string
			var s *subagent.Service
			s = serviceWith(t, st, subagent.Options{
				Run: func(ctx context.Context, row store.Session, text string, p inference.Priority) error {
					mu.Lock()
					n := len(runs)
					runs = append(runs, text)
					mu.Unlock()
					if p != inference.Background {
						t.Errorf("run %d priority = %v", n, p)
					}
					if tc.replies[n] == "report" {
						return s.Report(ctx, row.ID, subagent.Report{Summary: "reported"})
					}
					return nil
				},
				Final: func(context.Context, store.Session) (string, error) { return tc.final, tc.finalErr },
			})
			if _, _, err := s.Start(t.Context(), "home", child(), inference.Background); err != nil {
				t.Fatal(err)
			}
			awaitState(t, st, store.SessionIdle)
			if got := st.result("plumber"); got != tc.want {
				t.Errorf("result = %.60q, want %.60q", got, tc.want)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(runs, tc.wantRuns) {
				t.Errorf("runs = %q, want %q", runs, tc.wantRuns)
			}
		})
	}
}

func TestEachAssignmentNeedsItsOwnReport(t *testing.T) {
	st := newMemory()
	var runs []string
	var mu sync.Mutex
	var s *subagent.Service
	s = serviceWith(t, st, subagent.Options{Run: func(ctx context.Context, row store.Session, text string, _ inference.Priority) error {
		mu.Lock()
		runs = append(runs, text)
		mu.Unlock()
		if text == "first" || text == "Draft your report now with the report tool." {
			return s.Report(ctx, row.ID, subagent.Report{Summary: "report for " + text})
		}
		return nil
	}, Final: func(context.Context, store.Session) (string, error) { return "final of the second", nil }})
	first := child()
	first.Brief = "first"
	if _, _, err := s.Start(t.Context(), "home", first, fg); err != nil {
		t.Fatal(err)
	}
	awaitState(t, st, store.SessionIdle)
	if _, _, err := s.Message(t.Context(), "home", "plumber", "second", fg, ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for st.result("plumber") == "report for first" {
		if time.Now().After(deadline) {
			t.Fatal("the first report stood in for the second assignment")
		}
		time.Sleep(time.Millisecond)
	}
	awaitState(t, st, store.SessionIdle)
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"first", "second", "Draft your report now with the report tool."}; !reflect.DeepEqual(runs, want) {
		t.Errorf("runs = %q, want %q", runs, want)
	}
}

func TestFailedAndCancelledRunsAreNotAskedForReports(t *testing.T) {
	for name, runErr := range map[string]error{
		"failed":    errors.New("provider unavailable"),
		"cancelled": fmt.Errorf("aborted: %w", subagent.ErrCancelled),
	} {
		t.Run(name, func(t *testing.T) {
			st := newMemory()
			var runs int
			var mu sync.Mutex
			s := serviceWith(t, st, subagent.Options{Run: func(context.Context, store.Session, string, inference.Priority) error {
				mu.Lock()
				runs++
				mu.Unlock()
				return runErr
			}, Final: func(context.Context, store.Session) (string, error) {
				t.Error("read a final message of a run that did not complete")
				return "", nil
			}})
			if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
				t.Fatal(err)
			}
			awaitState(t, st, store.SessionState(name))
			mu.Lock()
			defer mu.Unlock()
			if runs != 1 || st.result("plumber") != "" {
				t.Errorf("runs = %d, result = %q", runs, st.result("plumber"))
			}
		})
	}
}

func TestCancelAndCloseDuringTheDraftEndWithoutAFallbackReport(t *testing.T) {
	for _, stop := range []string{"cancel", "close"} {
		t.Run(stop, func(t *testing.T) {
			st := newMemory()
			drafting := make(chan struct{})
			s := serviceWith(t, st, subagent.Options{Run: func(ctx context.Context, _ store.Session, text string, _ inference.Priority) error {
				if text != "Draft your report now with the report tool." {
					return nil
				}
				close(drafting)
				<-ctx.Done()
				return ctx.Err()
			}, Final: func(context.Context, store.Session) (string, error) {
				t.Error("fallback report after a stop")
				return "late", nil
			}})
			if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
				t.Fatal(err)
			}
			<-drafting
			if stop == "cancel" {
				if row, err := s.Cancel(t.Context(), "home", "plumber"); err != nil || row.State != store.SessionCancelled {
					t.Fatalf("cancel: %+v %v", row, err)
				}
			} else {
				s.Close()
			}
			if row := awaitState(t, st, store.SessionCancelled); row.Result != "" {
				t.Fatalf("result = %q", row.Result)
			}
		})
	}
}

func TestMaxRunningReservesSlotsUnderConcurrentStarts(t *testing.T) {
	st := newMemory()
	var limit atomic.Int32
	limit.Store(3)
	running := make(chan string, 64)
	s := serviceWith(t, st, subagent.Options{
		Limit: func(context.Context) int { return int(limit.Load()) },
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
	// A refused start leaves no child behind, and one that exists is still
	// reported at the limit instead of refused.
	if rows, _ := s.Status(t.Context(), "home", ""); len(rows) != 3 {
		t.Fatalf("children = %d, want 3", len(rows))
	}
	names := []string{<-running, <-running, <-running}
	if row, ok, err := s.Start(t.Context(), "home", func() store.Session { c := child(); c.Name = names[0]; return c }(), fg); err != nil || ok || row.Name != names[0] {
		t.Fatalf("duplicate at the limit: %+v %v %v", row, ok, err)
	}
	// A raised limit admits more at once; a freed slot admits one.
	limit.Store(4)
	extra := child()
	extra.Name = "extra"
	if _, ok, err := s.Start(t.Context(), "home", extra, fg); err != nil || !ok {
		t.Fatalf("start under a raised limit: %v %v", ok, err)
	}
	over := child()
	over.Name = "over"
	if _, _, err := s.Start(t.Context(), "home", over, fg); !errors.Is(err, subagent.ErrTooManyRunning) {
		t.Fatalf("start over the raised limit: %v", err)
	}
	if _, err := s.Cancel(t.Context(), "home", names[1]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Message(t.Context(), "home", names[1], "again", fg, ""); err != nil {
		t.Fatalf("message into the freed slot: %v", err)
	}
	// Slots are the running children: cancelled ones hold none.
	if _, err := s.Cancel(t.Context(), "home", names[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(t.Context(), "home", names[0]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Start(t.Context(), "home", over, fg); err != nil {
		t.Fatalf("start in a freed slot: %v", err)
	}
	if _, _, err := s.Message(t.Context(), "home", names[0], "again", fg, ""); err != nil {
		t.Fatalf("message into the last free slot: %v", err)
	}
	if _, _, err := s.Message(t.Context(), "home", names[2], "again", fg, ""); !errors.Is(err, subagent.ErrTooManyRunning) {
		t.Fatalf("message over the limit: %v", err)
	}
}

func TestMaxRunningDefaultsAndFloor(t *testing.T) {
	for name, tc := range map[string]struct {
		limit func(context.Context) int
		want  int
	}{
		"default": {nil, subagent.DefaultMaxRunning},
		"zero":    {func(context.Context) int { return 0 }, 1},
	} {
		t.Run(name, func(t *testing.T) {
			st := newMemory()
			s := serviceWith(t, st, subagent.Options{Limit: tc.limit, Run: func(ctx context.Context, _ store.Session, _ string, _ inference.Priority) error {
				<-ctx.Done()
				return ctx.Err()
			}})
			started := 0
			for i := range 8 {
				c := child()
				c.Name = fmt.Sprintf("agent-%d", i)
				if _, ok, err := s.Start(t.Context(), "home", c, fg); err == nil && ok {
					started++
				}
			}
			if started != tc.want {
				t.Fatalf("started %d, want %d", started, tc.want)
			}
		})
	}
}

func TestStoredReportIsCappedWhetherReportedOrFallback(t *testing.T) {
	const limit = 64 << 10
	items := make([]string, 20)
	for i := range items {
		items[i] = strings.Repeat("é", 512)
	}
	big := subagent.Report{Summary: strings.Repeat("é", 16<<10), Outputs: items, FollowUps: items}
	if len(big.Text()) <= limit {
		t.Fatalf("test report is %d bytes, not over the cap", len(big.Text()))
	}
	for _, via := range []string{"report", "fallback"} {
		t.Run(via, func(t *testing.T) {
			st := newMemory()
			var s *subagent.Service
			s = serviceWith(t, st, subagent.Options{
				Run: func(ctx context.Context, row store.Session, _ string, _ inference.Priority) error {
					if via == "report" {
						return s.Report(ctx, row.ID, big)
					}
					return nil
				},
				Final: func(context.Context, store.Session) (string, error) { return big.Text(), nil },
			})
			if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
				t.Fatal(err)
			}
			awaitState(t, st, store.SessionIdle)
			got := st.result("plumber")
			if len(got) > limit || len(got) < limit-1 || !utf8.ValidString(got) || !strings.HasPrefix(big.Text(), got) {
				t.Fatalf("stored %d bytes, valid=%v", len(got), utf8.ValidString(got))
			}
		})
	}
}

func TestReportedIsToldOfEachAssignmentThatEndsIdleWithAReport(t *testing.T) {
	st := newMemory()
	reported := make(chan store.Session, 8)
	var s *subagent.Service
	s = serviceWith(t, st, subagent.Options{
		Run: func(ctx context.Context, row store.Session, text string, _ inference.Priority) error {
			switch text {
			case "fail":
				return errors.New("model unavailable")
			case "hold":
				<-ctx.Done()
				return ctx.Err()
			}
			return s.Report(ctx, row.ID, subagent.Report{Summary: "done: " + text})
		},
		Final:    func(context.Context, store.Session) (string, error) { return "", nil },
		Reported: func(_ context.Context, row store.Session) { reported <- row },
	})
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
		t.Fatal(err)
	}
	got := <-reported
	if got.Name != "plumber" || got.State != store.SessionIdle || got.Result != "done: Find a plumber" {
		t.Fatalf("reported %+v, want the idle child with its report", got)
	}

	// A failed run and a cancelled one end without news for Eika.
	if _, _, err := s.Message(t.Context(), "home", "plumber", "fail", fg, ""); err != nil {
		t.Fatal(err)
	}
	awaitState(t, st, store.SessionFailed)
	if _, _, err := s.Message(t.Context(), "home", "plumber", "hold", fg, ""); err != nil {
		t.Fatal(err)
	}
	awaitState(t, st, store.SessionRunning)
	if _, err := s.Cancel(t.Context(), "home", "plumber"); err != nil {
		t.Fatal(err)
	}
	// The next assignment's report is news again.
	if _, _, err := s.Message(t.Context(), "home", "plumber", "again", fg, ""); err != nil {
		t.Fatal(err)
	}
	if got := <-reported; got.Result != "done: again" {
		t.Fatalf("second report = %q", got.Result)
	}
	select {
	case extra := <-reported:
		t.Fatalf("reported %+v for an assignment without a report", extra)
	default:
	}
}
