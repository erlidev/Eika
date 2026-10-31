package server

import (
	"context"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/store"
)

// idleSweepInterval is how often the harness looks for running computers
// that have gone unused for the idle timeout. It bounds how late a stop is.
const idleSweepInterval = time.Minute

// idleComputers stops the running computers no run has used for the
// computer_idle_minutes setting, so an unattended sandbox gives its memory
// and CPU back. A computer is in use while a run is going or being built in
// it or in a worktree it holds; nothing else counts.
//
// What it knows lives in memory: after a restart every running computer
// gets the whole timeout again, which errs on the side of leaving one up.
type idleComputers struct {
	server *Server

	mu sync.Mutex
	// lastUsed is when each computer was last seen in use, by computer id.
	lastUsed map[string]time.Time
}

// newIdleComputers returns the idle stopper of the server.
func newIdleComputers(s *Server) *idleComputers {
	return &idleComputers{server: s, lastUsed: make(map[string]time.Time)}
}

// used records that a computer was in use at the given time. A chat, which
// has no computer, passes the empty id and is ignored.
func (i *idleComputers) used(computerID string, at time.Time) {
	if computerID == "" {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if at.After(i.lastUsed[computerID]) {
		i.lastUsed[computerID] = at
	}
}

// watch sweeps once a minute until ctx is cancelled.
func (i *idleComputers) watch(ctx context.Context) {
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

// sweep stops every running computer whose last use is at least the idle
// timeout before now. A computer it sees running for the first time counts
// as used now.
func (i *idleComputers) sweep(ctx context.Context, now time.Time) {
	s := i.server
	timeout := s.idleTimeout(ctx)
	if timeout == 0 {
		return
	}
	all, err := s.deps.Store.Computers(ctx)
	if err != nil {
		s.log.Error("list computers for the idle sweep", "error", err)
		return
	}
	for id := range s.runs.computersInUse() {
		i.used(id, now)
	}
	for _, cx := range i.idle(all, now, timeout) {
		// A run may have started since the look above; it keeps the
		// computer up until the next sweep.
		if i.inUse(cx) {
			continue
		}
		if _, err := s.changeState(ctx, cx, s.stopComputer); err != nil {
			s.log.Error("stop idle computer", "computer_id", cx.ID, "error", err)
			continue
		}
		s.log.Info("idle computer stopped", "computer_id", cx.ID, "idle_timeout", timeout.String())
	}
}

// idle returns the running computers among all whose last use is at least
// timeout before now. It forgets
// the computers that are not running, so that one started again begins
// from its start and not from a use before it stopped.
func (i *idleComputers) idle(all []store.Computer, now time.Time, timeout time.Duration) []store.Computer {
	i.mu.Lock()
	defer i.mu.Unlock()
	var out []store.Computer
	for _, cx := range all {
		if cx.State != string(computer.StateRunning) {
			delete(i.lastUsed, cx.ID)
			continue
		}
		at, seen := i.lastUsed[cx.ID]
		if !seen || at.IsZero() {
			i.lastUsed[cx.ID] = now
			continue
		}
		if now.Sub(at) >= timeout {
			out = append(out, cx)
		}
	}
	return out
}

// inUse reports whether a run is going in the computer.
func (i *idleComputers) inUse(cx store.Computer) bool {
	return i.server.runs.computersInUse()[cx.ID]
}
