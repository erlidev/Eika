package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
	"github.com/erlidev/eika/internal/workspace"
	"github.com/erlidev/eika/internal/workspace/hub"
)

// shutdownTimeout bounds how long in-flight requests may finish after the
// context passed to Run is cancelled.
const shutdownTimeout = 10 * time.Second

// Workspaces is the part of the workspace host the API uses. It is an
// interface so that the handler tests run against a workspace backed by a
// temporary directory instead of a Docker daemon; *workspace.Host is the one
// production implementation.
type Workspaces interface {
	Create(ctx context.Context, spec workspace.Spec) (workspace.Workspace, error)
	Start(ctx context.Context, ws *workspace.Workspace) error
	Stop(ctx context.Context, ws *workspace.Workspace) error
	Destroy(ctx context.Context, ws *workspace.Workspace) error
	Inspect(ctx context.Context, id string) (workspace.Workspace, error)
	List(ctx context.Context) ([]workspace.Workspace, error)
	Clone(ctx context.Context, ws workspace.Workspace, project, branch string) (string, error)
	Executor(ws workspace.Workspace) (executor.Executor, error)
}

// Hub is the part of the git hub the API uses: creating a project's
// repository, mirroring a remote into it, and serving git over HTTP.
type Hub interface {
	Init(ctx context.Context, project string) (string, error)
	Mirror(ctx context.Context, project, remoteURL string, creds hub.Credentials) error
	Handler() http.Handler
}

// Models is the set of models a run may use. The production implementation
// builds a provider from the configuration; a test scripts one.
type Models interface {
	Names() []string
	Provider(name string) (provider.Provider, error)
}

// Deps are the harness pieces the API serves. Every one of them is built once
// during wiring and shared by every request. Store decides how much of the
// API exists: without it the server serves the health checks and the event
// stream alone, and with it every other field must be set too, because every
// remaining route reads or writes rows.
type Deps struct {
	Store      *store.Store
	Hub        Hub
	Workspaces Workspaces
	Models     Models
	Tools      *tool.Registry
	Questions  *builtin.Questions
	Bus        *event.Bus
}

// Options configures a Server beyond what config.Config carries.
type Options struct {
	// WebDir is the directory holding the built frontend. When empty the
	// server serves the API only, which is what `make dev` wants because Vite
	// serves the frontend itself.
	WebDir string
}

// Server serves the Eika HTTP API.
type Server struct {
	cfg  config.Config
	log  *slog.Logger
	mux  *http.ServeMux
	opts Options
	deps Deps
	tree *session.Tree
	runs *runs
}

// New builds a Server for the given configuration and dependencies. A zero
// Deps serves the health checks alone, which is what the phase 0 tests and a
// harness without a database get.
func New(cfg config.Config, log *slog.Logger, deps Deps, opts Options) *Server {
	if deps.Bus == nil {
		deps.Bus = event.NewBus(log)
	}
	s := &Server{cfg: cfg, log: log, mux: http.NewServeMux(), opts: opts, deps: deps}
	if deps.Store != nil {
		s.tree = session.NewTree(deps.Store)
	}
	s.runs = newRuns(s)
	s.routes()
	return s
}

// Handler returns the server's root handler.
func (s *Server) Handler() http.Handler { return s.mux }

// Bus returns the event bus the server fans events out on, which is the
// emitter every run writes to.
func (s *Server) Bus() *event.Bus { return s.deps.Bus }

// Run serves until ctx is cancelled, then drains in-flight requests and stops
// the runs that are still going.
func (s *Server) Run(ctx context.Context) error {
	defer s.Close()
	return Serve(ctx, s.cfg.Listen, s.mux, s.log)
}

// Close aborts every run that is still going and returns once they have
// stopped. Run calls it on shutdown; a caller that serves Handler itself
// calls it when it is done, before the store it gave the server closes.
func (s *Server) Close() { s.runs.stopAll() }

// Reconcile brings the recorded workspace states in line with the containers
// the host actually has. The harness calls it on startup: a container may
// have been stopped or removed while the harness was down.
func (s *Server) Reconcile(ctx context.Context) error {
	if s.deps.Store == nil {
		return nil
	}
	aborted, err := s.deps.Store.AbortRunningRuns(ctx)
	if err != nil {
		return err
	}
	if aborted != 0 {
		s.log.Info("stale runs aborted", "count", aborted)
	}
	if s.deps.Workspaces == nil {
		return nil
	}
	recorded, err := s.deps.Store.Workspaces(ctx, "")
	if err != nil {
		return err
	}
	type observedWorkspace struct {
		state       workspace.State
		containerID string
	}
	observed := make(map[string]observedWorkspace, len(recorded))
	for _, w := range recorded {
		host, err := s.deps.Workspaces.Inspect(ctx, w.ID)
		if err == nil {
			observed[w.ID] = observedWorkspace{state: host.State, containerID: host.ContainerID}
			continue
		}
		if errors.Is(err, workspace.ErrNoWorkspace) {
			observed[w.ID] = observedWorkspace{state: workspace.StateGone}
			continue
		}
		return fmt.Errorf("reconcile workspace %s: %w", w.ID, err)
	}
	for _, w := range recorded {
		actual := observed[w.ID]
		state := string(actual.state)
		if state == w.State {
			continue
		}
		if err := s.deps.Store.SetWorkspaceState(ctx, w.ID, state, actual.containerID); err != nil {
			return err
		}
		s.workspaceState(ctx, w.ID, w.ProjectID, state)
		s.log.Info("workspace state reconciled", "workspace_id", w.ID, "was", w.State, "state", state)
	}
	return nil
}

// workspaceState reports a workspace lifecycle transition to the clients
// watching that workspace.
func (s *Server) workspaceState(ctx context.Context, workspaceID, projectID, state string) {
	e, err := event.New(event.TypeWorkspaceState, event.WorkspaceTopic(workspaceID), event.WorkspaceState{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		State:       state,
	})
	if err != nil {
		s.log.Error("encode workspace state event", "workspace_id", workspaceID, "error", err)
		return
	}
	s.deps.Bus.Emit(ctx, e)
}

// Serve listens on addr and serves h until ctx is cancelled, then drains
// in-flight requests. It returns nil on a clean shutdown.
func Serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		defer close(serveErr)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	log.Info("http server listening", "addr", ln.Addr().String())

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve on %s: %w", addr, err)
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down server on %s: %w", addr, err)
	}
	log.Info("http server stopped", "addr", addr)
	return nil
}
