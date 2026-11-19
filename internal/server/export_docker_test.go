//go:build docker

package server

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// DelegationPriority is the priority a home session's subagent runs would
// take now.
func (s *Server) DelegationPriority(ctx context.Context, home string) (inference.Priority, error) {
	return s.delegationPriority(ctx, home)
}

// KeepHome runs the retry loop that ensures the home computer, with its first
// pause shortened to delay.
func (s *Server) KeepHome(ctx context.Context, delay time.Duration) { s.keepHome(ctx, delay) }

// ProbeProviders probes every stored provider once, as Run does at start.
func (s *Server) ProbeProviders(ctx context.Context) { s.probeProviders(ctx) }

// SweepIdle runs one idle sweep as though it were now, which lets a test
// move the clock instead of waiting out the timeout.
func (s *Server) SweepIdle(ctx context.Context, now time.Time) { s.idle.sweep(ctx, now) }

// BindAgentToolsForTest uses the production project and delegation bindings
// with the test host.
func (s *Server) BindAgentToolsForTest(host Computers) error {
	s.deps.Computers = host
	reg, err := agentToolRegistry(builtin.Deps{Questions: s.deps.Questions, Search: s.deps.Search, Pages: s.deps.Pages, Projects: s.deps.Store}, func() *Server { return s })
	if err != nil {
		return err
	}
	s.deps.Tools = reg
	return nil
}

// AfterRunReleasesForTest calls f each time a finished run has released its
// session. Set it before any run starts.
func (s *Server) AfterRunReleasesForTest(f func(sessionID string)) { s.runs.released = f }

// PostNoticeForTest writes a notice with the entry id id to a home session,
// as a report does.
func (s *Server) PostNoticeForTest(ctx context.Context, sessionID, id, text string) error {
	return s.notices.post(ctx, sessionID, id, text)
}

// FailNoticeWritesForTest makes the next failures notice writes fail, and
// writes after a failure wait retry first. With lost set, each failing write
// is made and only its answer lost. Set it before any notice is posted.
func (s *Server) FailNoticeWritesForTest(failures int32, lost bool, retry time.Duration) {
	var failed atomic.Int32
	write := s.notices.write
	s.notices.retry = retry
	s.notices.write = func(ctx context.Context, sessionID string, e store.Entry) (bool, error) {
		if failed.Add(1) <= failures {
			if lost {
				if _, err := write(ctx, sessionID, e); err != nil {
					return false, err
				}
			}
			return false, errors.New("database unavailable")
		}
		return write(ctx, sessionID, e)
	}
}

// StartIdleCompactionForTest starts idle compaction without waiting for a sweep.
func (s *Server) StartIdleCompactionForTest(ctx context.Context, id string) error {
	s.runs.mu.Lock()
	s.runs.lastImportant = time.Now().Add(-time.Hour)
	s.runs.mu.Unlock()
	_, _, err := s.runs.launch(ctx, id, "", job{compact: true, idle: true, idleFill: 1, idleFor: 30 * time.Minute, priority: inference.Maintenance})
	return err
}
