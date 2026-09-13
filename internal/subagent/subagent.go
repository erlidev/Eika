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

// name is the shape a subagent name and a branch suffix may have. It is
// stricter than git's own rules on purpose: the name reaches a branch name, a
// session title, and an event, so it holds nothing that needs escaping
// anywhere. Host.CloneAt checks the branch it builds with git itself.
var name = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

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
	Push(ctx context.Context, ws workspace.Workspace, project, branch string, force bool) error
	Executor(ws workspace.Workspace) (executor.Executor, error)
}

// Options configure a Spawner.
type Options struct {
	Store      Store
	Workspaces Workspaces
	// Emitter receives subagent.started and subagent.finished. Nil drops
	// them.
	Emitter event.Emitter
	// MaxDepth is how many levels of children a root session may have below
	// it. Zero means one.
	MaxDepth int
	// MaxChildren is how many children of one session may run at a time.
	// Zero means one.
	MaxChildren int
	// Logger receives one line per spawned and finished child.
	Logger *slog.Logger
}

// Spawner runs child agents for the sessions of one harness.
type Spawner struct {
	opts Options

	// mu guards the runner and the children that are going right now.
	mu       sync.Mutex
	runner   Runner
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
	opts.MaxDepth = max(opts.MaxDepth, 1)
	opts.MaxChildren = max(opts.MaxChildren, 1)
	return &Spawner{opts: opts, children: make(map[string]*child)}
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
	if !name.MatchString(candidate) {
		return fmt.Errorf("%w: %q", ErrBadName, candidate)
	}
	return nil
}

// childBranch is the branch a child works on: its parent's branch and its own
// name, joined with a dash. It is not a path under the parent's branch, which
// git cannot store: a repository holds either refs/heads/main or
// refs/heads/main/<name>, never both.
func childBranch(parentBranch, suffix string) string {
	if parentBranch == "" {
		return suffix
	}
	return parentBranch + "-" + suffix
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
