package server

import (
	"context"
	"encoding/json"
	"fmt"
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

// The layers a run's configuration is resolved from, top first. A value is
// taken from the first layer that sets it.
const (
	// layerRequest is the model a run request names.
	layerRequest = "request"
	// layerAgent is the effective agent definition.
	layerAgent = "agent"
	// layerClass is the model selected through the agent's class.
	layerClass = "class"
	// layerSession is what a session overrides of its profile, and its tool
	// choice.
	layerSession = "session"
	// layerProfile is the session's profile.
	layerProfile = "profile"
	// layerModel is the model row's own max output and reasoning effort, and
	// the settings only a model has.
	layerModel = "model"
	// layerDefault is what nothing above set: the default model and profile
	// the settings name, the built-in prompts, every tool, and for a
	// sampling parameter, the endpoint's own default.
	layerDefault = "default"
)

// The keys of configurationBody.Sources that are not the name of one of its
// fields.
const (
	// sourceProfile is where the profile came from: the session's choice or
	// the default.
	sourceProfile = "profile"
	// samplingSource prefixes the key of a sampling parameter, as in
	// sampling.temperature.
	samplingSource = "sampling."
)

// configurationBody is a run's configuration on the wire: every value it
// resolves to, and the layer each came from.
type configurationBody struct {
	ProfileID   string `json:"profile_id"`
	ProfileName string `json:"profile_name"`
	// ModelID and Model are the model's id and name, both empty when no
	// model is configured.
	ModelID string `json:"model_id"`
	Model   string `json:"model"`
	// ChatPrompt is the base prompt.
	ChatPrompt   string `json:"chat_prompt"`
	Instructions string `json:"instructions"`
	// PreserveThinking says whether earlier reasoning is replayed.
	PreserveThinking bool `json:"preserve_thinking"`
	// Tools is the tool choice, in the shape of a session's; null is every
	// tool the session can run.
	Tools []string `json:"tools"`
	// Sampling holds the parameters sent; one left out is the endpoint's.
	Sampling provider.Sampling `json:"sampling"`
	// DroppedEffort is a reasoning effort a layer above the model chose that
	// the model does not offer, which is not sent.
	DroppedEffort string `json:"dropped_effort,omitempty"`
	// Sources names the layer each value came from, keyed by the value's
	// field: profile, model, chat_prompt, instructions,
	// preserve_thinking, tools, and sampling.<parameter> for each parameter
	// sent.
	Sources map[string]string `json:"sources"`
}

// configLayer is one layer of a configuration above the model row: the
// settings it sets and its tool choice, nil for none.
type configLayer struct {
	name     string
	settings store.ProfileSettings
	tools    []string
}

// runConfig is a run's configuration, resolved.
type runConfig struct {
	definition    *agentdef.Definition
	profile       store.Profile
	profileSource string
	// model is the model the run uses; ok is false when none is configured.
	model   store.Model
	modelOK bool
	// chatPrompt replaces the built-in base prompt when it is not nil.
	chatPrompt   *string
	instructions string
	// preserveThinking is the model row's switch unless a layer set it.
	preserveThinking bool
	tools            []string
	sampling         provider.Sampling
	droppedEffort    string
	sources          map[string]string
	// probedWindow is the per-request context the model's endpoint reported,
	// zero when it stated none or the endpoint is not known yet (onEndpoint
	// sets it). The window a run is judged against is window().
	probedWindow int
}

// configBase is what resolving a configuration reads from the database:
// the models, the profiles, and the defaults the settings name.
type configBase struct {
	agentOverrides map[string]agentdef.Override
	assigned       map[modelClass]string
	// models are the chat models: the only ones a run can use. The others are
	// kept for refusing them by name.
	models       []store.Model
	notChat      []store.Model
	defaultModel store.Model
	profiles     []store.Profile
	// defaultProfile is the profile a session that chose none runs with.
	defaultProfile store.Profile
}

// loadConfigBase reads what resolving a configuration needs.
func (s *Server) loadConfigBase(ctx context.Context) (configBase, error) {
	models, err := s.deps.Store.Models(ctx)
	if err != nil {
		return configBase{}, err
	}
	profiles, err := s.deps.Store.Profiles(ctx)
	if err != nil {
		return configBase{}, err
	}
	b := configBase{models: chatModels(models), profiles: profiles}
	for _, m := range models {
		if m.Kind != store.ModelKindChat {
			b.notChat = append(b.notChat, m)
		}
	}
	b.defaultModel, _ = s.defaultModel(ctx, models)
	b.defaultProfile = s.defaultProfile(ctx, profiles)
	b.agentOverrides, err = s.deps.Store.AgentOverrides(ctx)
	if err != nil {
		return configBase{}, err
	}
	b.assigned = s.classAssignments(ctx)
	return b, nil
}

// defaultProfile returns the profile a session that chose none runs with:
// the one the default_profile setting names, or the first. The migration
// makes one and the last cannot be deleted, so there always is one.
func (s *Server) defaultProfile(ctx context.Context, profiles []store.Profile) store.Profile {
	if len(profiles) == 0 {
		return store.Profile{}
	}
	var id string
	if readSetting(ctx, s.deps.Store, s.log, settingDefaultProfile, &id) && id != "" {
		for _, p := range profiles {
			if p.ID == id {
				return p
			}
		}
		s.log.Warn("default profile setting names no profile", "profile_id", id)
	}
	return profiles[0]
}

// profileOf returns a session's profile and whether the session chose it
// or it is the default.
func (b configBase) profileOf(sess store.Session) (store.Profile, string) {
	if sess.ProfileID != "" {
		for _, p := range b.profiles {
			if p.ID == sess.ProfileID {
				return p, layerSession
			}
		}
	}
	return b.defaultProfile, layerDefault
}

// model returns the chat model with the given id.
func (b configBase) model(id string) (store.Model, bool) {
	for _, m := range b.models {
		if m.ID == id {
			return m, true
		}
	}
	return store.Model{}, false
}

// checkModelID accepts the id of a chat model, or the empty id for none, and
// refuses any other: one that does not exist, or a decision model, which
// cannot hold a conversation.
func (b configBase) checkModelID(id string) error {
	if _, ok := b.model(id); ok || id == "" {
		return nil
	}
	if i := slices.IndexFunc(b.notChat, func(m store.Model) bool { return m.ID == id }); i >= 0 {
		return notChatModel(b.notChat[i])
	}
	return invalidf("model %s does not exist", id)
}

// notChatModel refuses a model that cannot hold a conversation.
func notChatModel(m store.Model) error {
	return invalidf("model %q is a %s model, which cannot hold a conversation", m.Name, m.Kind)
}

// sessionLayers are the layers of a run of sess, top first: the model the
// run request names, if any, the session's overrides and tool choice, and
// its profile. A requested model that does not exist is refused.
func (b configBase) sessionLayers(sess store.Session, requested string) ([]configLayer, store.Profile, string, error) {
	profile, source := b.profileOf(sess)
	var layers []configLayer
	if requested != "" {
		i := slices.IndexFunc(b.models, func(m store.Model) bool { return m.Name == requested })
		if i < 0 {
			if j := slices.IndexFunc(b.notChat, func(m store.Model) bool { return m.Name == requested }); j >= 0 {
				return nil, store.Profile{}, "", notChatModel(b.notChat[j])
			}
			return nil, store.Profile{}, "", invalidf("unknown model %q", requested)
		}
		layers = append(layers, configLayer{name: layerRequest, settings: store.ProfileSettings{ModelID: b.models[i].ID}})
	}
	layers = append(layers,
		configLayer{name: layerSession, settings: sess.Overrides, tools: sess.Tools},
		configLayer{name: layerProfile, settings: profile.ProfileSettings, tools: profile.Tools},
	)
	return layers, profile, source, nil
}

// resolve is the one rule a run's configuration follows: each value comes
// from the first of layers that sets it, then from the model row, then from
// the defaults. A reasoning effort from above the model row that the model
// does not offer falls through to the row's own.
func (b configBase) resolve(profile store.Profile, profileSource string, layers []configLayer) (runConfig, error) {
	c := runConfig{
		profile:       profile,
		profileSource: profileSource,
		sources: map[string]string{
			sourceProfile:  profileSource,
			"model":        layerDefault,
			"chat_prompt":  layerDefault,
			"instructions": layerDefault,
			"tools":        layerDefault,
		},
	}
	var preserve *bool
	c.model, c.modelOK = b.defaultModel, len(b.models) > 0
	samplings := make([]provider.Sampling, len(layers))
	// The layers are read bottom first, so that the top one to set a value
	// is the last to write it.
	for i, l := range slices.Backward(layers) {
		set := l.settings
		if m, ok := b.model(set.ModelID); ok && set.ModelID != "" {
			c.model, c.modelOK, c.sources["model"] = m, true, l.name
		}
		if set.ChatPrompt != nil {
			c.chatPrompt, c.sources["chat_prompt"] = set.ChatPrompt, l.name
		}
		if set.Instructions != nil {
			c.instructions, c.sources["instructions"] = *set.Instructions, l.name
		}
		if set.PreserveThinking != nil {
			preserve, c.sources["preserve_thinking"] = set.PreserveThinking, l.name
		}
		if l.tools != nil {
			c.tools, c.sources["tools"] = l.tools, l.name
		}
		if len(set.Sampling) > 0 {
			if err := json.Unmarshal(set.Sampling, &samplings[i]); err != nil {
				return runConfig{}, fmt.Errorf("decode the sampling parameters of the %s layer: %w", l.name, err)
			}
		}
	}

	switch {
	case preserve != nil:
		c.preserveThinking = *preserve
	case c.modelOK:
		c.preserveThinking, c.sources["preserve_thinking"] = c.model.PreserveThinking, layerModel
	default:
		c.sources["preserve_thinking"] = layerDefault
	}

	row := modelSampling(c.model)
	resolved := row
	for _, s := range slices.Backward(samplings) {
		resolved = s.Over(resolved)
	}
	for _, key := range samplingKeys(resolved) {
		c.sources[samplingSource+key] = layerModel
		for i, s := range samplings {
			if slices.Contains(samplingKeys(s), key) {
				c.sources[samplingSource+key] = layers[i].name
				break
			}
		}
	}
	effortKey := samplingSource + "reasoning_effort"
	if e := resolved.ReasoningEffort; e != nil && *e != "" && c.sources[effortKey] != layerModel &&
		c.modelOK && !slices.Contains(c.model.ReasoningEfforts, *e) {
		c.droppedEffort = *e
		resolved.ReasoningEffort = row.ReasoningEffort
		delete(c.sources, effortKey)
		if row.ReasoningEffort != nil {
			c.sources[effortKey] = layerModel
		}
	}
	c.sampling = resolved
	return c, nil
}

// inherited is what layer i of layers falls through to: the configuration
// as it would be if that layer set nothing, which is what an editor of the
// layer shows beside a value it leaves unset. The layer's own model choice
// still picks the model row below it, since that is the row its unset
// values come from; the model it would inherit comes from the layers below.
func (b configBase) inherited(profile store.Profile, profileSource string, layers []configLayer, i int) (runConfig, error) {
	own := configLayer{name: layers[i].name, settings: store.ProfileSettings{ModelID: layers[i].settings.ModelID}}
	below := layers[i+1:]
	out, err := b.resolve(profile, profileSource, append([]configLayer{own}, below...))
	if err != nil {
		return runConfig{}, err
	}
	under, err := b.resolve(profile, profileSource, below)
	if err != nil {
		return runConfig{}, err
	}
	out.model, out.modelOK, out.sources["model"] = under.model, under.modelOK, under.sources["model"]
	return out, nil
}

// modelSampling is what a model row sets of the sampling parameters: its
// max output and its reasoning effort.
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

// samplingKeys lists the parameters s sets, by their JSON names: the keys
// its encoding has, since an unset parameter is left out of it.
func samplingKeys(s provider.Sampling) []string {
	data, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil
	}
	return slices.Sorted(maps.Keys(fields))
}

// configure resolves the configuration of a run of sess that names the
// model requested, empty for none.
func (s *Server) configure(ctx context.Context, sess store.Session, requested string) (runConfig, error) {
	b, err := s.loadConfigBase(ctx)
	if err != nil {
		return runConfig{}, err
	}
	return b.resolveSession(sess, requested)
}

// resolveSession resolves the configuration of a run of sess that names the
// model requested, empty for none.
func (b configBase) resolveSession(sess store.Session, requested string) (runConfig, error) {
	if sess.Agent != "" {
		return b.resolveAgent(sess.Agent, requested)
	}
	layers, profile, source, err := b.sessionLayers(sess, requested)
	if err != nil {
		return runConfig{}, err
	}
	return b.resolve(profile, source, layers)
}

// asConfiguration renders a resolved configuration on the wire.
func asConfiguration(c runConfig) configurationBody {
	body := configurationBody{
		ProfileID:        c.profile.ID,
		ProfileName:      c.profile.Name,
		ChatPrompt:       agent.ChatPrompt,
		Instructions:     c.instructions,
		PreserveThinking: c.preserveThinking,
		Tools:            c.tools,
		Sampling:         c.sampling,
		DroppedEffort:    c.droppedEffort,
		Sources:          maps.Clone(c.sources),
	}
	if c.modelOK {
		body.ModelID, body.Model = c.model.ID, c.model.Name
	}
	if c.chatPrompt != nil {
		body.ChatPrompt = *c.chatPrompt
	}
	return body
}

// parameterSources are the sources of what a request sends beside its
// content: the model, the sampling parameters, whether reasoning is
// replayed, and the model row's own thinking switch and image input.
func (c runConfig) parameterSources() map[string]string {
	out := map[string]string{"model": c.sources["model"], "preserve_thinking": c.sources["preserve_thinking"]}
	for key, layer := range c.sources {
		if strings.HasPrefix(key, samplingSource) {
			out[key] = layer
		}
	}
	if c.modelOK {
		out["thinking_switch"] = layerModel
		out["image_input"] = layerModel
	}
	return out
}

// newAgent builds the agent for a run of sess, or for the preview of one,
// with the configuration c resolved: the model, the sampling parameters,
// the system prompt's parts, and the tools the choice takes from those
// every run shares and mcpTools. opts carries what the caller adds:
// the emitter, the store, the recorder, and the logger.
func (s *Server) newAgent(p provider.Provider, sess store.Session, c runConfig, ex executor.Executor,
	mcpTools []tool.Tool, opts agent.Options) *agent.Agent {
	if sess.Kind == store.SessionHome {
		opts.Channel = "web"
		// Home compacts into the board and a rolling conversation;
		// subagent and routine sessions keep Pi's summary.
		opts.Compaction.Board = func(ctx context.Context, loc *time.Location) (string, time.Time, error) {
			return s.subagentBoard(ctx, sess.ID, loc)
		}
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

// resolveAgent uses only the definition and the model row selected by its class.
func (b configBase) resolveAgent(name, requested string) (runConfig, error) {
	d, ok := agentdef.Lookup(name)
	if !ok {
		return runConfig{}, invalidf("unknown agent %q", name)
	}
	if requested != "" {
		return runConfig{}, invalidf("agent sessions use model classes; model must be empty")
	}
	d = b.agentOverrides[name].Apply(d)
	choice, found := resolveClass(modelClass(d.Class), b.assigned, b.models, b.defaultModel.Name)
	b.defaultModel = choice.Model
	if !found {
		b.models = nil
	}
	c, err := b.resolve(store.Profile{}, layerAgent, []configLayer{{
		name: layerAgent, settings: store.ProfileSettings{ChatPrompt: &d.Prompt}, tools: d.Tools,
	}})
	if err != nil {
		return runConfig{}, err
	}
	c.definition = &d
	c.sources["model"] = layerClass
	return c, nil
}
