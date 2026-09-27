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

// begin records what the child turned out to be once its rows exist.
func (c *child) begin(result builtin.AgentResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.result = result
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
// claims the child's slot, commits the parent's work, adds a worktree for the
// child at that commit in the parent's container, opens the child's session,
// and records the subagent.
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
	// The child's id exists before its row does, because its branch is named
	// after it and the branch is what the worktree needs.
	c, runCtx, err := s.reserve(ctx, parent.ID, store.NewID(), req.Name)
	if err != nil {
		return nil, err
	}
	// Building a child takes a commit and a worktree. Until its run is
	// going, the reservation is what holds its slot, and every way out of
	// here releases it.
	started := false
	defer func() {
		if !started {
			s.release(c)
		}
	}()
	branch := childBranch(parentWS.Branch, suffix, c.id)

	parentHost, err := workspace.Locate(ctx, s.opts.Workspaces, parentWS.ID, parentWS.WorktreeOf)
	if err != nil {
		return nil, err
	}
	base, err := s.handOver(ctx, parentHost, req.Name)
	if err != nil {
		return nil, err
	}
	childWS, err := s.addChild(ctx, parentWS, parentHost, req.Name, branch, base)
	if err != nil {
		return nil, err
	}
	// From here the child has a worktree and a row, so a failure has both to
	// remove. The child runs as its parent is configured to: the same
	// profile, with the parent's overrides of it.
	sess, err := s.opts.Store.CreateSession(ctx, store.Session{
		WorkspaceID:     childWS.ID,
		Title:           req.Name,
		Kind:            store.SessionAgent,
		ParentSessionID: parent.ID,
		ProfileID:       parent.ProfileID,
		Overrides:       parent.Overrides,
	})
	if err != nil {
		s.discard(ctx, parentHost, childWS.ID)
		return nil, err
	}
	row, err := s.opts.Store.StartSubagent(ctx, store.Subagent{
		ID:               c.id,
		ParentSessionID:  parent.ID,
		ChildSessionID:   sess.ID,
		ChildWorkspaceID: childWS.ID,
	})
	if err != nil {
		s.discard(ctx, parentHost, childWS.ID)
		return nil, err
	}

	// The child outlives the tool call that asked for it: a parent that
	// spawns without waiting keeps working, and an abort is what stops a
	// child, not the end of the call.
	c.begin(builtin.AgentResult{
		ID:          row.ID,
		SessionID:   sess.ID,
		WorkspaceID: childWS.ID,
		Name:        req.Name,
		Branch:      branch,
		State:       string(store.RunRunning),
	})
	started = true

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
		"child_session_id", sess.ID, "workspace_id", childWS.ID, "holder_id", childWS.WorktreeOf, "branch", branch)

	go s.run(runCtx, c, runner, req, base, project.Name)
	return c, nil
}

// reserve claims one child's slot on a parent session, so that the depth and
// width limits hold for spawns that arrive at the same time. The counting and
// the claim happen under one lock; building the child afterwards is slow
// enough that a check without a claim would let every concurrent spawn
// through.
// It returns the reservation and the context the child's run is bound to,
// which exists from here so that an abort or a shutdown arriving while the
// child is still being built still stops it.
func (s *Spawner) reserve(ctx context.Context, parentSessionID, id, name string) (*child, context.Context, error) {
	limits := s.limits(ctx)
	depth, err := s.depthOf(ctx, parentSessionID, limits.MaxDepth)
	if err != nil {
		return nil, nil, err
	}
	if depth+1 > limits.MaxDepth {
		return nil, nil, fmt.Errorf("%w: %d levels are allowed", ErrTooDeep, limits.MaxDepth)
	}
	// Rows are the record of children this harness is no longer running; the
	// reservations below are the ones no row has caught up with yet.
	recorded, err := s.recordedChildren(ctx, parentSessionID)
	if err != nil {
		return nil, nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil, nil, ErrStopped
	}
	live := 0
	for _, c := range s.children {
		if c.parentSessionID == parentSessionID {
			live++
		}
	}
	if max(recorded, live) >= limits.MaxChildren {
		return nil, nil, fmt.Errorf("%w: %d at a time are allowed", ErrTooMany, limits.MaxChildren)
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c := &child{
		id:              id,
		parentSessionID: parentSessionID,
		name:            name,
		cancel:          cancel,
		done:            make(chan struct{}),
		result: builtin.AgentResult{
			ID:    id,
			Name:  name,
			State: string(store.RunRunning),
		},
	}
	s.children[id] = c
	return c, runCtx, nil
}

// release gives up a reservation whose child never started, which frees the
// slot and releases whoever is waiting on it.
func (s *Spawner) release(c *child) {
	s.forget(c)
	c.cancel()
	close(c.done)
}

// forget drops a child that is over.
func (s *Spawner) forget(c *child) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.children[c.id]; ok && current == c {
		delete(s.children, c.id)
	}
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
	s.report(reportCtx, &result, base, project)
	c.update(func(r *builtin.AgentResult) { *r = result })

	encoded, err := json.Marshal(result)
	if err != nil {
		s.opts.Logger.Error("encode subagent result", "subagent_id", c.id, "error", err)
	}
	if err := s.opts.Store.FinishSubagent(reportCtx, c.id, state, string(encoded)); err != nil {
		s.opts.Logger.Error("record finished subagent", "subagent_id", c.id, "error", err)
	}
	s.forget(c)

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

// report turns what the child left behind into its result: its work, and
// the child's last message, or a description of how it ended when it left
// none.
func (s *Spawner) report(ctx context.Context, result *builtin.AgentResult, base, project string) {
	s.keepWork(ctx, result, base, project)
	entries, err := s.opts.Store.SessionPath(ctx, result.SessionID)
	if err != nil {
		result.Error = join(result.Error, err.Error())
	} else {
		result.Summary = summaryOf(entries)
	}
	if result.Summary == "" {
		result.Summary = describe(*result)
	}
}

// keepWork commits everything in the child's tree, pushes its branch to the
// hub, and records the commit and a diffstat against the commit the child
// started from. A step that fails is recorded in the result rather than
// dropped: the parent has to hear what happened even when the child's
// workspace is unreachable.
//
// The worktree stays: the user opens a finished child's workspace to see
// what it did, and the branch is already in the parent's repository.
func (s *Spawner) keepWork(ctx context.Context, result *builtin.AgentResult, base, project string) {
	row, err := s.opts.Store.Workspace(ctx, result.WorkspaceID)
	if err != nil {
		result.Error = join(result.Error, err.Error())
		return
	}
	host, err := workspace.Locate(ctx, s.opts.Workspaces, row.ID, row.WorktreeOf)
	if err != nil {
		result.Error = join(result.Error, err.Error())
		return
	}
	ex, err := s.opts.Workspaces.Executor(host)
	if err != nil {
		result.Error = join(result.Error, err.Error())
		return
	}
	if _, err := commitAll(ctx, ex, "wip: subagent "+result.Name+" finished"); err != nil {
		result.Error = join(result.Error, err.Error())
	}
	if head, err := git(ctx, ex, "rev-parse", "HEAD"); err == nil {
		result.Commit = head
	}
	if result.Commit != "" {
		// The branch is the child's own, tagged with its id, so nothing is
		// there to overwrite and the push never has to force.
		if err := s.opts.Workspaces.Push(ctx, host, project, result.Branch); err != nil {
			result.Error = join(result.Error, err.Error())
		}
	}
	if base != "" && result.Commit != "" && base != result.Commit {
		if stat, err := git(ctx, ex, "diff", "--stat", base, result.Commit); err == nil {
			result.DiffStat = stat
		}
	}
}

// handOver commits whatever the parent has in its tree, so that the child
// starts from the parent's work, and returns that commit. The child's
// worktree shares the parent's repository, so nothing has to travel through
// the hub first.
func (s *Spawner) handOver(ctx context.Context, parent workspace.Workspace, name string) (string, error) {
	ex, err := s.opts.Workspaces.Executor(parent)
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
	return head, nil
}

// addChild gives the child a workspace: a worktree on its own branch at base,
// in the container its parent's files are in, and the row that records it.
// A child of a worktree workspace shares the same holder, so every agent in a
// tree works in the one container its root session's workspace has. Nothing
// it created survives a failure.
//
// The row has no sandbox of its own: the holder's container is what confines
// the child, so the holder's row is what says how.
func (s *Spawner) addChild(ctx context.Context, parent store.Workspace, parentHost workspace.Workspace, name, branch, base string) (store.Workspace, error) {
	holderID := parent.WorktreeOf
	if holderID == "" {
		holderID = parent.ID
	}
	ex, err := s.holderExecutor(parentHost)
	if err != nil {
		return store.Workspace{}, err
	}
	id := store.NewID()
	if err := workspace.AddWorktree(ctx, ex, id, branch, base); err != nil {
		return store.Workspace{}, err
	}
	childWS, err := s.opts.Store.CreateWorkspace(ctx, store.Workspace{
		ID:                id,
		ProjectID:         parent.ProjectID,
		Name:              name,
		Branch:            branch,
		BaseCommit:        base,
		Image:             parent.Image,
		State:             string(parentHost.State),
		ContainerID:       parentHost.ContainerID,
		ParentWorkspaceID: parent.ID,
		WorktreeOf:        holderID,
	})
	if err != nil {
		s.removeWorktree(ctx, ex, id)
		return store.Workspace{}, err
	}
	return childWS, nil
}

// holderExecutor returns the executor at the root of the container a
// workspace's files are in, which is where its repository's worktrees are
// added and removed.
func (s *Spawner) holderExecutor(host workspace.Workspace) (executor.Executor, error) {
	host.Dir = ""
	return s.opts.Workspaces.Executor(host)
}

// discard removes a child whose creation did not finish: its worktree and
// its row, which takes its sessions and entries with it.
func (s *Spawner) discard(ctx context.Context, parentHost workspace.Workspace, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportTimeout)
	defer cancel()
	if ex, err := s.holderExecutor(parentHost); err == nil {
		s.removeWorktree(ctx, ex, id)
	}
	if err := s.opts.Store.DeleteWorkspace(ctx, id); err != nil {
		s.opts.Logger.Error("remove half-created subagent workspace row", "workspace_id", id, "error", err)
	}
}

// removeWorktree removes a half-created child's worktree. It runs on a
// context of its own, so that the files go away even when the request that
// asked for the child is gone.
func (s *Spawner) removeWorktree(ctx context.Context, ex executor.Executor, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportTimeout)
	defer cancel()
	if err := workspace.RemoveWorktree(ctx, ex, id); err != nil {
		s.opts.Logger.Error("discard half-created subagent worktree", "workspace_id", id, "error", err)
	}
}

// depthOf reports how many parents a session has above it, following the
// subagent rows up to the session a user started. It stops counting past
// maxDepth, because any depth beyond it is refused the same way.
func (s *Spawner) depthOf(ctx context.Context, sessionID string, maxDepth int) (int, error) {
	depth := 0
	for id := sessionID; depth <= maxDepth; depth++ {
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

// recordedChildren counts the rows of a session's children that are still
// running, which is what a harness that restarted has left of them.
func (s *Spawner) recordedChildren(ctx context.Context, parentSessionID string) (int, error) {
	rows, err := s.opts.Store.Subagents(ctx, parentSessionID)
	if err != nil {
		return 0, err
	}
	running := 0
	for _, row := range rows {
		if row.State == store.RunRunning {
			running++
		}
	}
	return running, nil
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
