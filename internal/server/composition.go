package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/egress"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/arxiv"
	"github.com/erlidev/eika/internal/search/fetch"
	"github.com/erlidev/eika/internal/search/github"
	"github.com/erlidev/eika/internal/search/web"
	"github.com/erlidev/eika/internal/search/wikipedia"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// Run builds the harness from its configuration and serves until ctx is
// cancelled. It is the whole composition of the process: cmd/eika parses
// flags, loads the configuration, and calls this.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger, opts Options) error {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	// The key that seals credentials lives in the data volume, not in the
	// database, so a copy of the database alone opens none of them.
	secrets, err := secret.Load(cfg.SecretKeyFile)
	if err != nil {
		return err
	}
	var s *Server
	host, err := computer.NewHost(computer.Options{
		Plant:                func(ctx context.Context, id string) []string { return s.plantedEnv(ctx, id) },
		DockerSocket:         cfg.DockerSocket,
		Image:                cfg.SandboxImage,
		Network:              cfg.SandboxNetwork,
		DirectNetworkAddress: opts.LiveTest,
		EikadBinary:          cfg.EikadBinary,
		// The internal network is used only beside the sandbox network,
		// which is how a harness outside compose ends up without it.
		InternalNetwork: cfg.SandboxInternalNetwork,
		EgressProxyURL:  cfg.EgressProxyURL,
	}, log)
	if err != nil {
		return err
	}
	defer func() {
		if err := host.Close(); err != nil {
			log.Error("close docker client", "error", err)
		}
	}()

	engine, pages, err := buildSearch(ctx, cfg, st, secrets, log, func() sentry.Gate { return s.gate })
	if err != nil {
		return err
	}

	questions := builtin.NewQuestions()
	bus := event.NewBus(log)
	tools, err := agentToolRegistry(builtin.Deps{Questions: questions, Search: engine, Pages: pages, Projects: st, History: st, ScreenHistory: func(ctx context.Context, segs []sentry.Segment) ([]sentry.Ingested, error) {
		return s.ingestHistory(ctx, segs)
	}}, func() *Server { return s })
	if err != nil {
		return err
	}
	// The pool closes after the server, whose runs call its tools, and
	// before the store it reads.
	pool := NewMCP(cfg, st, secrets, host, bus, log)
	defer pool.Close()
	// The proxy asks the server for a computer's policy, and the server
	// reports what the proxy refused, so the proxy reaches the server
	// through a variable set just below.
	var proxy *egress.Proxy
	if host.EgressControl() {
		proxy = egress.New(egress.Options{Control: liveProxyControl(opts.LiveTest), Policy: func(ctx context.Context, id string) (egress.Policy, error) {
			return s.EgressPolicy(ctx, id)
		}, Halted: func() bool { return s.gate.Halted().Halted },
			Run: func(id string) (string, string) { return s.runs.onComputer(id) },
			Report: func(r egress.Report) {
				s.gate.Observe(context.Background(), sentry.Observation{Kind: sentry.ObserveEgress, Session: r.Session, Run: r.Run,
					Computer: r.Computer, Host: r.Host, Sent: r.Sent, Received: r.Received, Closed: r.Closed})
			},
			Decide: func(ctx context.Context, id, host, key, reason string) (bool, error) {
				session, run := s.runs.onComputer(id)
				v, err := s.gate.Decide(ctx, sentry.Action{Key: key, Principal: sentry.Principal{Session: session, Run: run, Computer: id}, Kind: sentry.KindEgress, Name: host, Destination: host, EgressDenial: reason})
				return v.Decision == sentry.Allow, err
			}}, log)
	}
	s = New(cfg, log, Deps{
		Store:     st,
		Computers: host,
		Providers: provider.NewRegistry(openai.New, openai.NewResponses),
		Secrets:   secrets,
		Tools:     tools,
		Questions: questions,
		Bus:       bus,
		Search:    engine,
		Pages:     pages,
		Egress:    proxy,
		MCP:       pool,
	}, opts)
	// Remote servers connect in the background, so their tools are there
	// when the first run asks; one that is down must not stop the API.
	if err := pool.Start(ctx); err != nil {
		log.Error("start mcp servers", "error", err)
	}

	// A container may have stopped or been removed while the harness was
	// down. Reconciling is how the recorded states catch up; a Docker daemon
	// that is not there yet stops a controlled host from serving until it can
	// verify that its computers are confined.
	if err := s.reconcileComputers(ctx); err != nil {
		if host.EgressControl() {
			return err
		}
		log.Error("reconcile computers", "error", err)
	}
	if proxy != nil {
		// A proxy that cannot listen leaves restricted sandboxes with no way
		// out, which is safe; the API still serves.
		done := make(chan error, 1)
		go func() { done <- Serve(ctx, cfg.EgressListen, proxy, log) }()
		defer func() {
			proxy.Close()
			if err := <-done; err != nil {
				log.Error("serve egress proxy", "error", err)
			}
		}()
	}
	return s.Run(ctx)
}

// buildSearch builds the search engine over the built-in backends and the
// page reader. Hosted sources share a guarded client; fetch has its own.
// SearXNG uses a client that exempts its configured local origin.
func buildSearch(ctx context.Context, cfg config.Config, st *store.Store, secrets *secret.Box, log *slog.Logger, gate func() sentry.Gate) (*search.Engine, *fetch.Reader, error) {
	client := coreWebClient(gate, "")
	client.Timeout = search.Timeout
	searxClient := coreWebClient(gate, cfg.SearxNGURL)
	searxClient.Timeout = search.Timeout
	return NewSearch(ctx, cfg, st, secrets, log, search.Searchers{
		SearxNG:      web.NewSearxNG(searxClient, cfg.SearxNGURL),
		Exa:          web.NewExa(client),
		Tavily:       web.NewTavily(client),
		Brave:        web.NewBrave(client),
		Marginalia:   web.NewMarginalia(client),
		Wikipedia:    wikipedia.New(client, wikipedia.DefaultURL),
		Arxiv:        arxiv.New(client, arxiv.DefaultURL),
		GitHubCode:   github.New(client, github.DefaultURL, github.Code),
		GitHubRepos:  github.New(client, github.DefaultURL, github.Repos),
		GitHubIssues: github.New(client, github.DefaultURL, github.Issues),
	}, coreWebClient(gate, "", cfg.FetchFixtureHosts...))
}

// NewSearch builds the search engine over searchers, and the page reader
// over pageClient, both reading their settings, sealed keys, and usage
// through the store. The wiring passes the real backends; a test passes
// scripted ones.
func NewSearch(ctx context.Context, cfg config.Config, st *store.Store, secrets *secret.Box, log *slog.Logger,
	searchers search.Searchers, pageClient *http.Client) (*search.Engine, *fetch.Reader, error) {
	backend := searchBackend{store: st, secrets: secrets, log: log}
	tracker, err := search.NewTracker(ctx, backend, log)
	if err != nil {
		return nil, nil, err
	}
	engine, err := search.NewEngine(search.Config{
		Backends:   search.Registry(searchers),
		Tracker:    tracker,
		Settings:   backend,
		Keys:       backend,
		SearxNGURL: cfg.SearxNGURL,
		Log:        log,
	})
	if err != nil {
		return nil, nil, err
	}
	pages := fetch.New(fetch.Config{FixtureHosts: cfg.FetchFixtureHosts, Client: pageClient, Tracker: tracker, Keys: backend, Quota: engine})
	return engine, pages, nil
}

var (
	_ Providers          = (*provider.Registry)(nil)
	_ Computers          = (*computer.Host)(nil)
	_ builtin.Searcher   = (*search.Engine)(nil)
	_ builtin.PageReader = (*fetch.Reader)(nil)
	_ fetch.Quota        = (*search.Engine)(nil)
	_ mcp.Store          = mcpStore{}
	_ mcp.Launcher       = mcpLauncher{}
	_ search.Settings    = searchBackend{}
	_ search.Keys        = searchBackend{}
	_ search.UsageStore  = searchBackend{}
)

// agentToolRegistry binds project status reads to the server's home computer,
// and the delegation and report tools to its subagents.
func agentToolRegistry(deps builtin.Deps, server func() *Server) (*tool.Registry, error) {
	deps.HomeExecutor = func(ctx context.Context, sessionID string) (executor.Executor, func(), error) {
		return server().projectExecutor(ctx, sessionID)
	}
	deps.Subagents = delegation{server: server}
	deps.QuestionRelay = delegation{server: server}.AskSubagent
	deps.Reports = delegation{server: server}
	deps.Memory = memoryService{server: server}
	deps.MemoryCheck = memoryCheck{server: server}
	deps.Skills = skillService{server: server}
	deps.Schedule = func(ctx context.Context, sessionID, name string, raw json.RawMessage) (string, error) {
		return server().scheduleTool(ctx, sessionID, name, raw)
	}
	return builtin.Registry(deps)
}
