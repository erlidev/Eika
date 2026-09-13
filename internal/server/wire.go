package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
	"github.com/erlidev/eika/internal/store"
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
	tools, err := builtin.Registry(questions)
	if err != nil {
		return err
	}
	s := New(cfg, log, Deps{
		Store:      st,
		Hub:        repos,
		Workspaces: host,
		Models:     configuredModels{registry: provider.NewRegistry(openai.New), models: cfg.Models},
		Tools:      tools,
		Questions:  questions,
		Bus:        event.NewBus(log),
	}, opts)

	// A container may have stopped or been removed while the harness was
	// down. Reconciling is how the recorded states catch up; a Docker daemon
	// that is not there yet must not stop the API from serving.
	if err := s.Reconcile(ctx); err != nil {
		log.Error("reconcile workspaces", "error", err)
	}
	return s.Run(ctx)
}

// configuredModels is the set of models the configuration declares. It builds
// a provider per run rather than holding one, so that a model's API key is
// read when it is used and a run never shares a client with another.
type configuredModels struct {
	registry *provider.Registry
	models   []config.Model
}

// Names lists the configured model names, in configuration order: the first
// is the default until the user picks one in the settings.
func (m configuredModels) Names() []string {
	out := make([]string, 0, len(m.models))
	for _, model := range m.models {
		out = append(out, model.Name)
	}
	return out
}

// Provider builds the provider for one configured model.
func (m configuredModels) Provider(name string) (provider.Provider, error) {
	for _, model := range m.models {
		if model.Name == name {
			return m.registry.Build("openai", model)
		}
	}
	return nil, fmt.Errorf("build provider: model %s is not configured", name)
}

var (
	_ Models     = configuredModels{}
	_ Workspaces = (*workspace.Host)(nil)
	_ Hub        = (*hub.Hub)(nil)
)
