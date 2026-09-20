package subagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
	"github.com/erlidev/eika/internal/workspace"
)

// namePattern is the shape a subagent name and a branch suffix may have. It
// is stricter than git's own rules on purpose: the name reaches a branch
// name, a session title, and an event, so it holds nothing that needs
// escaping anywhere. Host.CloneAt checks the branch it builds with git itself.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

// Errors the spawner reports to the model that asked for a child.
var (
	// ErrBadName reports a subagent name that cannot become a branch name.
	ErrBadName = errors.New("invalid subagent name")
	// ErrTooDeep reports a child that would sit deeper than the configured
	// limit allows.
	ErrTooDeep = errors.New("subagent tree is too deep")
	// ErrTooMany reports that a session already runs as many children as the
	// configuration allows.
	ErrTooMany = errors.New("too many subagents are already running")
	// ErrNoRunner reports a spawner that was never given a way to run a
	// child, which is a harness that is not wired.
	ErrNoRunner = errors.New("no runner is attached")
	// ErrNotChild reports an agent id that is not a child of the session
	// asking about it.
	ErrNotChild = errors.New("not a child of this session")
	// ErrStopped reports a spawn asked for after the harness began shutting
	// down, when nothing new may start.
	ErrStopped = errors.New("the harness is shutting down")
)

// Runner runs the agent loop on a child session and returns when it has
// finished. The server's run manager is the one implementation: a child run
// is an ordinary run, with the same tools, events, and entries as any other.
type Runner interface {
	RunChild(ctx context.Context, sessionID, message, model string) error
}

// Store is the part of the database the spawner uses.
type Store interface {
	Session(ctx context.Context, id string) (store.Session, error)
	SessionPath(ctx context.Context, sessionID string) ([]store.Entry, error)
	CreateSession(ctx context.Context, sess store.Session) (store.Session, error)
	Workspace(ctx context.Context, id string) (store.Workspace, error)
	CreateWorkspace(ctx context.Context, ws store.Workspace) (store.Workspace, error)
	DeleteWorkspace(ctx context.Context, id string) error
	SetWorkspaceState(ctx context.Context, id, state, containerID string) error
	Project(ctx context.Context, id string) (store.Project, error)
	StartSubagent(ctx context.Context, sub store.Subagent) (store.Subagent, error)
	FinishSubagent(ctx context.Context, id string, state store.RunState, result string) error
	Subagent(ctx context.Context, id string) (store.Subagent, error)
	Subagents(ctx context.Context, parentSessionID string) ([]store.Subagent, error)
	SubagentOfSession(ctx context.Context, childSessionID string) (store.Subagent, error)
}

// Workspaces is the part of the workspace host the spawner uses: a child gets
// a sandbox of its own, cloned from the hub, and the parent pushes to the hub
// before it does.
type Workspaces interface {
	Create(ctx context.Context, spec workspace.Spec) (workspace.Workspace, error)
	Start(ctx context.Context, ws *workspace.Workspace) error
	Stop(ctx context.Context, ws *workspace.Workspace) error
	Destroy(ctx context.Context, ws *workspace.Workspace) error
	Inspect(ctx context.Context, id string) (workspace.Workspace, error)
	CloneAt(ctx context.Context, ws workspace.Workspace, project, branch, commit string) (string, error)
	Push(ctx context.Context, ws workspace.Workspace, project, branch string) error
	Executor(ws workspace.Workspace) (executor.Executor, error)
}

// Options configure a Spawner.
type Options struct {
	Store      Store
	Workspaces Workspaces
	// Emitter receives subagent.started and subagent.finished. Nil drops
	// them.
	Emitter event.Emitter
	// Limits returns the bounds a spawn is checked against. It is called at
	// every spawn, so a change the user makes in the settings applies to the
	// next child. Nil, or a bound below one, means one.
	Limits func(ctx context.Context) Limits
	// Logger receives one line per spawned and finished child.
	Logger *slog.Logger
}

// Limits bounds the tree of children a session may grow.
type Limits struct {
	// MaxDepth is how many levels of children a root session may have below
	// it, so 2 allows a child and a grandchild.
	MaxDepth int
	// MaxChildren is how many children of one session may run at a time.
	MaxChildren int
}

// Spawner runs child agents for the sessions of one harness.
type Spawner struct {
	opts Options

	// mu guards the runner, the shutdown flag, and the children that are
	// going right now, reservations included. Claiming a child's slot under
	// the same lock that counts them is what makes the limit true: two
	// concurrent spawns would otherwise both get past a count taken before
	// the slow work of creating a sandbox.
	mu       sync.Mutex
	runner   Runner
	stopped  bool
	children map[string]*child
}

// New returns a Spawner. Attach gives it the runner before it is used.
func New(opts Options) *Spawner {
	if opts.Emitter == nil {
		opts.Emitter = event.Discard
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Spawner{opts: opts, children: make(map[string]*child)}
}

// limits returns the bounds for a spawn happening now, never below one.
func (s *Spawner) limits(ctx context.Context) Limits {
	var l Limits
	if s.opts.Limits != nil {
		l = s.opts.Limits(ctx)
	}
	return Limits{MaxDepth: max(l.MaxDepth, 1), MaxChildren: max(l.MaxChildren, 1)}
}

// Attach gives the spawner the runner that drives a child's session. It is
// called once while the harness is wired and before anything serves: the
// runner cannot exist before the spawner, because the tool registry every run
// uses holds the spawn_agent tool this spawner backs.
func (s *Spawner) Attach(r Runner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runner = r
}

// Spawn starts a child and returns when it has finished. A cancelled ctx
// stops waiting; the child is stopped by an abort, not by the caller giving
// up, so that a parent turn that ends does not throw away work in progress.
func (s *Spawner) Spawn(ctx context.Context, req builtin.SpawnRequest) (builtin.AgentResult, error) {
	c, err := s.start(ctx, req)
	if err != nil {
		return builtin.AgentResult{}, err
	}
	return s.await(ctx, c)
}

// Start starts a child and returns as soon as it is running, so that a parent
// can start several and wait for them together.
func (s *Spawner) Start(ctx context.Context, req builtin.SpawnRequest) (builtin.AgentResult, error) {
	c, err := s.start(ctx, req)
	if err != nil {
		return builtin.AgentResult{}, err
	}
	return c.snapshot(), nil
}

// Wait returns when each named child of the session has finished. An id that
// names something other than a child of this session is an error, so a run
// cannot wait on another session's work.
func (s *Spawner) Wait(ctx context.Context, parentSessionID string, ids []string) ([]builtin.AgentResult, error) {
	out := make([]builtin.AgentResult, 0, len(ids))
	for _, id := range ids {
		result, err := s.waitOne(ctx, parentSessionID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, nil
}

// List reports the children of a session, the ones still running included.
func (s *Spawner) List(ctx context.Context, parentSessionID string) ([]builtin.AgentResult, error) {
	rows, err := s.opts.Store.Subagents(ctx, parentSessionID)
	if err != nil {
		return nil, err
	}
	out := make([]builtin.AgentResult, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.resultOf(row))
	}
	return out, nil
}

// Abort stops one child. It returns once the child has recorded how it ended,
// so that the row the caller reads next is the final one.
func (s *Spawner) Abort(ctx context.Context, id string) error {
	s.mu.Lock()
	c := s.children[id]
	s.mu.Unlock()
	if c == nil {
		row, err := s.opts.Store.Subagent(ctx, id)
		if err != nil {
			return err
		}
		if row.State != store.RunRunning {
			return fmt.Errorf("abort subagent %s: it is already %s", id, row.State)
		}
		// A row left running by a harness restart has no goroutine to stop.
		return s.opts.Store.FinishSubagent(ctx, id, store.RunAborted, "")
	}
	c.abort()
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AbortChildren stops every child of a session. Aborting a run aborts the
// children it spawned: they work on a branch of the run that asked for them
// and have nobody left to report to.
func (s *Spawner) AbortChildren(parentSessionID string) {
	s.mu.Lock()
	var stopping []*child
	for _, c := range s.children {
		if c.parentSessionID == parentSessionID {
			stopping = append(stopping, c)
		}
	}
	s.mu.Unlock()
	for _, c := range stopping {
		c.abort()
	}
}

// Shutdown stops every child and returns once they have all recorded how
// they ended, which is what a harness does before its database pool closes. A
// child whose parent's run is long over is still running here, so aborting
// the runs is not enough. Nothing spawns after it.
func (s *Spawner) Shutdown(ctx context.Context) {
	s.mu.Lock()
	s.stopped = true
	stopping := make([]*child, 0, len(s.children))
	for _, c := range s.children {
		stopping = append(stopping, c)
	}
	s.mu.Unlock()
	for _, c := range stopping {
		c.abort()
	}
	for _, c := range stopping {
		select {
		case <-c.done:
		case <-ctx.Done():
			s.opts.Logger.Warn("subagent did not stop before shutdown gave up", "subagent_id", c.id)
			return
		}
	}
}

// await waits for one child, reporting what it became.
func (s *Spawner) await(ctx context.Context, c *child) (builtin.AgentResult, error) {
	select {
	case <-c.done:
		return c.snapshot(), nil
	case <-ctx.Done():
		return builtin.AgentResult{}, ctx.Err()
	}
}

// waitOne waits for one child of a session by id, whether it is still running
// here or finished before the call.
func (s *Spawner) waitOne(ctx context.Context, parentSessionID, id string) (builtin.AgentResult, error) {
	s.mu.Lock()
	c := s.children[id]
	s.mu.Unlock()
	if c != nil {
		if c.parentSessionID != parentSessionID {
			return builtin.AgentResult{}, fmt.Errorf("wait for subagent %s: %w", id, ErrNotChild)
		}
		return s.await(ctx, c)
	}
	row, err := s.opts.Store.Subagent(ctx, id)
	if err != nil {
		return builtin.AgentResult{}, err
	}
	if row.ParentSessionID != parentSessionID {
		return builtin.AgentResult{}, fmt.Errorf("wait for subagent %s: %w", id, ErrNotChild)
	}
	return s.resultOf(row), nil
}

// resultOf renders a stored row, preferring the live child when the row is
// one this harness is running.
func (s *Spawner) resultOf(row store.Subagent) builtin.AgentResult {
	s.mu.Lock()
	c := s.children[row.ID]
	s.mu.Unlock()
	if c != nil {
		return c.snapshot()
	}
	result := builtin.AgentResult{
		ID:          row.ID,
		SessionID:   row.ChildSessionID,
		WorkspaceID: row.ChildWorkspaceID,
		State:       string(row.State),
	}
	if row.Result != "" {
		stored, err := decodeResult(row.Result)
		if err == nil {
			stored.ID, stored.SessionID, stored.WorkspaceID = result.ID, result.SessionID, result.WorkspaceID
			stored.State = result.State
			return stored
		}
		s.opts.Logger.Error("decode subagent result", "subagent_id", row.ID, "error", err)
		result.Error = row.Result
	}
	return result
}

// runnerOf returns the attached runner.
func (s *Spawner) runnerOf() Runner {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runner
}

// checkName rejects a subagent name that cannot safely become a branch name.
func checkName(candidate string) error {
	if !namePattern.MatchString(candidate) {
		return fmt.Errorf("%w: %q", ErrBadName, candidate)
	}
	return nil
}

// branchTag is how many characters of a subagent's id end its branch name.
// Six of twenty base32 characters are 30 bits, which is more than enough to
// keep the children of one parent apart.
const branchTag = 6

// childBranch is the branch a child works on: its parent's branch, its own
// name, and a tag from its id, joined with dashes. It is not a path under the
// parent's branch, which git cannot store: a repository holds either
// refs/heads/main or refs/heads/main/<name>, never both. The tag is what
// makes it the child's own: two children a parent gave the same name to would
// otherwise write over each other in the hub.
func childBranch(parentBranch, suffix, id string) string {
	name := suffix + "-" + id[:min(branchTag, len(id))]
	if parentBranch == "" {
		return name
	}
	return parentBranch + "-" + name
}

// describe says what a child did when it left no closing message of its own,
// so that the parent's model has something to act on rather than a blank
// report.
func describe(r builtin.AgentResult) string {
	if r.DiffStat != "" {
		return "the child ended " + r.State + " without a closing message; it changed:\n" + r.DiffStat
	}
	return "the child ended " + r.State + " without a closing message and changed nothing"
}

// summaryOf returns the child's last assistant message, which is what it
// reports back to its parent.
func summaryOf(entries []store.Entry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		m, ok, err := session.Message(entries[i])
		if err != nil || !ok {
			continue
		}
		if m.Role == provider.RoleAssistant && strings.TrimSpace(m.Content) != "" {
			return strings.TrimSpace(m.Content)
		}
	}
	return ""
}
