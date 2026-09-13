package subagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
	"github.com/erlidev/eika/internal/workspace"
)

// gitTimeout bounds one git command the spawner runs in a workspace.
const gitTimeout = 60 * time.Second

// reportTimeout bounds the work that happens after a child's run: committing
// what it left in the tree, pushing its branch, and recording the result. It
// runs on a context of its own, because an aborted child has to report what
// it did just as much as one that finished.
const reportTimeout = 5 * time.Minute

// child is one running subagent: the rows it owns, the goroutine driving its
// run, and what it has become so far.
type child struct {
	id              string
	parentSessionID string
	name            string

	cancel context.CancelFunc
	done   chan struct{}

	// mu guards the result the run fills in and the abort flag that tells a
	// cancelled run from a failed one.
	mu      sync.Mutex
	result  builtin.AgentResult
	aborted bool
}

// snapshot returns what the child is right now.
func (c *child) snapshot() builtin.AgentResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.result
}

// update records what became of the child.
func (c *child) update(f func(r *builtin.AgentResult)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(&c.result)
}

// abort stops the child's run and records that the stop was asked for.
func (c *child) abort() {
	c.mu.Lock()
	c.aborted = true
	c.mu.Unlock()
	c.cancel()
}

// wasAborted reports whether the child was stopped on purpose.
func (c *child) wasAborted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.aborted
}

// start does everything up to and including starting the child's run: it
// commits and pushes the parent's work, clones a child workspace from the hub
// at that commit, opens the child's session, and records the subagent.
func (s *Spawner) start(ctx context.Context, req builtin.SpawnRequest) (*child, error) {
	runner := s.runnerOf()
	if runner == nil {
		return nil, ErrNoRunner
	}
	if err := checkName(req.Name); err != nil {
		return nil, err
	}
	suffix := req.BranchSuffix
	if suffix == "" {
		suffix = req.Name
	}
	if err := checkName(suffix); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Task) == "" {
		return nil, fmt.Errorf("spawn subagent %s: task is empty", req.Name)
	}

	parent, err := s.opts.Store.Session(ctx, req.ParentSessionID)
	if err != nil {
		return nil, err
	}
	parentWS, err := s.opts.Store.Workspace(ctx, parent.WorkspaceID)
	if err != nil {
		return nil, err
	}
	project, err := s.opts.Store.Project(ctx, parentWS.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.checkLimits(ctx, parent.ID); err != nil {
		return nil, err
	}

	base, err := s.handOver(ctx, parentWS, project, req.Name)
	if err != nil {
		return nil, err
	}
	branch := childBranch(parentWS.Branch, suffix)

	host, err := s.createChild(ctx, project, branch, base, req.Image)
	if err != nil {
		return nil, err
	}
	childWS, err := s.opts.Store.CreateWorkspace(ctx, store.Workspace{
		ID:                host.ID,
		ProjectID:         project.ID,
		Name:              req.Name,
		Branch:            branch,
		BaseCommit:        base,
		Image:             host.Image,
		State:             string(host.State),
		ContainerID:       host.ContainerID,
		ParentWorkspaceID: parentWS.ID,
	})
	if err != nil {
		s.discard(ctx, host)
		return nil, err
	}
	// From here the child's sandbox has a row of its own, so a failure has
	// both to remove.
	sess, err := s.opts.Store.CreateSession(ctx, store.Session{
		WorkspaceID:     childWS.ID,
		Title:           req.Name,
		ParentSessionID: parent.ID,
	})
	if err != nil {
		s.discardRecorded(ctx, host)
		return nil, err
	}
	row, err := s.opts.Store.StartSubagent(ctx, store.Subagent{
		ParentSessionID:  parent.ID,
		ChildSessionID:   sess.ID,
		ChildWorkspaceID: childWS.ID,
	})
	if err != nil {
		s.discardRecorded(ctx, host)
		return nil, err
	}

	// The child outlives the tool call that asked for it: a parent that
	// spawns without waiting keeps working, and an abort is what stops a
	// child, not the end of the call.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c := &child{
		id:              row.ID,
		parentSessionID: parent.ID,
		name:            req.Name,
		cancel:          cancel,
		done:            make(chan struct{}),
		result: builtin.AgentResult{
			ID:          row.ID,
			SessionID:   sess.ID,
			WorkspaceID: childWS.ID,
			Name:        req.Name,
			Branch:      branch,
			State:       string(store.RunRunning),
		},
	}
	s.mu.Lock()
	s.children[c.id] = c
	s.mu.Unlock()

	s.emit(ctx, event.TypeSubagentStarted, event.SessionTopic(parent.ID), event.SubagentStarted{
		SubagentID:       c.id,
		ParentSessionID:  parent.ID,
		ChildSessionID:   sess.ID,
		ChildWorkspaceID: childWS.ID,
		Name:             req.Name,
		Branch:           branch,
		BaseCommit:       base,
		Task:             req.Task,
	})
	s.opts.Logger.Info("subagent started", "subagent_id", c.id, "parent_session_id", parent.ID,
		"child_session_id", sess.ID, "workspace_id", childWS.ID, "branch", branch)

	go s.run(runCtx, c, runner, req, base, project.Name)
	return c, nil
}

// run drives the child's session and reports what it did, whatever happened.
func (s *Spawner) run(ctx context.Context, c *child, runner Runner, req builtin.SpawnRequest, base, project string) {
	defer close(c.done)
	defer c.cancel()
	result := c.snapshot()
	runErr := runner.RunChild(ctx, result.SessionID, req.Task, req.Model)

	state := store.RunDone
	message := ""
	switch {
	case runErr != nil && c.wasAborted():
		state, message = store.RunAborted, runErr.Error()
	case runErr != nil:
		state, message = store.RunError, runErr.Error()
	}

	// The run may have ended because its context was cancelled, so the report
	// runs on one of its own: a child that was stopped still leaves its work
	// on a branch the user can look at.
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportTimeout)
	defer cancel()
	result.State, result.Error = string(state), message
	s.report(reportCtx, &result, base, project, req.Name)
	c.update(func(r *builtin.AgentResult) { *r = result })

	encoded, err := json.Marshal(result)
	if err != nil {
		s.opts.Logger.Error("encode subagent result", "subagent_id", c.id, "error", err)
	}
	if err := s.opts.Store.FinishSubagent(reportCtx, c.id, state, string(encoded)); err != nil {
		s.opts.Logger.Error("record finished subagent", "subagent_id", c.id, "error", err)
	}
	s.mu.Lock()
	delete(s.children, c.id)
	s.mu.Unlock()

	s.emit(reportCtx, event.TypeSubagentFinished, event.SessionTopic(c.parentSessionID), event.SubagentFinished{
		SubagentID:       c.id,
		ParentSessionID:  c.parentSessionID,
		ChildSessionID:   result.SessionID,
		ChildWorkspaceID: result.WorkspaceID,
		Name:             result.Name,
		Branch:           result.Branch,
		State:            result.State,
		Commit:           result.Commit,
		Summary:          result.Summary,
		DiffStat:         result.DiffStat,
		Error:            result.Error,
	})
	s.opts.Logger.Info("subagent finished", "subagent_id", c.id, "state", result.State,
		"branch", result.Branch, "commit", result.Commit)
}

// report turns what the child left behind into its result: everything in the
// tree is committed, the branch goes to the hub, and the parent gets the
// child's last message and a diffstat against the commit it started from. A
// step that fails is recorded in the result rather than dropped: the parent
// has to hear what happened even when the child's workspace is unreachable.
func (s *Spawner) report(ctx context.Context, result *builtin.AgentResult, base, project, name string) {
	host, err := s.opts.Workspaces.Inspect(ctx, result.WorkspaceID)
	if err != nil {
		result.Error = join(result.Error, err.Error())
		return
	}
	ex, err := s.opts.Workspaces.Executor(host)
	if err != nil {
		result.Error = join(result.Error, err.Error())
		return
	}
	if _, err := commitAll(ctx, ex, "wip: subagent "+name+" finished"); err != nil {
		result.Error = join(result.Error, err.Error())
	}
	if head, err := git(ctx, ex, "rev-parse", "HEAD"); err == nil {
		result.Commit = head
	}
	if result.Commit != "" {
		// A child owns its branch, so its own history is what the hub keeps.
		if err := s.opts.Workspaces.Push(ctx, host, project, result.Branch, true); err != nil {
			result.Error = join(result.Error, err.Error())
		}
	}
	if base != "" && result.Commit != "" && base != result.Commit {
		if stat, err := git(ctx, ex, "diff", "--stat", base, result.Commit); err == nil {
			result.DiffStat = stat
		}
	}
	entries, err := s.opts.Store.SessionPath(ctx, result.SessionID)
	if err != nil {
		result.Error = join(result.Error, err.Error())
	} else {
		result.Summary = summaryOf(entries)
	}

	// The container stops but nothing is destroyed: the user opens a finished
	// child's workspace to see what it did, and starting it again is a click.
	if err := s.opts.Workspaces.Stop(ctx, &host); err != nil {
		s.opts.Logger.Error("stop subagent workspace", "workspace_id", host.ID, "error", err)
		return
	}
	if err := s.opts.Store.SetWorkspaceState(ctx, host.ID, string(host.State), host.ContainerID); err != nil {
		s.opts.Logger.Error("record subagent workspace state", "workspace_id", host.ID, "error", err)
		return
	}
	s.emit(ctx, event.TypeWorkspaceState, event.WorkspaceTopic(host.ID), event.WorkspaceState{
		WorkspaceID: host.ID,
		State:       string(host.State),
	})
}

// handOver commits whatever the parent has in its tree and pushes its branch
// to the hub, so that the child can clone the parent's work. It returns the
// commit the child starts from.
func (s *Spawner) handOver(ctx context.Context, parent store.Workspace, project store.Project, name string) (string, error) {
	host, err := s.opts.Workspaces.Inspect(ctx, parent.ID)
	if err != nil {
		return "", err
	}
	ex, err := s.opts.Workspaces.Executor(host)
	if err != nil {
		return "", err
	}
	if _, err := commitAll(ctx, ex, "wip: before spawning "+name); err != nil {
		return "", err
	}
	head, err := git(ctx, ex, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read the parent workspace's commit: %w", err)
	}
	// A local project is pushed as well: the hub mirrors both kinds, so a
	// child clones the same way whatever its project is.
	if err := s.opts.Workspaces.Push(ctx, host, project.Name, parent.Branch, false); err != nil {
		return "", err
	}
	return head, nil
}

// createChild builds the child's sandbox and puts the parent's commit in it
// on the child's own branch. Nothing it created survives a failure.
func (s *Spawner) createChild(ctx context.Context, project store.Project, branch, base, image string) (workspace.Workspace, error) {
	// A child never bind-mounts the host directory of a local project: it
	// works on a clone of its own, which is what makes parallel children
	// possible at all.
	host, err := s.opts.Workspaces.Create(ctx, workspace.Spec{
		ID:      store.NewID(),
		Image:   image,
		Project: project.Name,
	})
	if err != nil {
		return workspace.Workspace{}, err
	}
	if err := s.opts.Workspaces.Start(ctx, &host); err != nil {
		s.discard(ctx, host)
		return workspace.Workspace{}, err
	}
	if _, err := s.opts.Workspaces.CloneAt(ctx, host, project.Name, branch, base); err != nil {
		s.discard(ctx, host)
		return workspace.Workspace{}, err
	}
	return host, nil
}

// discard removes a child sandbox whose creation did not finish. It runs on a
// context of its own, so that the container and its volume go away even when
// the request that asked for the child is gone.
func (s *Spawner) discard(ctx context.Context, host workspace.Workspace) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportTimeout)
	defer cancel()
	if err := s.opts.Workspaces.Destroy(ctx, &host); err != nil {
		s.opts.Logger.Error("discard half-created subagent workspace", "workspace_id", host.ID, "error", err)
	}
}

// discardRecorded removes a child sandbox that already has a row, which is
// what a spawn that failed after the workspace was recorded leaves behind.
// Deleting the row takes its sessions and entries with it.
func (s *Spawner) discardRecorded(ctx context.Context, host workspace.Workspace) {
	s.discard(ctx, host)
	remove, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportTimeout)
	defer cancel()
	if err := s.opts.Store.DeleteWorkspace(remove, host.ID); err != nil {
		s.opts.Logger.Error("remove half-created subagent workspace row", "workspace_id", host.ID, "error", err)
	}
}

// checkLimits rejects a child that would make the tree deeper or wider than
// the configuration allows.
func (s *Spawner) checkLimits(ctx context.Context, parentSessionID string) error {
	depth, err := s.depthOf(ctx, parentSessionID)
	if err != nil {
		return err
	}
	if depth+1 > s.opts.MaxDepth {
		return fmt.Errorf("%w: %d levels are allowed", ErrTooDeep, s.opts.MaxDepth)
	}
	running, err := s.runningChildren(ctx, parentSessionID)
	if err != nil {
		return err
	}
	if running >= s.opts.MaxChildren {
		return fmt.Errorf("%w: %d at a time are allowed", ErrTooMany, s.opts.MaxChildren)
	}
	return nil
}

// depthOf reports how many parents a session has above it, following the
// subagent rows up to the session a user started.
func (s *Spawner) depthOf(ctx context.Context, sessionID string) (int, error) {
	depth := 0
	for id := sessionID; depth <= s.opts.MaxDepth; depth++ {
		row, err := s.opts.Store.SubagentOfSession(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return depth, nil
		}
		if err != nil {
			return 0, err
		}
		id = row.ParentSessionID
	}
	return depth, nil
}

// runningChildren counts the children of a session that have not finished.
// The rows are the record, and the children this harness is running are the
// ones a row cannot have caught up with yet.
func (s *Spawner) runningChildren(ctx context.Context, parentSessionID string) (int, error) {
	rows, err := s.opts.Store.Subagents(ctx, parentSessionID)
	if err != nil {
		return 0, err
	}
	recorded := 0
	for _, row := range rows {
		if row.State == store.RunRunning {
			recorded++
		}
	}
	s.mu.Lock()
	live := 0
	for _, c := range s.children {
		if c.parentSessionID == parentSessionID {
			live++
		}
	}
	s.mu.Unlock()
	return max(recorded, live), nil
}

// emit publishes one event, logging an encoding failure rather than failing
// the spawn it belongs to.
func (s *Spawner) emit(ctx context.Context, typ, topic string, payload any) {
	e, err := event.New(typ, topic, payload)
	if err != nil {
		s.opts.Logger.Error("encode event", "type", typ, "error", err)
		return
	}
	s.opts.Emitter.Emit(ctx, e)
}

// decodeResult reads the result stored on a subagent row.
func decodeResult(raw string) (builtin.AgentResult, error) {
	var result builtin.AgentResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return builtin.AgentResult{}, fmt.Errorf("decode subagent result: %w", err)
	}
	return result, nil
}

// commitAll commits everything in a workspace's tree, reporting whether there
// was anything to commit. A clean tree is left alone.
func commitAll(ctx context.Context, ex executor.Executor, message string) (bool, error) {
	status, err := git(ctx, ex, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) == "" {
		return false, nil
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", message}} {
		if _, err := git(ctx, ex, args...); err != nil {
			return false, err
		}
	}
	return true, nil
}

// git runs one git command in a workspace and returns its standard output.
func git(ctx context.Context, ex executor.Executor, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	res, err := ex.Exec(ctx, executor.ExecSpec{
		Command: "git",
		Args:    args,
		Timeout: gitTimeout,
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("run git %s: %w", args[0], err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("run git %s: exit %d: %s", args[0], res.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// join appends a message to the errors a result already carries.
func join(existing, message string) string {
	if existing == "" {
		return message
	}
	return existing + "; " + message
}
