package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/memory"
	"github.com/erlidev/eika/internal/memory/repo"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
)

// The private memory computer. It holds the memory repository and is how Core
// reads, writes and runs git on it without touching files itself. Its id is
// generated once per install and recorded in the store's memory_computer
// table, so its container and volume (eika-cx-<id>) belong to this install
// alone. It is not a row in computers, so the computers API, the UI, the idle stopper and every
// tool's computer argument never see it, and no agent run is given its
// executor. It sits on the internal sandbox network with egress none, and
// mounts only its own volume.
const (
	memoryRole = "memory"
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

	id, err := s.deps.Store.EnsureMemoryComputerID(ctx)
	if err != nil {
		return err
	}
	s.memoryID.Store(&id)
	host, err := s.deps.Computers.Inspect(ctx, id)
	switch {
	case err == nil:
	case errors.Is(err, computer.ErrNoComputer):
		if host, err = s.deps.Computers.Create(ctx, computer.Spec{
			ID:    id,
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
		s.forgetEgress(id)
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

// isMemoryComputer is whether id is the private memory computer's. It reads
// the store once, and only a recorded id can match, so a lookup never records one.
func (s *Server) isMemoryComputer(ctx context.Context, id string) bool {
	if known := s.memoryID.Load(); known != nil {
		return *known == id
	}
	rec, err := s.deps.Store.MemoryComputerID(ctx)
	if err != nil {
		return false
	}
	s.memoryID.Store(&rec)
	return rec == id
}

// MemoryRepository returns the memory repository, starting the memory
// computer if it is stopped. It is for Core's memory writer alone.
func (s *Server) MemoryRepository(ctx context.Context) (*repo.Repository, error) {
	if err := s.EnsureMemory(ctx); err != nil {
		return nil, err
	}
	id, err := s.deps.Store.MemoryComputerID(ctx)
	if err != nil {
		return nil, err
	}
	host, err := s.deps.Computers.Inspect(ctx, id)
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
	w := memory.NewWriter(&memoryFiles{fresh: func(ctx context.Context) (memory.Files, error) { return s.MemoryRepository(ctx) }, log: s.log, cur: r}, s.deps.Store)
	w.SetScreen(s.screenMemoryFile)
	w.SetOnChange(s.publishMemoryChange)
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
	id, err := st.MemoryComputerID(ctx)
	if err != nil {
		return memory.Report{}, fmt.Errorf("find the memory computer: %w", err)
	}
	cx, err := host.Inspect(ctx, id)
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

// memoryFiles is the writer's file store: a cached memory repository that is
// made again when it fails. The repository holds the memory computer's daemon
// address and token, which change if the computer is recreated after startup
// (its container removed, the host restarted), so a failure drops the cache,
// ensures the computer again and tries once more. Every operation is safe to
// repeat: a write replaces the file and a commit with nothing staged is a no-op.
type memoryFiles struct {
	// fresh ensures the computer and returns a repository for it as it is now.
	fresh func(context.Context) (memory.Files, error)
	log   *slog.Logger
	mu    sync.Mutex
	cur   memory.Files
}

func (f *memoryFiles) current() memory.Files {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur
}

// refresh replaces failed with a repository for the computer as it is now,
// unless another call already has.
func (f *memoryFiles) refresh(ctx context.Context, failed memory.Files) (memory.Files, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cur != failed {
		return f.cur, nil
	}
	r, err := f.fresh(ctx)
	if err != nil {
		return nil, err
	}
	f.cur = r
	return r, nil
}

// retryable is whether err may come from a stale executor rather than from
// the request: a missing file and a cancelled call do not.
func retryable(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() == nil && !errors.Is(err, fs.ErrNotExist)
}

func memoryFilesDo[T any](ctx context.Context, f *memoryFiles, op func(memory.Files) (T, error)) (T, error) {
	r := f.current()
	v, err := op(r)
	if !retryable(ctx, err) {
		return v, err
	}
	fresh, rerr := f.refresh(ctx, r)
	if rerr != nil {
		return v, err
	}
	f.log.Warn("memory repository refreshed after a failure", "error", err)
	return op(fresh)
}

func (f *memoryFiles) Read(ctx context.Context, name string) ([]byte, error) {
	return memoryFilesDo(ctx, f, func(r memory.Files) ([]byte, error) { return r.Read(ctx, name) })
}

func (f *memoryFiles) Write(ctx context.Context, name string, data []byte) error {
	_, err := memoryFilesDo(ctx, f, func(r memory.Files) (struct{}, error) { return struct{}{}, r.Write(ctx, name, data) })
	return err
}

func (f *memoryFiles) Commit(ctx context.Context, name, message string) error {
	_, err := memoryFilesDo(ctx, f, func(r memory.Files) (struct{}, error) { return struct{}{}, r.Commit(ctx, name, message) })
	return err
}

func (f *memoryFiles) List(ctx context.Context) ([]string, error) {
	return memoryFilesDo(ctx, f, func(r memory.Files) ([]string, error) { return r.List(ctx) })
}

func (f *memoryFiles) Uncommitted(ctx context.Context) ([]string, error) {
	return memoryFilesDo(ctx, f, func(r memory.Files) ([]string, error) { return r.Uncommitted(ctx) })
}

func (f *memoryFiles) Remove(ctx context.Context, name, message string) error {
	_, err := memoryFilesDo(ctx, f, func(r memory.Files) (struct{}, error) { return struct{}{}, r.Remove(ctx, name, message) })
	return err
}

func (f *memoryFiles) Restore(ctx context.Context, name string) (bool, error) {
	return memoryFilesDo(ctx, f, func(r memory.Files) (bool, error) { return r.Restore(ctx, name) })
}

// RecoverMemory ensures the memory computer and reconciles its index, then
// publishes the writer, as keepMemory does at startup without the retries.
func (s *Server) RecoverMemory(ctx context.Context) error {
	if err := s.EnsureMemory(ctx); err != nil {
		return err
	}
	return s.recoverMemory(ctx)
}
