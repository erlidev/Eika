package server

import (
	"context"
	"log/slog"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
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
	}, log)
	if err != nil {
		return err
	}
	defer func() {
		if err := host.Close(); err != nil {
			log.Error("close docker client", "error", err)
		}
	}()

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
		Logger:     log,
	})
	tools, err := builtin.Registry(questions, spawner)
	if err != nil {
		return err
	}
	s := New(cfg, log, Deps{
		Store:      st,
		Hub:        repos,
		Workspaces: host,
		Providers:  provider.NewRegistry(openai.New),
		Secrets:    secrets,
		Tools:      tools,
		Questions:  questions,
		Bus:        bus,
	}, opts)
	s.UseSubagents(spawner, spawner.Attach)

	// A container may have stopped or been removed while the harness was
	// down. Reconciling is how the recorded states catch up; a Docker daemon
	// that is not there yet must not stop the API from serving.
	if err := s.Reconcile(ctx); err != nil {
		log.Error("reconcile workspaces", "error", err)
	}
	return s.Run(ctx)
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
)
