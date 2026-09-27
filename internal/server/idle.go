package server

import (
	"context"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/workspace"
)

// idleSweepInterval is how often the harness looks for running workspaces
// that have gone unused for the idle timeout. It bounds how late a stop is.
const idleSweepInterval = time.Minute

// idleWorkspaces stops the running workspaces no run has used for the
// workspace_idle_minutes setting, so an unattended sandbox gives its memory
// and CPU back. A workspace is in use while a run is going or being built in
// it or in a worktree it holds; a terminal or a preview does not count.
//
// What it knows lives in memory: after a restart every running workspace
// gets the whole timeout again, which errs on the side of leaving one up.
type idleWorkspaces struct {
	server *Server

	mu sync.Mutex
	// lastUsed is when each workspace was last seen in use, by workspace id,
	// which for a worktree is its own and not its holder's.
	lastUsed map[string]time.Time
}

// newIdleWorkspaces returns the idle stopper of the server.
func newIdleWorkspaces(s *Server) *idleWorkspaces {
	return &idleWorkspaces{server: s, lastUsed: make(map[string]time.Time)}
}

// used records that a workspace was in use at the given time. A chat, which
// has no workspace, passes the empty id and is ignored.
func (i *idleWorkspaces) used(workspaceID string, at time.Time) {
	if workspaceID == "" {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if at.After(i.lastUsed[workspaceID]) {
		i.lastUsed[workspaceID] = at
	}
}

// watch sweeps once a minute until ctx is cancelled.
func (i *idleWorkspaces) watch(ctx context.Context) {
	ticker := time.NewTicker(idleSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			i.sweep(ctx, now)
		}
	}
}

// sweep stops every running workspace whose last use is at least the idle
// timeout before now. A workspace it sees running for the first time counts
// as used now.
func (i *idleWorkspaces) sweep(ctx context.Context, now time.Time) {
	s := i.server
	timeout := s.idleTimeout(ctx)
	if timeout == 0 {
		return
	}
	all, err := s.deps.Store.Workspaces(ctx, "")
	if err != nil {
		s.log.Error("list workspaces for the idle sweep", "error", err)
		return
	}
	for id := range s.runs.workspacesInUse() {
		i.used(id, now)
	}
	for _, ws := range i.idle(all, now, timeout) {
		// A run may have started since the look above; it keeps the
		// workspace up until the next sweep.
		if i.inUse(ws, all) {
			continue
		}
		if _, err := s.changeState(ctx, ws, s.stopWorkspace); err != nil {
			s.log.Error("stop idle workspace", "workspace_id", ws.ID, "error", err)
			continue
		}
		s.log.Info("idle workspace stopped", "workspace_id", ws.ID, "idle_timeout", timeout.String())
	}
}

// idle returns the running workspaces among all whose last use, their own or
// that of a worktree they hold, is at least timeout before now. It forgets
// the workspaces that are not running, so that one started again begins
// from its start and not from a use before it stopped.
func (i *idleWorkspaces) idle(all []store.Workspace, now time.Time, timeout time.Duration) []store.Workspace {
	i.mu.Lock()
	defer i.mu.Unlock()
	last := make(map[string]time.Time, len(all))
	for _, ws := range all {
		holder := ws.ID
		if ws.WorktreeOf != "" {
			holder = ws.WorktreeOf
		}
		if at := i.lastUsed[ws.ID]; at.After(last[holder]) {
			last[holder] = at
		}
	}
	var out []store.Workspace
	for _, ws := range all {
		if ws.State != string(workspace.StateRunning) {
			delete(i.lastUsed, ws.ID)
			continue
		}
		if ws.WorktreeOf != "" {
			continue
		}
		at, seen := last[ws.ID]
		if !seen || at.IsZero() {
			i.lastUsed[ws.ID] = now
			continue
		}
		if now.Sub(at) >= timeout {
			out = append(out, ws)
		}
	}
	return out
}

// inUse reports whether a run is going in the workspace or in a worktree it
// holds.
func (i *idleWorkspaces) inUse(holder store.Workspace, all []store.Workspace) bool {
	inUse := i.server.runs.workspacesInUse()
	for _, ws := range all {
		if inUse[ws.ID] && (ws.ID == holder.ID || ws.WorktreeOf == holder.ID) {
			return true
		}
	}
	return false
}
