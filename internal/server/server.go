package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/egress"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/sandbox"
	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/fetch"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
	"github.com/erlidev/eika/internal/workspace"
	"github.com/erlidev/eika/internal/workspace/hub"
)

// shutdownTimeout bounds how long in-flight requests may finish after the
// context passed to Run is cancelled.
const shutdownTimeout = 10 * time.Second

// subagentStopTimeout bounds how long Close waits for the child agents to
// commit what they have and record how they ended.
const subagentStopTimeout = 2 * time.Minute

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
	CloneAt(ctx context.Context, ws workspace.Workspace, project, branch, commit string) (string, error)
	Push(ctx context.Context, ws workspace.Workspace, project, branch string) error
	Fetch(ctx context.Context, ws workspace.Workspace, project, branch string) error
	Executor(ws workspace.Workspace) (executor.Executor, error)
	// Terminal opens a shell in a running workspace for the person using it.
	// It is not part of the executor, so no tool can reach a terminal.
	Terminal(ctx context.Context, ws workspace.Workspace, rows, cols uint16) (*websocket.Conn, error)
	// Process starts a long-lived process in a running workspace, which is
	// how a stdio MCP server runs there. Like Terminal, it is not part of
	// the executor, so no tool can hold a process of its own.
	Process(ctx context.Context, ws workspace.Workspace, spec sandbox.ProcessSpec, stderr func(string)) (io.ReadWriteCloser, error)
	HasImage(ctx context.Context, ref string) (bool, error)
	// Confine applies a workspace's limits and network to its container,
	// running or stopped, without restarting it.
	Confine(ctx context.Context, ws *workspace.Workspace, c workspace.Confinement) error
	// Usage samples what a running workspace is consuming.
	Usage(ctx context.Context, ws workspace.Workspace) (workspace.Usage, error)
	// Capacity is what the Docker host has to give, which bounds a limit.
	Capacity(ctx context.Context) (workspace.Capacity, error)
	// EgressControl reports whether a workspace's egress can be restricted.
	EgressControl() bool
	// PortURL is where the harness reaches one of a workspace's ports,
	// which is where a preview is forwarded.
	PortURL(ctx context.Context, ws workspace.Workspace, port int) (string, error)
}

// Egress is the part of the egress proxy the API uses: the hosts a
// workspace was refused, and forgetting a workspace that is gone.
// *egress.Proxy is the one implementation.
type Egress interface {
	Blocked(workspaceID string) []egress.Blocked
	Forget(workspaceID string)
}

// Subagents is the part of the subagent spawner the API uses: the children of
// a session, and stopping one or all of them. *subagent.Spawner is the one
// implementation; UseSubagents gives it to the server once it exists.
type Subagents interface {
	List(ctx context.Context, parentSessionID string) ([]builtin.AgentResult, error)
	Abort(ctx context.Context, id string) error
	AbortChildren(parentSessionID string)
	Shutdown(ctx context.Context)
}

// Hub is the part of the git hub the API uses: creating a project's
// repository, mirroring a remote into it, pushing a branch back to that
// remote, and serving git over HTTP.
type Hub interface {
	Init(ctx context.Context, project string) (string, error)
	Mirror(ctx context.Context, project, remoteURL string, creds hub.Credentials) error
	Push(ctx context.Context, project, remoteURL, refspec string, creds hub.Credentials) error
	Handler() http.Handler
}

// Providers builds a provider on an endpoint the user configured. The models
// and providers themselves are rows; this is only what turns one into a
// client. *provider.Registry is the production implementation; a test scripts
// one.
type Providers interface {
	Kinds() []string
	Build(kind string, e provider.Endpoint) (provider.Provider, error)
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
	Providers  Providers
	// Secrets seals the credentials the UI writes, API keys and remote
	// passwords, and opens them again when a provider or the hub needs one.
	Secrets   *secret.Box
	Tools     *tool.Registry
	Questions *builtin.Questions
	Bus       *event.Bus
	// Search and Pages back web_search and web_fetch, which the tools hold;
	// the API reports their health and lets the user try a search. Without
	// them the search routes are not served.
	Search *search.Engine
	Pages  *fetch.Reader
	// Subagents is set by UseSubagents rather than by the caller: the spawner
	// needs the run manager this server owns.
	Subagents Subagents
	// Egress is the proxy restricted sandboxes reach the internet through.
	// Nil when the harness cannot restrict a sandbox's egress.
	Egress Egress
	// MCP holds the connections to the MCP servers the user configured,
	// whose tools runs offer beside the built-in ones. Without it the MCP
	// routes are not served. The caller closes it after the server.
	MCP *mcp.Pool
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
	// titles names untitled sessions after their first message.
	titles *titles

	// loginMu makes sign-in attempts take turns, which bounds how fast a
	// password can be guessed without locking its owner out.
	loginMu sync.Mutex

	// tokensMu guards tokens.
	tokensMu sync.Mutex
	// tokens caches each workspace's egress proxy token, read from its
	// container the first time the proxy is asked about it.
	tokens map[string]string

	// previews holds the tickets and sessions of workspace previews.
	previews previews
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
	s.titles = newTitles(s)
	s.routes()
	return s
}

// UseSubagents gives the server the spawner that runs child agents, and gives
// the spawner the run manager it drives them with. The wiring calls it once,
// before serving: the spawner cannot be built before the server, because the
// tool registry every run shares holds the spawn_agent tool it backs.
func (s *Server) UseSubagents(sp Subagents, attach func(subagent.Runner)) {
	s.deps.Subagents = sp
	attach(s)
}

// Handler returns the server's root handler. A request for a workspace
// preview's host goes to the preview; everything else to the routes.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, port, ok := previewHost(r.Host); ok && s.deps.Store != nil && s.deps.Workspaces != nil {
			s.servePreview(w, r, id, port)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

// Bus returns the event bus the server fans events out on, which is the
// emitter every run writes to.
func (s *Server) Bus() *event.Bus { return s.deps.Bus }

// Run serves until ctx is cancelled, then drains in-flight requests and stops
// the runs that are still going.
func (s *Server) Run(ctx context.Context) error {
	defer s.Close()
	return Serve(ctx, s.cfg.Listen, s.Handler(), s.log)
}

// Close aborts every run, session title, and child agent that is still going
// and returns once they have stopped. Run calls it on shutdown; a caller that
// serves Handler itself calls it when it is done, before the store it gave
// the server closes.
//
// The children come after the runs: a child whose parent spawned it without
// waiting outlives that run, so aborting the runs alone leaves it writing to
// a database that is about to close. Titles come after the runs too, since
// no run is left to start one.
func (s *Server) Close() {
	s.runs.stopAll()
	s.titles.stop()
	if s.deps.Subagents == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), subagentStopTimeout)
	defer cancel()
	s.deps.Subagents.Shutdown(ctx)
}

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
