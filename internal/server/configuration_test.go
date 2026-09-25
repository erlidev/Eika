package server

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
)

// sampling encodes a sampling layer as the store keeps it.
func sampling(t *testing.T, s provider.Sampling) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("encode sampling: %v", err)
	}
	return data
}

func TestResolveTakesEachValueFromTheTopLayerThatSetsIt(t *testing.T) {
	small := store.Model{ID: "m1", Name: "small", MaxOutput: 1024, ReasoningEffort: "low", ReasoningEfforts: []string{"low", "high"}}
	large := store.Model{ID: "m2", Name: "large", MaxOutput: 8192, ReasoningEfforts: []string{"minimal", "high"}}
	base := configBase{models: []store.Model{small, large}, defaultModel: small}
	profile := store.Profile{ID: "p1", Name: "Careful"}

	cases := []struct {
		name    string
		layers  []configLayer
		model   string
		prompt  *string
		files   bool
		tools   []string
		want    provider.Sampling
		dropped string
		sources map[string]string
	}{
		{
			name:  "nothing set is the default model's row and the built-in prompts",
			model: "small", files: true,
			want: provider.Sampling{MaxOutput: new(1024), ReasoningEffort: new("low")},
			sources: map[string]string{
				"profile": layerDefault, "model": layerDefault, "workspace_prompt": layerDefault, "chat_prompt": layerDefault,
				"instructions": layerDefault, "context_files": layerDefault, "tools": layerDefault,
				"sampling.max_output": layerModel, "sampling.reasoning_effort": layerModel,
			},
		},
		{
			name: "the session wins over the profile, which wins over the model row",
			layers: []configLayer{
				{name: layerSession, settings: store.ProfileSettings{
					Sampling: sampling(t, provider.Sampling{Temperature: new(0.2)}),
				}, tools: []string{"bash"}},
				{name: layerProfile, settings: store.ProfileSettings{
					ModelID:         "m2",
					WorkspacePrompt: new("be brief"),
					ContextFiles:    new(false),
					Sampling:        sampling(t, provider.Sampling{Temperature: new(0.9), MaxOutput: new(100)}),
				}, tools: []string{"ask_user"}},
			},
			model: "large", prompt: new("be brief"), files: false, tools: []string{"bash"},
			want: provider.Sampling{Temperature: new(0.2), MaxOutput: new(100)},
			sources: map[string]string{
				"profile": layerDefault, "model": layerProfile, "workspace_prompt": layerProfile, "chat_prompt": layerDefault,
				"instructions": layerDefault, "context_files": layerProfile, "tools": layerSession,
				"sampling.temperature": layerSession, "sampling.max_output": layerProfile,
			},
		},
		{
			name: "the request's model wins over every layer",
			layers: []configLayer{
				{name: layerRequest, settings: store.ProfileSettings{ModelID: "m1"}},
				{name: layerSession, settings: store.ProfileSettings{ModelID: "m2"}},
			},
			model: "small", files: true,
			want: provider.Sampling{MaxOutput: new(1024), ReasoningEffort: new("low")},
			sources: map[string]string{
				"profile": layerDefault, "model": layerRequest, "workspace_prompt": layerDefault, "chat_prompt": layerDefault,
				"instructions": layerDefault, "context_files": layerDefault, "tools": layerDefault,
				"sampling.max_output": layerModel, "sampling.reasoning_effort": layerModel,
			},
		},
		{
			name: "an effort the model does not offer falls through to the row's own",
			layers: []configLayer{{name: layerProfile, settings: store.ProfileSettings{
				Sampling: sampling(t, provider.Sampling{ReasoningEffort: new("max")}),
			}}},
			model: "small", files: true, dropped: "max",
			want: provider.Sampling{MaxOutput: new(1024), ReasoningEffort: new("low")},
			sources: map[string]string{
				"profile": layerDefault, "model": layerDefault, "workspace_prompt": layerDefault, "chat_prompt": layerDefault,
				"instructions": layerDefault, "context_files": layerDefault, "tools": layerDefault,
				"sampling.max_output": layerModel, "sampling.reasoning_effort": layerModel,
			},
		},
		{
			name: "an effort the model offers is sent, and the empty one leaves it to the endpoint",
			layers: []configLayer{
				{name: layerSession, settings: store.ProfileSettings{Sampling: sampling(t, provider.Sampling{ReasoningEffort: new("")})}},
				{name: layerProfile, settings: store.ProfileSettings{Sampling: sampling(t, provider.Sampling{ReasoningEffort: new("high")})}},
			},
			model: "small", files: true,
			want: provider.Sampling{MaxOutput: new(1024), ReasoningEffort: new("")},
			sources: map[string]string{
				"profile": layerDefault, "model": layerDefault, "workspace_prompt": layerDefault, "chat_prompt": layerDefault,
				"instructions": layerDefault, "context_files": layerDefault, "tools": layerDefault,
				"sampling.max_output": layerModel, "sampling.reasoning_effort": layerSession,
			},
		},
		{
			name: "an empty stop list clears the one below",
			layers: []configLayer{
				{name: layerSession, settings: store.ProfileSettings{Sampling: sampling(t, provider.Sampling{Stop: []string{}})}},
				{name: layerProfile, settings: store.ProfileSettings{Sampling: sampling(t, provider.Sampling{Stop: []string{"END"}})}},
			},
			model: "small", files: true,
			want: provider.Sampling{Stop: []string{}, MaxOutput: new(1024), ReasoningEffort: new("low")},
			sources: map[string]string{
				"profile": layerDefault, "model": layerDefault, "workspace_prompt": layerDefault, "chat_prompt": layerDefault,
				"instructions": layerDefault, "context_files": layerDefault, "tools": layerDefault,
				"sampling.stop": layerSession, "sampling.max_output": layerModel, "sampling.reasoning_effort": layerModel,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := base.resolve(profile, layerDefault, c.layers)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got.model.Name != c.model {
				t.Errorf("model = %q, want %q", got.model.Name, c.model)
			}
			if !reflect.DeepEqual(got.workspacePrompt, c.prompt) || got.contextFiles != c.files {
				t.Errorf("prompt, context files = %v, %v; want %v, %v", got.workspacePrompt, got.contextFiles, c.prompt, c.files)
			}
			if !reflect.DeepEqual(got.tools, c.tools) {
				t.Errorf("tools = %v, want %v", got.tools, c.tools)
			}
			if !reflect.DeepEqual(got.sampling, c.want) {
				t.Errorf("sampling = %s, want %s", encoded(got.sampling), encoded(c.want))
			}
			if got.droppedEffort != c.dropped {
				t.Errorf("dropped effort = %q, want %q", got.droppedEffort, c.dropped)
			}
			if !maps.Equal(got.sources, c.sources) {
				t.Errorf("sources = %v\nwant %v", got.sources, c.sources)
			}
		})
	}
}

func TestInheritedIsTheLayerSettingNothing(t *testing.T) {
	small := store.Model{ID: "m1", Name: "small", MaxOutput: 1024}
	large := store.Model{ID: "m2", Name: "large", MaxOutput: 8192}
	base := configBase{models: []store.Model{small, large}, defaultModel: small}
	profile := store.Profile{ID: "p1", Name: "Careful"}
	layers := []configLayer{
		{name: layerSession, settings: store.ProfileSettings{
			ModelID:      "m2",
			Instructions: new("mine"),
			Sampling:     sampling(t, provider.Sampling{MaxOutput: new(50)}),
		}},
		{name: layerProfile, settings: store.ProfileSettings{Instructions: new("the profile's")}},
	}
	got, err := base.inherited(profile, layerSession, layers, 0)
	if err != nil {
		t.Fatalf("inherited: %v", err)
	}
	// The model the session would inherit is the default, but its unset
	// max output falls through to the row of the model it chose.
	if got.model.Name != "small" || got.sources["model"] != layerDefault {
		t.Errorf("inherited model = %q from %s, want small from the default", got.model.Name, got.sources["model"])
	}
	if deref(got.sampling.MaxOutput) != 8192 || got.sources["sampling.max_output"] != layerModel {
		t.Errorf("inherited max output = %s, want the chosen model's row", encoded(got.sampling))
	}
	if got.instructions != "the profile's" || got.sources["instructions"] != layerProfile {
		t.Errorf("inherited instructions = %q, want the profile's", got.instructions)
	}
}

func TestAnUnknownRequestedModelIsRefused(t *testing.T) {
	base := configBase{models: []store.Model{{ID: "m1", Name: "small"}}}
	if _, _, _, err := base.sessionLayers(store.Session{}, "typo"); err == nil {
		t.Error("a request for a model that does not exist was accepted")
	}
}

// namedTool is a tool with a name and nothing else.
type namedTool struct {
	tool.Tool
	name string
}

func (n namedTool) Name() string { return n.name }

func TestToolChosen(t *testing.T) {
	bash := namedTool{name: "bash"}
	cases := []struct {
		name   string
		choice []string
		want   bool
	}{
		{"no choice takes every tool", nil, true},
		{"an empty choice takes none", []string{}, false},
		{"a choice takes the tool it names", []string{"ask_user", "bash"}, true},
		{"a choice leaves out one it does not name", []string{"ask_user"}, false},
		{"a server entry takes no built-in tool", []string{"mcp__bash__*"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := toolChosen(c.choice, bash); got != c.want {
				t.Errorf("toolChosen(%v, bash) = %v, want %v", c.choice, got, c.want)
			}
		})
	}
}

func TestChoiceServer(t *testing.T) {
	cases := []struct {
		entry  string
		server string
		ok     bool
	}{
		{"mcp__docs__*", "docs", true},
		{"mcp__my-server__*", "my-server", true},
		{"mcp__docs__search", "", false},
		{"mcp____*", "", false},
		{"bash", "", false},
		{"mcp_list_resources", "", false},
	}
	for _, c := range cases {
		t.Run(c.entry, func(t *testing.T) {
			server, ok := choiceServer(c.entry)
			if server != c.server || ok != c.ok {
				t.Errorf("choiceServer(%q) = %q, %v; want %q, %v", c.entry, server, ok, c.server, c.ok)
			}
		})
	}
}

func TestNewAgentAppliesTheConfiguration(t *testing.T) {
	s := &Server{}
	c := runConfig{
		model:           store.Model{Model: "vendor/m", ContextWindow: 4096, ThinkingSwitch: "thinking", PreserveThinking: true},
		modelOK:         true,
		workspacePrompt: new("workspace rules"),
		chatPrompt:      new("chat rules"),
		instructions:    "be kind",
		contextFiles:    true,
		sampling:        provider.Sampling{Temperature: new(0.3)},
	}
	preview, err := s.newAgent(nil, store.Session{}, c, nil, nil, agent.Options{}).Preview(context.Background(), agent.NewSession("s1", ""))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if got := preview.System(); got != "chat rules\n\nbe kind" {
		t.Errorf("a chat's system prompt = %q, want its prompt and the instructions", got)
	}
	want := agent.Parameters{
		Model:            "vendor/m",
		Sampling:         provider.Sampling{Temperature: new(0.3)},
		ThinkingSwitch:   provider.SwitchThinking,
		PreserveThinking: true,
	}
	if !reflect.DeepEqual(preview.Parameters, want) {
		t.Errorf("parameters = %s, want %s", encoded(preview.Parameters), encoded(want))
	}
}

// encoded is v as JSON, for a message.
func encoded(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// deref returns what p points to, or the zero value for nil.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
