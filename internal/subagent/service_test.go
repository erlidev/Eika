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
	mu   sync.Mutex
	rows map[string]store.Session
	fail bool
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
	row.State = store.SessionIdle
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
	if len(out.rows) != 8 {
		t.Fatalf("events = %d, want 8", len(out.rows))
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
	started := make(chan struct{})
	s := service(t, st, func(ctx context.Context, _ store.Session, _ string) error {
		close(started)
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
	// The stored running state prevents another run before recovery.
	row, err := s.Message(t.Context(), "home", "plumber", "retry")
	if err != nil || row.State != store.SessionRunning {
		t.Fatalf("lost failed reservation: %+v %v", row, err)
	}
}
