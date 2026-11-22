package server

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
)

const (
	layerAgent     = "agent"
	layerClass     = "class"
	layerModel     = "model"
	layerDefault   = "default"
	samplingSource = "sampling."
)

type runConfig struct {
	definition       *agentdef.Definition
	model            store.Model
	modelOK          bool
	chatPrompt       *string
	instructions     string
	preserveThinking bool
	tools            []string
	sampling         provider.Sampling
	droppedEffort    string
	sources          map[string]string
	probedWindow     int
}
type configBase struct {
	agentOverrides map[string]agentdef.Override
	assigned       map[modelClass]string
	models         []store.Model
	defaultModel   store.Model
}

func (s *Server) loadConfigBase(ctx context.Context) (configBase, error) {
	models, err := s.deps.Store.Models(ctx)
	if err != nil {
		return configBase{}, err
	}
	overrides, err := s.deps.Store.AgentOverrides(ctx)
	if err != nil {
		return configBase{}, err
	}
	b := configBase{models: chatModels(models), agentOverrides: overrides, assigned: s.classAssignments(ctx)}
	b.defaultModel, _ = s.defaultModel(ctx, models)
	return b, nil
}
func modelSampling(m store.Model) provider.Sampling {
	var s provider.Sampling
	if m.MaxOutput > 0 {
		s.MaxOutput = new(m.MaxOutput)
	}
	if m.ReasoningEffort != "" {
		s.ReasoningEffort = new(m.ReasoningEffort)
	}
	return s
}
func samplingKeys(s provider.Sampling) []string {
	data, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return nil
	}
	return slices.Sorted(maps.Keys(fields))
}
func (s *Server) configure(ctx context.Context, sess store.Session, requested string) (runConfig, error) {
	b, err := s.loadConfigBase(ctx)
	if err != nil {
		return runConfig{}, err
	}
	return b.resolveSession(sess, requested)
}
func (b configBase) resolveSession(sess store.Session, requested string) (runConfig, error) {
	d, ok := agentdef.Lookup(sess.Agent)
	if !ok {
		return runConfig{}, invalidf("unknown agent %q", sess.Agent)
	}
	if requested != "" {
		return runConfig{}, invalidf("agent sessions use model classes; model must be empty")
	}
	d = b.agentOverrides[sess.Agent].Apply(d)
	choice, found := resolveClass(modelClass(d.Class), b.assigned, b.models, b.defaultModel.Name)
	c := runConfig{definition: &d, model: choice.Model, modelOK: found, chatPrompt: &d.Prompt, tools: d.Tools,
		sources: map[string]string{"model": layerClass, "preserve_thinking": layerDefault, "tools": layerAgent}}
	if found {
		c.preserveThinking = c.model.PreserveThinking
		c.sources["preserve_thinking"] = layerModel
		c.sampling = modelSampling(c.model)
	}
	for _, key := range samplingKeys(c.sampling) {
		c.sources[samplingSource+key] = layerModel
	}
	return c, nil
}
func (c runConfig) parameterSources() map[string]string {
	out := map[string]string{"model": c.sources["model"], "preserve_thinking": c.sources["preserve_thinking"]}
	for key, layer := range c.sources {
		if strings.HasPrefix(key, samplingSource) {
			out[key] = layer
		}
	}
	if c.modelOK {
		out["thinking_switch"], out["image_input"] = layerModel, layerModel
	}
	return out
}

func (s *Server) newAgent(p provider.Provider, sess store.Session, c runConfig, ex executor.Executor,
	mcpTools []tool.Tool, opts agent.Options) *agent.Agent {
	if sess.Kind == store.SessionHome {
		opts.Channel = "web"
		// Home compacts into the board and a rolling conversation, and cuts
		// at a user-turn boundary; subagent and routine sessions keep Pi's
		// summary and cut.
		opts.Compaction.Board = func(ctx context.Context, loc *time.Location) (string, time.Time, error) {
			return s.subagentBoard(ctx, sess.ID, loc)
		}
		opts.Compaction.SummarySpan = func(ctx context.Context, kept int) (int64, int64, int, error) {
			path, err := s.deps.Store.SessionPath(ctx, sess.ID)
			if err != nil {
				return 0, 0, 0, err
			}
			state, err := store.HistoryState(path, kept)
			return state.FirstCompacted, state.LastCompacted, state.Compactions, err
		}
		opts.Compaction.KeepRecentShare = agent.DefaultKeepRecentShare
	}
	opts.Model = c.model.Model
	opts.ContextWindow = c.window()
	opts.Sampling = c.sampling
	opts.ThinkingSwitch = provider.ThinkingSwitch(c.model.ThinkingSwitch)
	opts.PreserveThinking = c.preserveThinking
	opts.ImageInput = c.model.ImageInput
	opts.BasePrompt = c.chatPrompt
	opts.Instructions = c.instructions
	opts.Executor = ex
	opts.Gate = s.gate
	if d := c.definition; d != nil {
		opts.Limits.MaxCalls = d.MaxTurns
		opts.Inference.Class = d.Class
		opts.Compaction.Prompts = agent.CompactionPrompts{
			Summary: d.Compaction.Summary, Update: d.Compaction.Update, TurnPrefix: d.Compaction.TurnPrefix,
		}
		if !s.usesComputer(sess) {
			opts.Executor = nil
		}
	}
	return agent.New(p, s.runTools(sess, c.tools, mcpTools), opts)
}
