package subagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/store"
)

// Store is the session storage used by the lifecycle service.
type Store interface {
	Session(context.Context, string) (store.Session, error)
	CreateSession(context.Context, store.Session) (store.Session, error)
	Subagent(context.Context, string, string) (store.Session, error)
	Subagents(context.Context, string) ([]store.Session, error)
	SetSubagentState(context.Context, string, store.SessionState, string) (store.Session, error)
}

// Run executes one message in the child's stored context and returns when done.
// It must stop and join its work when ctx is cancelled.
type Run func(ctx context.Context, child store.Session, text string) error

// Service serializes lifecycle changes and owns each child run until it ends.
type Service struct {
	ctx    context.Context
	cancel context.CancelFunc
	store  Store
	run    Run
	events event.Emitter
	log    *slog.Logger
	wg     sync.WaitGroup

	mu     sync.Mutex
	closed bool
	active map[string]*execution
}

type execution struct {
	cancel    context.CancelFunc
	done      chan struct{}
	cancelled bool
	err       error
	result    store.Session
}

// New creates a service bounded by ctx. Close must run before storage closes.
func New(ctx context.Context, st Store, run Run, events event.Emitter, log *slog.Logger) *Service {
	ctx, cancel := context.WithCancel(ctx)
	if events == nil {
		events = event.Discard
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{ctx: ctx, cancel: cancel, store: st, run: run, events: events, log: log, active: make(map[string]*execution)}
}

// Start creates a named child and starts its brief. An existing name returns
// its status without starting work or replacing any fields.
func (s *Service) Start(ctx context.Context, parent string, child store.Session) (store.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return store.Session{}, fmt.Errorf("subagent service is closed")
	}
	if err := validText("name", child.Name, 128); err != nil {
		return store.Session{}, err
	}
	home, err := s.store.Session(ctx, parent)
	if err != nil {
		return store.Session{}, err
	}
	if home.Kind != store.SessionHome {
		return store.Session{}, fmt.Errorf("subagent parent must be home")
	}
	old, err := s.store.Subagent(ctx, parent, child.Name)
	if err == nil {
		return old, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.Session{}, err
	}
	if child.Agent != "worker" && child.Agent != "researcher" {
		return store.Session{}, fmt.Errorf("subagent agent must be worker or researcher")
	}
	if err := validText("brief", child.Brief, 64<<10); err != nil {
		return store.Session{}, err
	}
	// Copy only creation fields; callers cannot set lifecycle or profile fields.
	name := child.Name
	child, err = s.store.CreateSession(ctx, store.Session{Kind: store.SessionSubagent,
		Owner: home.Owner, ParentSessionID: home.ID, Name: child.Name, Title: child.Name,
		Agent: child.Agent, Brief: child.Brief, ProjectID: child.ProjectID, ComputerID: child.ComputerID,
		State: store.SessionRunning})
	if errors.Is(err, store.ErrConflict) {
		return s.store.Subagent(ctx, parent, name)
	}
	if err != nil {
		return store.Session{}, err
	}
	s.updated(ctx, child)
	return s.begin(ctx, child, child.Brief)
}

// Message resumes a child with no active run. A child whose run is active
// returns its status without accepting the text. A stored running or waiting
// state with no active run is a lost final write and is resumed too. Question
// relay is separate.
func (s *Service) Message(ctx context.Context, parent, name, text string) (store.Session, error) {
	if err := validText("text", text, 64<<10); err != nil {
		return store.Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return store.Session{}, fmt.Errorf("subagent service is closed")
	}
	child, err := s.store.Subagent(ctx, parent, name)
	if err != nil {
		return store.Session{}, err
	}
	if s.active[child.ID] != nil {
		return child, nil
	}
	return s.begin(ctx, child, text)
}

func (s *Service) begin(ctx context.Context, child store.Session, text string) (store.Session, error) {
	child, err := s.state(ctx, child, store.SessionRunning, "")
	if err != nil {
		return store.Session{}, err
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	active := &execution{cancel: cancel, done: make(chan struct{})}
	s.active[child.ID] = active
	s.wg.Add(1)
	go s.execute(runCtx, child, text, active)
	return child, nil
}

func (s *Service) execute(ctx context.Context, child store.Session, text string, active *execution) {
	defer s.wg.Done()
	err := s.run(ctx, child, text)
	s.mu.Lock()
	defer s.mu.Unlock()
	state := store.SessionIdle
	if active.cancelled || ctx.Err() != nil {
		state = store.SessionCancelled
	} else if err != nil {
		state = store.SessionFailed
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	active.result, active.err = s.state(finishCtx, child, state, "")
	if active.err != nil {
		s.log.Error("record subagent state", "session_id", child.ID, "error", active.err)
	}
	active.cancel()
	delete(s.active, child.ID)
	close(active.done)
}

// Waiting records a code-owned wait or returns the child to running. It accepts
// only an active, uncancelled run. An empty reason ends the wait.
func (s *Service) Waiting(ctx context.Context, parent, name, reason string) (store.Session, error) {
	if reason != "" {
		if err := validText("waiting reason", reason, 4096); err != nil {
			return store.Session{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	child, err := s.store.Subagent(ctx, parent, name)
	if err != nil {
		return store.Session{}, err
	}
	active := s.active[child.ID]
	if active == nil || active.cancelled {
		return store.Session{}, fmt.Errorf("subagent has no active run")
	}
	state := store.SessionWaiting
	if reason == "" {
		state = store.SessionRunning
	}
	return s.state(ctx, child, state, reason)
}

// Cancel stops a child's current execution and waits for its final state write.
// A child without a current execution keeps its stored state, except that a
// stored running or waiting state is a lost final write and becomes cancelled.
func (s *Service) Cancel(ctx context.Context, parent, name string) (store.Session, error) {
	s.mu.Lock()
	child, err := s.store.Subagent(ctx, parent, name)
	if err != nil {
		s.mu.Unlock()
		return store.Session{}, err
	}
	active := s.active[child.ID]
	if active == nil {
		defer s.mu.Unlock()
		if child.State == store.SessionRunning || child.State == store.SessionWaiting {
			return s.state(ctx, child, store.SessionCancelled, "")
		}
		return child, nil
	}
	active.cancelled = true
	active.cancel()
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return store.Session{}, ctx.Err()
	case <-active.done:
	}
	if active.err != nil {
		return store.Session{}, active.err
	}
	return active.result, nil
}

// Status returns one child by name, or every child when name is empty.
func (s *Service) Status(ctx context.Context, parent, name string) ([]store.Session, error) {
	if name == "" {
		return s.store.Subagents(ctx, parent)
	}
	child, err := s.store.Subagent(ctx, parent, name)
	if err != nil {
		return nil, err
	}
	return []store.Session{child}, nil
}

// Close stops and joins all executions. Further starts and messages are refused.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Service) state(ctx context.Context, child store.Session, state store.SessionState, reason string) (store.Session, error) {
	if child.State == state && child.WaitingReason == reason {
		return child, nil
	}
	child, err := s.store.SetSubagentState(ctx, child.ID, state, reason)
	if err == nil {
		s.updated(ctx, child)
	}
	return child, err
}

func (s *Service) updated(ctx context.Context, child store.Session) {
	e, err := event.New(event.TypeSessionUpdated, event.TopicGlobal, event.SessionUpdated{SessionID: child.ID, ComputerID: child.ComputerID})
	if err == nil {
		s.events.Emit(ctx, e)
	}
}

func validText(field, text string, limit int) error {
	if !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') || strings.TrimSpace(text) == "" || len(text) > limit {
		return fmt.Errorf("%s must be nonempty UTF-8 without NUL, at most %d bytes", field, limit)
	}
	return nil
}
