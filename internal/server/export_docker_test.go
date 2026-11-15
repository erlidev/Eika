//go:build docker

package server

import (
	"context"
	"time"

	"github.com/erlidev/eika/internal/tool/builtin"
)

// KeepHome runs the retry loop that ensures the home computer, with its first
// pause shortened to delay.
func (s *Server) KeepHome(ctx context.Context, delay time.Duration) { s.keepHome(ctx, delay) }

// ProbeProviders probes every stored provider once, as Run does at start.
func (s *Server) ProbeProviders(ctx context.Context) { s.probeProviders(ctx) }

// SweepIdle runs one idle sweep as though it were now, which lets a test
// move the clock instead of waiting out the timeout.
func (s *Server) SweepIdle(ctx context.Context, now time.Time) { s.idle.sweep(ctx, now) }

// BindProjectToolsForTest uses the production project binding with the test host.
func (s *Server) BindProjectToolsForTest(host Computers) error {
	s.deps.Computers = host
	reg, err := projectToolRegistry(builtin.Deps{Questions: s.deps.Questions, Search: s.deps.Search, Pages: s.deps.Pages, Projects: s.deps.Store}, func() *Server { return s })
	if err != nil {
		return err
	}
	s.deps.Tools = reg
	return nil
}
