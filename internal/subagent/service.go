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
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/store"
)

// Store is the session storage used by the lifecycle service.
type Store interface {
	Session(context.Context, string) (store.Session, error)
	CreateSession(context.Context, store.Session) (store.Session, error)
	Subagent(context.Context, string, string) (store.Session, error)
	Subagents(context.Context, string) ([]store.Session, error)
	SetSubagentState(context.Context, string, store.SessionState, string) (store.Session, error)
	SetSubagentResult(context.Context, string, string) (store.Session, error)
}

// DefaultMaxRunning is how many subagents may run at once until the
// settings say otherwise.
const DefaultMaxRunning = 4

// DraftReport is the message that asks a run that ended without a report for
// one.
const DraftReport = "Draft your report now with the report tool."

// noReport is the report of a run that ended with neither a report nor a
// final message.
const noReport = "The subagent ended without a report or a final message."

// unscreenedReport stands in for a report that could not be screened, which
// happens only when a removal could not be recorded.
const unscreenedReport = "The subagent's report could not be delivered."

// ErrTooManyRunning is what Start and Message return, wrapped, when the
// limit on running subagents leaves no room for another.
var ErrTooManyRunning = errors.New("too many subagents are running")

// ErrCancelled is what a Run returns, wrapped or not, when someone stopped its
// run from outside the service, as the user's abort does. The child is
// recorded cancelled; any other error records it failed.
var ErrCancelled = errors.New("subagent run cancelled")

// Run executes one message in the child's stored context at priority and
// returns when done. It must stop and join its work when ctx is cancelled.
type Run func(ctx context.Context, child store.Session, text string, priority inference.Priority) error

// Report is what a subagent hands back when it finishes an assignment.
type Report struct {
	Summary   string
	Outputs   []string
	FollowUps []string
}

// Report limits, which the report tool also tells the model about.
const (
	maxSummaryBytes = 32 << 10
	maxItemBytes    = 1 << 10
	maxItems        = 20
	maxResultBytes  = 64 << 10
)

// Text is the report as it is stored and shown: the summary, then the lists
// that are not empty. The board shows the summary's first line.
func (r Report) Text() string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(r.Summary))
	for _, list := range []struct {
		title string
		items []string
	}{{"Outputs", r.Outputs}, {"Follow-ups", r.FollowUps}} {
		if len(list.items) == 0 {
			continue
		}
		b.WriteString("\n\n" + list.title + ":")
		for _, item := range list.items {
			b.WriteString("\n- " + strings.TrimSpace(item))
		}
	}
	return b.String()
}

func (r Report) validate() error {
	if err := validText("summary", r.Summary, maxSummaryBytes); err != nil {
		return err
	}
	for _, list := range []struct {
		field string
		items []string
	}{{"outputs", r.Outputs}, {"follow_ups", r.FollowUps}} {
		if len(list.items) > maxItems {
			return fmt.Errorf("%s must have at most %d entries", list.field, maxItems)
		}
		for _, item := range list.items {
			if err := validText(list.field+" entry", item, maxItemBytes); err != nil {
				return err
			}
		}
	}
	return nil
}

// Options are the application's side of the service.
type Options struct {
	// Run executes one message in a child's session. It is required.
	Run Run
	// Final returns a child's last assistant message, which becomes its report
	// when it ends an assignment without calling report. It is required.
	Final func(context.Context, store.Session) (string, error)
	// Limit returns how many subagents may run at once; nil means
	// DefaultMaxRunning. A value below one is one.
	Limit func(context.Context) int
	// Reported is told about each assignment that ends with the child idle
	// after a report, with the child as it was stored then: its Result is
	// the report. It runs on the execution's goroutine, outside the service
	// lock, before Close can return. Nil tells nobody.
	Reported func(context.Context, store.Session)
	// Ended is told about each assignment that ends failed, with the child as
	// stored then: its WaitingReason holds why. A stop someone asked for
	// (Cancel, an abort) is known to the one who asked, and a shutdown is
	// told at the next start, so neither is announced. Same goroutine and
	// ordering rules as Reported. Nil tells nobody.
	Ended func(context.Context, store.Session)
	// Started is told about each new child session after it is stored and
	// before its first run. Nil tells nobody.
	Started func(context.Context, store.Session)
	// Screen cleans a report's text for the child session before it is stored,
	// so the stored report is what Eika reads. An error means the text could not
	// be cleaned and must not be used. Nil keeps the text as it is.
	Screen func(ctx context.Context, sessionID, text string) (string, error)
	Events event.Emitter
	Log    *slog.Logger
}

// Service serializes lifecycle changes and owns each child run until it ends.
type Service struct {
	ctx      context.Context
	cancel   context.CancelFunc
	store    Store
	run      Run
	final    func(context.Context, store.Session) (string, error)
	limit    func(context.Context) int
	reported func(context.Context, store.Session)
	ended    func(context.Context, store.Session)
	started  func(context.Context, store.Session)
	screen   func(context.Context, string, string) (string, error)
	events   event.Emitter
	log      *slog.Logger
	wg       sync.WaitGroup

	mu     sync.Mutex
	closed bool
	active map[string]*execution
}

type execution struct {
	cancel    context.CancelFunc
	done      chan struct{}
	cancelled bool
	// reported is whether the child has reported since this execution began.
	reported bool
	err      error
	result   store.Session
	question *pendingQuestion
}

// New creates a service bounded by ctx. Close must run before storage closes.
func New(ctx context.Context, st Store, o Options) *Service {
	ctx, cancel := context.WithCancel(ctx)
	if o.Events == nil {
		o.Events = event.Discard
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Limit == nil {
		o.Limit = func(context.Context) int { return DefaultMaxRunning }
	}
	return &Service{ctx: ctx, cancel: cancel, store: st, run: o.Run, final: o.Final, limit: o.Limit,
		reported: o.Reported, ended: o.Ended, started: o.Started, screen: o.Screen, events: o.Events, log: o.Log, active: make(map[string]*execution)}
}

// Start creates a named child and starts its brief at priority. It reports
// whether it started work. An existing name returns its status without
// starting work or replacing any fields.
func (s *Service) Start(ctx context.Context, parent string, child store.Session, priority inference.Priority) (store.Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return store.Session{}, false, fmt.Errorf("subagent service is closed")
	}
	if err := validText("name", child.Name, 128); err != nil {
		return store.Session{}, false, err
	}
	home, err := s.store.Session(ctx, parent)
	if err != nil {
		return store.Session{}, false, err
	}
	if home.Kind != store.SessionHome {
		return store.Session{}, false, fmt.Errorf("subagent parent must be home")
	}
	old, err := s.store.Subagent(ctx, parent, child.Name)
	if err == nil {
		return old, false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.Session{}, false, err
	}
	if child.Agent != "worker" && child.Agent != "researcher" {
		return store.Session{}, false, fmt.Errorf("subagent agent must be worker or researcher")
	}
	if err := validText("brief", child.Brief, 64<<10); err != nil {
		return store.Session{}, false, err
	}
	if err := s.room(ctx); err != nil {
		return store.Session{}, false, err
	}
	// Copy only creation fields; callers cannot set lifecycle or profile fields.
	name := child.Name
	child, err = s.store.CreateSession(ctx, store.Session{Kind: store.SessionSubagent,
		Owner: home.Owner, ParentSessionID: home.ID, Name: child.Name, Title: child.Name,
		Agent: child.Agent, Brief: child.Brief, ProjectID: child.ProjectID, ComputerID: child.ComputerID,
		State: store.SessionRunning})
	if errors.Is(err, store.ErrConflict) {
		old, err := s.store.Subagent(ctx, parent, name)
		return old, false, err
	}
	if err != nil {
		return store.Session{}, false, err
	}
	if s.started != nil {
		s.started(ctx, child)
	}
	s.updated(ctx, child)
	child, err = s.begin(ctx, child, child.Brief, priority)
	return child, err == nil, err
}

// Message resumes a child with no active run at priority and reports whether
// it accepted the text. A child whose run is active returns its status without
// accepting the text. A stored running or waiting state with no active run is
// a lost final write and is resumed too. A waiting question takes the text as
// its answer only when questionID matches its id. It continues the same run.
func (s *Service) Message(ctx context.Context, parent, name, text string, priority inference.Priority, questionID string) (store.Session, bool, error) {
	if err := validText("text", text, 64<<10); err != nil {
		return store.Session{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return store.Session{}, false, fmt.Errorf("subagent service is closed")
	}
	child, err := s.store.Subagent(ctx, parent, name)
	if err != nil {
		return store.Session{}, false, err
	}
	if active := s.active[child.ID]; active != nil {
		if active.cancelled || active.question == nil || questionID == "" || active.question.id != questionID || !active.question.live() {
			return child, false, nil
		}
		if err := active.question.accept(text); err != nil {
			return child, false, err
		}
		child, err = s.state(ctx, child, store.SessionRunning, "")
		if err != nil {
			return store.Session{}, false, err
		}
		active.question.answer <- text
		active.question = nil
		return child, true, nil
	}
	if questionID != "" {
		return child, false, nil
	}
	if err := s.room(ctx); err != nil {
		return store.Session{}, false, err
	}
	child, err = s.begin(ctx, child, text, priority)
	return child, err == nil, err
}

// room reports whether another execution may begin. Callers hold mu from this
// check until begin has recorded the execution, so concurrent starts cannot
// all see the same free slot.
func (s *Service) room(ctx context.Context) error {
	limit := max(s.limit(ctx), 1)
	if len(s.active) >= limit {
		return fmt.Errorf("%w: %d of at most %d are running; wait for one to finish or cancel one", ErrTooManyRunning, len(s.active), limit)
	}
	return nil
}

func (s *Service) begin(ctx context.Context, child store.Session, text string, priority inference.Priority) (store.Session, error) {
	child, err := s.state(ctx, child, store.SessionRunning, "")
	if err != nil {
		return store.Session{}, err
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	active := &execution{cancel: cancel, done: make(chan struct{})}
	s.active[child.ID] = active
	s.wg.Add(1)
	go s.execute(runCtx, child, text, priority, active)
	return child, nil
}

func (s *Service) execute(ctx context.Context, child store.Session, text string, priority inference.Priority, active *execution) {
	defer s.wg.Done()
	err := s.run(ctx, child, text, priority)
	if err == nil {
		err = s.requireReport(ctx, child, priority, active)
	}
	ended, reported := s.end(ctx, child, err, active)
	switch {
	case reported && s.reported != nil:
		s.reported(context.WithoutCancel(ctx), ended)
	case !reported && s.ended != nil && ended.State == store.SessionFailed:
		s.ended(context.WithoutCancel(ctx), ended)
	}
}

// end records the state an execution ended in and releases it. It returns the
// stored child and whether the assignment ended idle with a report, which is
// when the report is news for Eika.
func (s *Service) end(ctx context.Context, child store.Session, err error, active *execution) (store.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, reason := store.SessionIdle, ""
	if active.cancelled || ctx.Err() != nil || errors.Is(err, ErrCancelled) {
		state = store.SessionCancelled
		if !active.cancelled && s.ctx.Err() != nil {
			reason = ShutdownReason
		}
	} else if err != nil {
		state, reason = store.SessionFailed, truncateReason(err.Error())
		s.log.Warn("subagent run failed", "session_id", child.ID, "error", err)
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	active.result, active.err = s.state(finishCtx, child, state, reason)
	if active.err != nil {
		s.log.Error("record subagent state", "session_id", child.ID, "error", active.err)
	}
	if active.err == nil && state == store.SessionIdle && active.reported {
		s.reportUpdate(finishCtx, active.result)
	}
	active.cancel()
	delete(s.active, child.ID)
	close(active.done)
	return active.result, active.err == nil && state == store.SessionIdle && active.reported
}

// ShutdownReason is the reason stored on a child whose run the server's
// shutdown cut off.
const ShutdownReason = "Eika shut down"

// truncateReason keeps a failure's reason short enough to store and show.
func truncateReason(text string) string {
	text = strings.TrimSpace(strings.ToValidUTF8(strings.ReplaceAll(text, "\x00", ""), ""))
	if r := []rune(text); len(r) > 500 {
		text = string(r[:500]) + "..."
	}
	return text
}

// requireReport makes a run that ended on its own leave a report. It asks the
// child to draft one, once, and if it still has not called report, takes its
// final message as the report. A run that was stopped needs none.
func (s *Service) requireReport(ctx context.Context, child store.Session, priority inference.Priority, active *execution) error {
	if ctx.Err() != nil || s.hasReported(active) {
		return nil
	}
	if err := s.run(ctx, child, DraftReport, priority); err != nil {
		return err
	}
	if s.hasReported(active) {
		return nil
	}
	text, err := s.final(ctx, child)
	if err != nil {
		s.log.Warn("read subagent final message", "session_id", child.ID, "error", err)
		text = ""
	}
	text = strings.TrimSpace(strings.ToValidUTF8(strings.ReplaceAll(text, "\x00", ""), ""))
	if text == "" {
		text = noReport
	}
	text, err = s.cleanReport(ctx, child.ID, text)
	if err != nil {
		s.log.Warn("screen subagent report", "session_id", child.ID, "error", err)
		text = unscreenedReport
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if active.cancelled || ctx.Err() != nil {
		return nil
	}
	return s.keepReport(ctx, child.ID, text, active)
}

func (s *Service) hasReported(active *execution) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return active.reported
}

// Report records the report of the child session's current assignment. The
// child is still running when it reports; its run then ends on its own, and
// the child becomes idle.
func (s *Service) Report(ctx context.Context, sessionID string, r Report) error {
	if err := r.validate(); err != nil {
		return err
	}
	text, err := s.cleanReport(ctx, sessionID, r.Text())
	if err != nil {
		return fmt.Errorf("report could not be screened: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	active := s.active[sessionID]
	if active == nil || active.cancelled {
		return fmt.Errorf("subagent has no active assignment to report on")
	}
	return s.keepReport(ctx, sessionID, text, active)
}

// cleanReport cuts text at maxResultBytes, at a character boundary, and
// screens exactly what will be stored, outside the lock: the guard may be slow.
func (s *Service) cleanReport(ctx context.Context, id, text string) (string, error) {
	if len(text) > maxResultBytes {
		text = strings.ToValidUTF8(text[:maxResultBytes], "")
	}
	if s.screen == nil {
		return text, nil
	}
	return s.screen(ctx, id, text)
}

// keepReport stores text, already cleaned, as the child's latest report.
// Callers hold mu.
func (s *Service) keepReport(ctx context.Context, id, text string, active *execution) error {
	child, err := s.store.SetSubagentResult(ctx, id, text)
	if err != nil {
		return err
	}
	active.reported = true
	s.updated(ctx, child)
	return nil
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

func (s *Service) reportUpdate(ctx context.Context, child store.Session) {
	e, err := event.New(event.TypeSessionUpdated, event.TopicGlobal, event.SessionUpdated{SessionID: child.ID, ComputerID: child.ComputerID, ReportID: child.ReportNoticeID})
	if err == nil {
		s.events.Emit(ctx, e)
	}
}
