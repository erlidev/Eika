package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/memory"
	"github.com/erlidev/eika/internal/memory/repo"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
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

// MemoryWriter returns Core's memory writer, or nil until recovery has
// finished. Nothing may serve or write memory before then: the index can be
// behind the files after a crash, and files can have been edited outside Eika.
func (s *Server) MemoryWriter() *memory.Writer { return s.memoryWriter.Load() }

// recoverMemory reconciles the memory index and repository, then publishes
// the writer. It is the only place the writer is made available.
func (s *Server) recoverMemory(ctx context.Context) error {
	r, err := s.MemoryRepository(ctx)
	if err != nil {
		return err
	}
	w := memory.NewWriter(r, s.deps.Store)
	w.SetScreen(s.screenMemoryFile)
	rep, err := w.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("reconcile memory: %w", err)
	}
	s.log.Info("memory reconciled", "reindexed", rep.Reindexed, "committed", rep.Committed,
		"restored", rep.Restored, "removed", rep.Removed, "held_pending", rep.Held, "invalid", rep.Invalid)
	s.memoryWriter.Store(w)
	return nil
}

// screenMemoryFile is the writer's screen for a file found changed outside
// it: Sentry's write check decides active or pending. A credential, a failed
// or refused check, or no gate leaves it pending (the writer never raises a
// state), so the edit is held for the user rather than dropped from disk.
func (s *Server) screenMemoryFile(ctx context.Context, f memory.File) (string, error) {
	checker, ok := s.gate.(sentry.MemoryChecker)
	if !ok {
		return "pending", nil
	}
	v, err := checker.CheckMemory(ctx, sentry.MemoryCandidate{Name: f.Name, Description: f.Description, Body: f.Body})
	if err != nil || v.Decision != sentry.MemoryActive {
		return "pending", err
	}
	return "active", nil
}

// ReindexMemory rebuilds the whole memory index from the files in the running
// memory computer, through the same writer as a normal write. It is what
// `eika -memory-reindex` and `make memory-reindex` run.
func ReindexMemory(ctx context.Context, cfg config.Config, log *slog.Logger) (memory.Report, error) {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return memory.Report{}, err
	}
	defer st.Close()
	host, err := computer.NewHost(computer.Options{
		DockerSocket: cfg.DockerSocket, Image: cfg.SandboxImage, Network: cfg.SandboxNetwork,
		EikadBinary: cfg.EikadBinary, InternalNetwork: cfg.SandboxInternalNetwork, EgressProxyURL: cfg.EgressProxyURL,
	}, log)
	if err != nil {
		return memory.Report{}, err
	}
	defer host.Close()
	cx, err := host.Inspect(ctx, memoryComputerID)
	if err != nil {
		return memory.Report{}, fmt.Errorf("find the memory computer: %w", err)
	}
	if cx.State != computer.StateRunning {
		if err := host.Start(ctx, &cx); err != nil {
			return memory.Report{}, fmt.Errorf("start the memory computer: %w", err)
		}
	}
	ex, err := host.Executor(cx)
	if err != nil {
		return memory.Report{}, err
	}
	return memory.NewWriter(repo.NewRepository(ex), st).Reindex(ctx)
}

// keepMemory ensures the memory computer and recovers its index, trying
// again after delay and then after longer ones, until that works or ctx is
// cancelled.
func (s *Server) keepMemory(ctx context.Context, delay time.Duration) {
	for {
		err := s.EnsureMemory(ctx)
		if err == nil {
			err = s.recoverMemory(ctx)
		}
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

var errNoMemory = errors.New("memory is not available yet")

// memoryService gives the memory tools the server's writer. The tools are
// built before the server, so it is looked up on each call.
type memoryService struct{ server func() *Server }

func (m memoryService) writer() (*memory.Writer, error) {
	// Nil until recovery has finished, so a tool never serves or writes
	// before the index and the files agree.
	if s := m.server(); s != nil {
		if w := s.MemoryWriter(); w != nil {
			return w, nil
		}
	}
	return nil, errNoMemory
}

func (m memoryService) Search(ctx context.Context, q memory.Query) ([]memory.Hit, error) {
	w, err := m.writer()
	if err != nil {
		return nil, err
	}
	return w.Search(ctx, q)
}

func (m memoryService) Read(ctx context.Context, name string) (memory.File, error) {
	w, err := m.writer()
	if err != nil {
		return memory.File{}, err
	}
	return w.Read(ctx, name)
}

func (m memoryService) Write(ctx context.Context, req memory.Request) (memory.Result, error) {
	w, err := m.writer()
	if err != nil {
		return memory.Result{}, err
	}
	return w.Write(ctx, req)
}

func (m memoryService) Archive(ctx context.Context, name, reason string) (memory.File, error) {
	w, err := m.writer()
	if err != nil {
		return memory.File{}, err
	}
	return w.Archive(ctx, name, reason)
}

// memoryCheck is Sentry's write check, reached through the server's gate.
type memoryCheck struct{ server func() *Server }

func (m memoryCheck) CheckMemory(ctx context.Context, c sentry.MemoryCandidate) (sentry.MemoryVerdict, error) {
	if s := m.server(); s != nil {
		if checker, ok := s.gate.(sentry.MemoryChecker); ok {
			return checker.CheckMemory(ctx, c)
		}
	}
	return sentry.MemoryVerdict{}, errNoMemory
}
