package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/memory/repo"
)

// The private memory computer. It holds the memory repository and is how Core
// reads, writes and runs git on it without touching files itself. It is not a
// row in the store, so the computers API, the UI, the idle stopper and every
// tool's computer argument never see it, and no agent run is given its
// executor. It sits on the internal sandbox network with egress none, and
// mounts only its own volume.
const (
	memoryComputerID = "memory"
	memoryRole       = "memory"
	// memoryRoot is where the memory repository is mounted in its computer.
	memoryRoot = "/var/lib/eika/memory"
)

// EnsureMemory makes the memory computer exist and run, and the repository in
// it initialised. Calling it again changes nothing. A host that cannot give a
// computer an internal network cannot give it no way out either, so it
// returns computer.ErrNoEgressControl rather than run one with a route out.
func (s *Server) EnsureMemory(ctx context.Context) error {
	s.memoryMu.Lock()
	defer s.memoryMu.Unlock()

	host, err := s.deps.Computers.Inspect(ctx, memoryComputerID)
	switch {
	case err == nil:
	case errors.Is(err, computer.ErrNoComputer):
		if host, err = s.deps.Computers.Create(ctx, computer.Spec{
			ID:    memoryComputerID,
			Image: s.sandboxImage(ctx),
			Root:  memoryRoot,
			Role:  memoryRole,
			Confinement: computer.Confinement{
				Limits:  computer.Limits{CPUs: 1, MemoryBytes: 256 << 20, PIDs: 256},
				Proxied: true,
			},
		}); err != nil {
			return fmt.Errorf("create the memory computer: %w", err)
		}
		s.forgetEgress(memoryComputerID)
		s.log.Info("memory computer created")
	default:
		return err
	}
	if host.State != computer.StateRunning {
		if err := s.deps.Computers.Start(ctx, &host); err != nil {
			return fmt.Errorf("start the memory computer: %w", err)
		}
	}
	ex, err := s.deps.Computers.Executor(host)
	if err != nil {
		return err
	}
	if err := repo.NewRepository(ex).Init(ctx); err != nil {
		return fmt.Errorf("initialise the memory repository: %w", err)
	}
	return nil
}

// MemoryRepository returns the memory repository, starting the memory
// computer if it is stopped. It is for Core's memory writer alone.
func (s *Server) MemoryRepository(ctx context.Context) (*repo.Repository, error) {
	if err := s.EnsureMemory(ctx); err != nil {
		return nil, err
	}
	host, err := s.deps.Computers.Inspect(ctx, memoryComputerID)
	if err != nil {
		return nil, err
	}
	ex, err := s.deps.Computers.Executor(host)
	if err != nil {
		return nil, err
	}
	return repo.NewRepository(ex), nil
}

// keepMemory ensures the memory computer, trying again after delay and then
// after longer ones, until that works or ctx is cancelled.
func (s *Server) keepMemory(ctx context.Context, delay time.Duration) {
	for {
		err := s.EnsureMemory(ctx)
		if err == nil || ctx.Err() != nil {
			return
		}
		if errors.Is(err, computer.ErrNoEgressControl) {
			s.log.Warn("memory computer not started: it needs the internal sandbox network", "error", err)
			return
		}
		s.log.Error("ensure memory computer", "error", err, "retry_in", delay.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, longestHomeRetry)
	}
}
