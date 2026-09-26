package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/egress"
	"github.com/erlidev/eika/internal/event"
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
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
	"github.com/erlidev/eika/internal/tool/builtin"
	"github.com/erlidev/eika/internal/workspace"
	"github.com/erlidev/eika/internal/workspace/hub"
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

	// The key that seals credentials lives beside the hub, not in the
	// database, so a copy of the database alone opens none of them.
	secrets, err := secret.Load(cfg.SecretKeyFile)
	if err != nil {
		return err
	}
	repos, err := hub.New(cfg.HubRoot, log)
	if err != nil {
		return err
	}
	host, err := workspace.NewHost(workspace.Options{
		DockerSocket: cfg.DockerSocket,
		Hub:          repos,
		HubURL:       cfg.HubURL,
		Image:        cfg.SandboxImage,
		Network:      cfg.SandboxNetwork,
		EikadBinary:  cfg.EikadBinary,
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

	engine, pages, err := buildSearch(ctx, cfg, st, secrets, log)
	if err != nil {
		return err
	}

	questions := builtin.NewQuestions()
	bus := event.NewBus(log)
	// The spawner and the server need each other: the tools every run shares
	// hold spawn_agent, and the spawner drives a child through the run
	// manager. The spawner is built first and given the runner afterwards.
	spawner := subagent.New(subagent.Options{
		Store:      st,
		Workspaces: host,
		Emitter:    bus,
		Limits:     subagentLimits(st, log),
		Sandbox:    ChildSandbox,
		Logger:     log,
	})
	tools, err := builtin.Registry(builtin.Deps{Questions: questions, Agents: spawner, Search: engine, Pages: pages})
	if err != nil {
		return err
	}
	// The pool closes after the server, whose runs call its tools, and
	// before the store it reads.
	pool := NewMCP(cfg, st, secrets, host, bus, log)
	defer pool.Close()
	// The proxy asks the server for a workspace's policy, and the server
	// reports what the proxy refused, so the proxy reaches the server
	// through a variable set just below.
	var s *Server
	var proxy *egress.Proxy
	if host.EgressControl() {
		proxy = egress.New(egress.Options{Policy: func(ctx context.Context, id string) (egress.Policy, error) {
			return s.EgressPolicy(ctx, id)
		}}, log)
	}
	s = New(cfg, log, Deps{
		Store:      st,
		Hub:        repos,
		Workspaces: host,
		Providers:  provider.NewRegistry(openai.New),
		Secrets:    secrets,
		Tools:      tools,
		Questions:  questions,
		Bus:        bus,
		Search:     engine,
		Pages:      pages,
		Egress:     egressDep(proxy),
		MCP:        pool,
	}, opts)
	s.UseSubagents(spawner, spawner.Attach)
	// Remote servers connect in the background, so their tools are there
	// when the first run asks; one that is down must not stop the API.
	if err := pool.Start(ctx); err != nil {
		log.Error("start mcp servers", "error", err)
	}

	// A container may have stopped or been removed while the harness was
	// down. Reconciling is how the recorded states catch up; a Docker daemon
	// that is not there yet must not stop the API from serving.
	if err := s.Reconcile(ctx); err != nil {
		log.Error("reconcile workspaces", "error", err)
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

// egressDep hands the server the proxy, or nothing when there is none: a nil
// *egress.Proxy in the interface would not compare equal to nil.
func egressDep(p *egress.Proxy) Egress {
	if p == nil {
		return nil
	}
	return p
}

// buildSearch builds the search engine over the built-in backends and the
// page reader. The sources share one client; fetch has its own, which
// refuses to connect to a non-public address, because its URLs come from the
// model.
func buildSearch(ctx context.Context, cfg config.Config, st *store.Store, secrets *secret.Box, log *slog.Logger) (*search.Engine, *fetch.Reader, error) {
	client := &http.Client{Timeout: search.Timeout}
	return NewSearch(ctx, cfg, st, secrets, log, search.Searchers{
		SearxNG:      web.NewSearxNG(client, cfg.SearxNGURL),
		Exa:          web.NewExa(client),
		Tavily:       web.NewTavily(client),
		Brave:        web.NewBrave(client),
		Marginalia:   web.NewMarginalia(client),
		Wikipedia:    wikipedia.New(client, wikipedia.DefaultURL),
		Arxiv:        arxiv.New(client, arxiv.DefaultURL),
		GitHubCode:   github.New(client, github.DefaultURL, github.Code),
		GitHubRepos:  github.New(client, github.DefaultURL, github.Repos),
		GitHubIssues: github.New(client, github.DefaultURL, github.Issues),
	}, fetch.NewClient())
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
	pages := fetch.New(fetch.Config{Client: pageClient, Tracker: tracker, Keys: backend, Quota: engine})
	return engine, pages, nil
}

var (
	_ Providers           = (*provider.Registry)(nil)
	_ Workspaces          = (*workspace.Host)(nil)
	_ Hub                 = (*hub.Hub)(nil)
	_ Subagents           = (*subagent.Spawner)(nil)
	_ subagent.Runner     = (*Server)(nil)
	_ builtin.Subagents   = (*subagent.Spawner)(nil)
	_ subagent.Store      = (*store.Store)(nil)
	_ subagent.Workspaces = (*workspace.Host)(nil)
	_ builtin.Searcher    = (*search.Engine)(nil)
	_ builtin.PageReader  = (*fetch.Reader)(nil)
	_ fetch.Quota         = (*search.Engine)(nil)
	_ mcp.Store           = mcpStore{}
	_ mcp.Launcher        = mcpLauncher{}
	_ search.Settings     = searchBackend{}
	_ search.Keys         = searchBackend{}
	_ search.UsageStore   = searchBackend{}
)
