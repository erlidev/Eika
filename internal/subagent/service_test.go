package subagent_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
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

type events struct {
	mu   sync.Mutex
	rows []event.Event
}

func (e *events) Emit(_ context.Context, row event.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rows = append(e.rows, row)
}

func child() store.Session {
	return store.Session{Name: "plumber", Agent: "researcher", Brief: "Find a plumber"}
}
func service(t *testing.T, st *memory, run subagent.Run, out event.Emitter) *subagent.Service {
	t.Helper()
	s := subagent.New(t.Context(), st, run, out, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	s := service(t, st, func(ctx context.Context, row store.Session, text string) error {
		started <- row.ID + ":" + text
		<-ctx.Done()
		return ctx.Err()
	}, out)
	ctx, cancelRequest := context.WithCancel(t.Context())
	first, err := s.Start(ctx, "home", child())
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
			row, err := s.Start(t.Context(), "home", input)
			if err != nil || row.ID != first.ID || row.Brief != first.Brief {
				t.Errorf("duplicate: %+v %v", row, err)
			}
			row, err = s.Message(t.Context(), "home", "plumber", "do not accept")
			if err != nil || row.State != store.SessionRunning {
				t.Errorf("running message: %+v %v", row, err)
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
	row, err := s.Message(t.Context(), "home", "plumber", "not an answer yet")
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
			if _, err := s.Message(t.Context(), "home", "plumber", "follow up"); err != nil {
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
	if _, err := s.Message(t.Context(), "home", "plumber", "closed"); err == nil {
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
	s := service(t, st, func(ctx context.Context, row store.Session, text string) error {
		current := sessions[row.ID]
		if current == nil {
			current = &agent.Session{ID: row.ID, Conversation: agent.NewConversation()}
			sessions[row.ID] = current
		}
		loop := agent.New(p, nil, agent.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		return loop.Run(ctx, current, provider.UserMessage(text))
	}, nil)
	row, err := s.Start(t.Context(), "home", child())
	if err != nil {
		t.Fatal(err)
	}
	awaitState(t, st, store.SessionIdle)
	next, err := s.Message(t.Context(), "home", "plumber", "follow up")
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
	s := service(t, st, func(context.Context, store.Session, string) error { return errors.New("provider unavailable") }, nil)
	if _, err := s.Start(t.Context(), "home", child()); err != nil {
		t.Fatal(err)
	}
	awaitState(t, st, store.SessionFailed)
	st.mu.Lock()
	st.fail = true
	st.mu.Unlock()
	if _, err := s.Message(t.Context(), "home", "plumber", "retry"); err == nil {
		t.Fatal("ignored state write error")
	}
	st.mu.Lock()
	st.fail = false
	st.mu.Unlock()
	if _, err := s.Message(t.Context(), "home", "plumber", "retry"); err != nil {
		t.Fatal(err)
	}
	awaitState(t, st, store.SessionFailed)
}

func TestStartValidationAndScope(t *testing.T) {
	st := newMemory()
	s := service(t, st, func(context.Context, store.Session, string) error { return nil }, nil)
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
			if _, err := s.Start(t.Context(), "home", c); err == nil {
				t.Fatal("accepted invalid child")
			}
		})
	}
	if _, err := s.Start(t.Context(), "home", child()); err != nil {
		t.Fatal(err)
	}
	first := awaitState(t, st, store.SessionIdle)
	if _, err := s.Start(t.Context(), first.ID, child()); err == nil {
		t.Fatal("accepted recursive parent")
	}
	other, err := s.Start(t.Context(), "other", child())
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
	s := service(t, st, func(ctx context.Context, _ store.Session, _ string) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	if _, err := s.Start(t.Context(), "home", child()); err != nil {
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
	row, err := s.Message(t.Context(), "home", "plumber", "retry")
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
			s := service(t, st, func(ctx context.Context, _ store.Session, text string) error {
				runs <- text
				if text == child().Brief {
					<-finish
					return nil
				}
				<-ctx.Done()
				return ctx.Err()
			}, nil)
			if _, err := s.Start(t.Context(), "home", child()); err != nil {
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
			if _, err := s.Message(t.Context(), "home", "plumber", "again"); err != nil {
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
	s := service(t, st, func(_ context.Context, _ store.Session, text string) error {
		started <- text
		return nil
	}, nil)
	if _, err := s.Start(t.Context(), "home", child()); err == nil {
		t.Fatal("accepted failed initial write")
	}
	if _, err := st.Subagent(t.Context(), "home", "plumber"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed write left a child: %v", err)
	}
	st.mu.Lock()
	st.fail = false
	st.mu.Unlock()
	row, err := s.Start(t.Context(), "home", child())
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
	s := service(t, st, func(ctx context.Context, row store.Session, text string) error {
		loop := agent.New(p, nil, agent.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		return loop.Run(ctx, &agent.Session{ID: row.ID, Conversation: conversation}, provider.UserMessage(text))
	}, emitter)
	first, err := s.Start(t.Context(), "home", child())
	if err != nil {
		t.Fatal(err)
	}
	<-p.started
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			input := child()
			input.Brief = "replacement"
			row, err := s.Start(t.Context(), "home", input)
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
	duplicate, err := s.Start(t.Context(), "home", child())
	if err != nil || duplicate.ID != first.ID || duplicate.State != store.SessionIdle || p.provider.Calls() != 1 {
		t.Fatalf("completed duplicate: %+v %v, calls %d", duplicate, err, p.provider.Calls())
	}
	for range 20 {
		wg.Go(func() {
			row, err := s.Message(t.Context(), "home", "plumber", "follow up")
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
	if _, err := s.Message(t.Context(), "home", "plumber", "try again"); err != nil {
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
		{store.SessionRunning, ""}, {store.SessionIdle, ""}, {store.SessionRunning, ""},
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
