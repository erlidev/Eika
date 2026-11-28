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
	"sync/atomic"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/egress"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/sandbox"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/memory"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/schedule"
	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/fetch"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/sentry/screen"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/skill"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// shutdownTimeout bounds how long in-flight requests may finish after the
// context passed to Run is cancelled.
const shutdownTimeout = 10 * time.Second

// Computers is the part of the computer host the API uses. It is an
// interface so that the handler tests run against a computer backed by a
// temporary directory instead of a Docker daemon; *computer.Host is the one
// production implementation.
type Computers interface {
	Create(ctx context.Context, spec computer.Spec) (computer.Computer, error)
	// Recreate replaces a stopped computer's container, keeping its volume.
	Recreate(ctx context.Context, cx computer.Computer, spec computer.Spec) (computer.Computer, error)
	Start(ctx context.Context, cx *computer.Computer) error
	Stop(ctx context.Context, cx *computer.Computer) error
	Inspect(ctx context.Context, id string) (computer.Computer, error)
	Executor(cx computer.Computer) (executor.Executor, error)
	// Process starts a long-lived process in a running computer, which is
	// how a stdio MCP server runs there. It is not part of the executor, so
	// no tool can hold a process of its own.
	Process(ctx context.Context, cx computer.Computer, spec sandbox.ProcessSpec, stderr func(string)) (io.ReadWriteCloser, error)
	HasImage(ctx context.Context, ref string) (bool, error)
	// Confine applies a computer's limits and network to its container,
	// running or stopped, without restarting it.
	Confine(ctx context.Context, cx *computer.Computer, c computer.Confinement) error
	// Capacity is what the Docker host has to give, which bounds a limit.
	Capacity(ctx context.Context) (computer.Capacity, error)
	// EgressControl reports whether a computer's egress can be restricted.
	EgressControl() bool
	// Refresh gives a running computer's daemon its environment again.
	Refresh(ctx context.Context, cx computer.Computer) error
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
	Store     *store.Store
	Computers Computers
	Providers Providers
	// Secrets seals the credentials the UI writes, such as API keys, and opens
	// them again when a provider needs one.
	Secrets   *secret.Box
	Tools     *tool.Registry
	Questions *builtin.Questions
	Bus       *event.Bus
	// Search and Pages back web_search and web_fetch, which the tools hold;
	// the API reports their health and lets the user try a search. Without
	// them the search routes are not served.
	Search *search.Engine
	Pages  *fetch.Reader
	// Egress is the proxy sandboxes reach the internet through.
	// Nil when the harness cannot restrict a sandbox's egress.
	Egress *egress.Proxy
	// MCP holds the connections to the MCP servers the user configured,
	// whose tools runs offer beside the built-in ones. Without it the MCP
	// routes are not served. The caller closes it after the server.
	MCP *mcp.Pool
	// Pause is the scheduler's pause hook: called with true when Eika is
	// halted and false when it is resumed. Nil until the scheduler of phase 7
	// exists.
	Pause func(paused bool)
}

// Options configures a Server beyond what config.Config carries.
type Options struct {
	// WebDir is the directory holding the built frontend. When empty the
	// server serves the API only, which is what `make dev` wants because Vite
	// serves the frontend itself.
	WebDir string
	// LiveTest enables controls compiled only for the live suite.
	LiveTest bool
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
	// subagents owns the home session's named subagents and their runs.
	subagents *subagent.Service
	// notices delivers subagent reports to the home session.
	notices       *notices
	notifications *notifications
	// counters keeps each session's token counter across its runs.
	counters *sessionCounters
	// titles names untitled sessions after their first message.
	titles *titles
	// idle stops the running computers no run has used for a while.
	idle *idleComputers
	// inference is the queue every model call of the server waits in.
	inference *inference.Queue

	// inferenceMu serialises describing the queue from the store, and
	// inferenceSynced records that it has been described once.
	inferenceMu     sync.Mutex
	inferenceSynced bool

	// homeMu makes EnsureHome take turns. startMu serializes computer use
	// reservations, idle stops and starts for runs.
	homeMu sync.Mutex
	// memoryMu makes EnsureMemory take turns.
	memoryMu sync.Mutex
	// memoryID caches the recorded id of the memory computer once known.
	memoryID atomic.Pointer[string]
	// memoryWriter is set once the memory index has been reconciled.
	memoryWriter atomic.Pointer[memory.Writer]
	// skillInstaller is set once the skills rows have been reconciled.
	skillInstaller atomic.Pointer[skill.Installer]
	// skillsMu makes EnsureSkillsInstaller take turns; skillsID caches the
	// recorded id of the skills installer once known.
	skillsMu sync.Mutex
	skillsID atomic.Pointer[string]
	startMu  sync.Mutex

	// loginMu makes sign-in attempts take turns, which bounds how fast a
	// password can be guessed without locking its owner out.
	loginMu sync.Mutex

	// proxyTokens caches each computer's egress proxy token, read from its
	// container the first time the proxy is asked about it.
	proxyTokens proxyTokens

	// gate decides every tool call of every agent loop, the home session's
	// and the subagents' alike; newAgent hands it to each.
	gate sentry.Gate
	// canary is the fake key planted in the home computer, which the gate
	// halts Eika for.
	canary canaryHolder
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
	if deps.Store != nil {
		o := sentry.Options{Audit: deps.Store, Halts: deps.Store, Withheld: deps.Store, Marks: deps.Store, Destinations: deps.Store, TrustedSource: deps.Store.IsTrustedSource, BlockedHost: deps.Store.IsBlockedHost, Threshold: cfg.ScreenThreshold, Log: log}
		if cfg.GuardURL != "" {
			o.Screen = screen.New(cfg.GuardURL)
		}
		o.ScreenSettings = s.screenSettings
		o.MemoryThreshold = s.memoryScreenThreshold
		o.WatchThreshold = s.watchThreshold
		o.GuardChanged = func(up bool) {
			if s.notifications != nil {
				message := "The guard is unavailable. Untrusted content cannot be screened."
				if up {
					message = "The guard is available again."
				}
				s.notifications.send(message)
			}
		}
		o.Canary = s.canary.get
		o.Halter = func(ctx context.Context, reason string) error {
			_, err := s.haltWith(ctx, reason, func() { go s.runs.abortAll() })
			return err
		}
		s.ensureCanary(context.Background())
		s.gate = sentry.New(o)
	}
	s.inference = inference.New(log)
	s.counters = newSessionCounters()
	s.runs = newRuns(s)
	s.notices = newNotices(s)
	s.notifications = newNotifications(s)
	if deps.Store != nil {
		// Subagents outlive the home turn that started them, so their runs
		// are bounded by the server, not by a request.
		s.subagents = subagent.New(context.Background(), deps.Store, subagent.Options{
			Run: s.runSubagent, Final: s.finalMessage, Limit: s.subagentLimit, Reported: s.subagentReported,
			Ended: s.subagentEnded, Started: s.subagentStarted,
			Screen: s.screenReport,
			Events: deps.Bus, Log: log,
		})
	}
	s.titles = newTitles(s)
	s.idle = newIdleComputers(s)
	s.routes()
	return s
}

// Handler returns the server's root handler.
func (s *Server) Handler() http.Handler { return s.mux }

// Bus returns the event bus the server fans events out on, which is the
// emitter every run writes to.
func (s *Server) Bus() *event.Bus { return s.deps.Bus }

// Run serves until ctx is cancelled, then drains in-flight requests and stops
// the runs that are still going. While it serves, it makes sure the home
// computer exists, probes every provider's endpoint once (a failed probe
// keeps the earlier result and never stops the server), and stops the
// computers that go unused for the idle timeout.
func (s *Server) Run(ctx context.Context) error {
	defer s.Close()
	if s.deps.Store != nil {
		if _, err := s.deps.Store.EnsureHomeSession(ctx); err != nil {
			return err
		}
		if err := s.reconcileSessions(ctx); err != nil {
			return fmt.Errorf("reconcile sessions: %w", err)
		}
		if err := s.deps.Store.RecoverSchedule(ctx); err != nil {
			return fmt.Errorf("recover scheduler: %w", err)
		}
		if err := s.recoverRoutineNotices(ctx); err != nil {
			return fmt.Errorf("recover routine notices: %w", err)
		}
		// The queue knows its pools before the first run asks for a lease.
		s.syncInference(ctx)
		watchCtx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		wg.Go(func() { s.watchIdleCompaction(watchCtx) })
		wg.Go(func() { (schedule.Engine{Store: s.deps.Store, Runner: scheduleRunner{s}, Log: s.log}).Run(watchCtx) })
		if s.deps.Providers != nil {
			wg.Go(func() { s.probeProviders(watchCtx) })
		}
		if s.deps.Computers != nil {
			wg.Go(func() { s.idle.watch(watchCtx) })
			wg.Go(func() { s.keepHome(watchCtx, firstHomeRetry) })
			wg.Go(func() { s.keepMemory(watchCtx, firstHomeRetry) })
			wg.Go(func() { s.keepSkillsInstaller(watchCtx, firstHomeRetry) })
		}
		// The sweep stops before Close, whose runs it would otherwise race.
		defer wg.Wait()
		defer cancel()
	}
	return Serve(ctx, s.cfg.Listen, s.Handler(), s.log)
}

// Close aborts every run and session title that is still going and returns
// once they have stopped. Run calls it on shutdown; a caller that serves
// Handler itself calls it when it is done, before the store it gave the
// server closes. Notices stop starting background turns first, so that the
// runs being stopped start none; subagents stop next, so each records its
// run cancelled while the run manager still takes the abort; titles stop
// after the runs, since no run is left to start one.
func (s *Server) Close() {
	s.notices.close()
	if s.subagents != nil {
		s.subagents.Close()
	}
	s.runs.stopAll()
	s.notifications.close()
	s.titles.stop()
}

// Reconcile repairs interrupted sessions and recorded computer states.
// Call it only before serving requests or starting new runs. Recovered
// notices wait for a later home turn.
func (s *Server) Reconcile(ctx context.Context) error {
	if s.deps.Store == nil {
		return nil
	}
	if err := s.reconcileSessions(ctx); err != nil {
		return err
	}
	return s.reconcileComputers(ctx)
}

func (s *Server) reconcileComputers(ctx context.Context) error {
	if s.deps.Computers == nil {
		return nil
	}
	if !s.deps.Computers.EgressControl() {
		s.log.Warn("computer egress is uncontrolled; computers have direct network routes")
	}
	recorded, err := s.deps.Store.Computers(ctx)
	if err != nil {
		return err
	}
	type observedComputer struct {
		state       computer.State
		containerID string
	}
	observed := make(map[string]observedComputer, len(recorded))
	var reconcileErr error
	for _, w := range recorded {
		host, err := s.deps.Computers.Inspect(ctx, w.ID)
		if err == nil {
			if host.State == computer.StateRunning {
				if err := s.deps.Computers.Confine(ctx, &host, s.confinement(w.Sandbox)); err != nil {
					s.log.Error("confine computer; stopping it", "computer_id", w.ID, "error", err)
					s.notifications.send("A computer could not be confined; Eika is stopping it")
					if stopErr := s.deps.Computers.Stop(ctx, &host); stopErr != nil {
						s.log.Error("stop unconfined computer", "computer_id", w.ID, "error", stopErr)
						reconcileErr = errors.Join(reconcileErr, fmt.Errorf("stop unconfined computer %s: %w", w.ID, stopErr))
						continue
					}
				}
			}
			observed[w.ID] = observedComputer{state: host.State, containerID: host.ContainerID}
			continue
		}
		if errors.Is(err, computer.ErrNoComputer) {
			observed[w.ID] = observedComputer{state: computer.StateGone}
			continue
		}
		reconcileErr = errors.Join(reconcileErr, fmt.Errorf("reconcile computer %s: %w", w.ID, err))
	}
	for _, w := range recorded {
		actual, ok := observed[w.ID]
		if !ok {
			continue
		}
		state := string(actual.state)
		if state == w.State {
			continue
		}
		if err := s.deps.Store.SetComputerState(ctx, w.ID, state, actual.containerID); err != nil {
			return err
		}
		s.computerState(ctx, w.ID, state)
		s.log.Info("computer state reconciled", "computer_id", w.ID, "was", w.State, "state", state)
	}
	return reconcileErr
}

// computerState reports a computer lifecycle transition to the clients
// watching that computer.
func (s *Server) computerState(ctx context.Context, computerID, state string) {
	e, err := event.New(event.TypeComputerState, event.ComputerTopic(computerID), event.ComputerState{
		ComputerID: computerID,
		State:      state,
	})
	if err != nil {
		s.log.Error("encode computer state event", "computer_id", computerID, "error", err)
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
	err = srv.Shutdown(shutdownCtx)
	// Shutdown makes Serve return at once, so this wait is short.
	<-serveErr
	if err != nil {
		return fmt.Errorf("shut down server on %s: %w", addr, err)
	}
	log.Info("http server stopped", "addr", addr)
	return nil
}
