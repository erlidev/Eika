package server

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

func TestAgentConfigurationIgnoresLegacyLayers(t *testing.T) {
	main := store.Model{ID: "main", Name: "main", Kind: store.ModelKindChat, Model: "vendor/main", MaxOutput: 123, PreserveThinking: true}
	fast := store.Model{ID: "fast", Name: "fast", Kind: store.ModelKindChat, Model: "vendor/fast"}
	b := configBase{models: []store.Model{main, fast}, defaultModel: main,
		assigned:       map[modelClass]string{classMain: "main", classFast: "fast"},
		defaultProfile: store.Profile{ProfileSettings: store.ProfileSettings{ChatPrompt: new("legacy prompt"), Instructions: new("legacy rules"), ModelID: "fast"}, Tools: []string{"mcp__secret__*", "bash"}},
		agentOverrides: map[string]agentdef.Override{"worker": {Class: new("deep"), Prompt: new("")}},
	}
	for _, name := range []string{"eika", "researcher", "worker"} {
		t.Run(name, func(t *testing.T) {
			sess := store.Session{Agent: name, Tools: []string{"mcp__secret__*"}, Overrides: store.ProfileSettings{ModelID: "fast", ChatPrompt: new("session prompt"), Instructions: new("session rules"), PreserveThinking: new(false), Sampling: []byte(`{"temperature":0.9}`)}}
			cfg, err := b.resolveSession(sess, "")
			if err != nil {
				t.Fatal(err)
			}
			d, _ := agentdef.Lookup(name)
			d = b.agentOverrides[name].Apply(d)
			want := main
			if name == "researcher" {
				want = fast
			}
			if !reflect.DeepEqual(cfg.model, want) {
				t.Errorf("model = %+v, want %+v", cfg.model, want)
			}
			if cfg.chatPrompt == nil || *cfg.chatPrompt != d.Prompt || cfg.instructions != "" || cfg.preserveThinking != want.PreserveThinking || cfg.sampling.Temperature != nil {
				t.Fatalf("legacy settings reached agent: %+v", asConfiguration(cfg))
			}
			if !slices.Equal(cfg.tools, d.Tools) || cfg.sources["tools"] != layerAgent || cfg.sources["model"] != layerClass {
				t.Fatalf("capabilities or sources = %+v", asConfiguration(cfg))
			}
			if cfg.definition.Class != d.Class {
				t.Fatalf("class = %s, want %s", cfg.definition.Class, d.Class)
			}
			if _, err := b.resolveSession(sess, "main"); err == nil {
				t.Fatal("explicit model accepted")
			}
		})
	}
	if _, err := b.resolveSession(store.Session{Agent: "missing"}, ""); err == nil {
		t.Fatal("unknown agent accepted")
	}
	// The old path still uses its profile and explicit request model.
	cfg, err := b.resolveSession(store.Session{}, "main")
	if err != nil || cfg.definition != nil || *cfg.chatPrompt != "legacy prompt" || cfg.instructions != "legacy rules" || cfg.model.Name != "main" {
		t.Fatalf("legacy resolution: %+v, %v", cfg, err)
	}
}

func TestAgentClassWithoutAModelDoesNotUseAnUnrelatedModel(t *testing.T) {
	b := configBase{models: []store.Model{{ID: "other", Name: "other", Kind: store.ModelKindChat}}, assigned: map[modelClass]string{classFast: "deleted"}}
	cfg, err := b.resolveSession(store.Session{Agent: "researcher"}, "")
	if err != nil || cfg.modelOK {
		t.Fatalf("resolution = %+v, %v", cfg, err)
	}
}

func runtimeServer(t *testing.T) *Server {
	t.Helper()
	reg, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	return &Server{deps: Deps{Tools: reg}}
}

func TestAgentCapabilitiesAndSchemasAreFixed(t *testing.T) {
	s := runtimeServer(t)
	for _, name := range []string{"eika", "researcher", "worker"} {
		t.Run(name, func(t *testing.T) {
			sess := store.Session{Agent: name, ComputerID: "computer"}
			d, _ := agentdef.Lookup(name)
			want := d.Tools
			for _, choice := range [][]string{nil, {}, {"bash", "ask_user", "mcp__secret__*"}} {
				reg := s.runTools(sess, choice, []tool.Tool{namedTool{name: "mcp__secret__read"}})
				var names []string
				for _, entry := range reg.List() {
					names = append(names, entry.Name())
				}
				if !slices.Equal(names, want) {
					t.Fatalf("tools = %v, want %v", names, want)
				}
				if !reflect.DeepEqual(reg.Schemas(), s.runTools(sess, nil, nil).Schemas()) {
					t.Fatal("schema changed with legacy choice or MCP")
				}
			}
			if slices.Contains(want, "ask_user") {
				t.Fatal("subagent question path is not available in stage A")
			}
		})
	}
	// A worker without a computer cannot advertise file or shell tools.
	reg := s.runTools(store.Session{Agent: "worker"}, nil, nil)
	if _, ok := reg.Get("bash"); ok {
		t.Fatal("computerless worker offered bash")
	}
	if _, ok := s.runTools(store.Session{Agent: "unknown"}, nil, nil).Get("bash"); ok {
		t.Fatal("unknown agent inherited tools")
	}
}

func TestAgentRunDeniesToolsOutsideItsDefinition(t *testing.T) {
	s := runtimeServer(t)
	b := configBase{}
	for _, name := range []string{"eika", "researcher"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := b.resolveSession(store.Session{Agent: name}, "")
			if err != nil {
				t.Fatal(err)
			}
			p := providertest.New(providertest.Calls("", providertest.Call("bad", "bash", map[string]any{"command": "echo forbidden"}), providertest.Call("mcp", "mcp__secret__read", map[string]any{})), providertest.Text("done"))
			ag := s.newAgent(p, store.Session{Agent: name}, cfg, nil, []tool.Tool{namedTool{name: "mcp__secret__read"}}, agent.Options{})
			sess := agent.NewSession("s", "")
			if err := ag.Run(t.Context(), sess, provider.UserMessage("start")); err != nil {
				t.Fatal(err)
			}
			var denied int
			for _, m := range sess.Conversation.Messages() {
				if m.Role == provider.RoleTool && strings.Contains(m.Content, "unknown tool") {
					denied++
				}
			}
			if denied != 2 {
				t.Fatalf("denied results = %d, messages = %+v", denied, sess.Conversation.Messages())
			}
			reqs := p.Requests()
			if reqs[0].System != strings.TrimSpace(*cfg.chatPrompt) {
				t.Fatalf("prompt = %q, want %q", reqs[0].System, *cfg.chatPrompt)
			}
		})
	}
}

func TestAgentRunUsesItsCallLimit(t *testing.T) {
	s := runtimeServer(t)
	for _, name := range []string{"eika", "worker", "researcher"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := (configBase{}).resolveSession(store.Session{Agent: name}, "")
			if err != nil {
				t.Fatal(err)
			}
			steps := make([]providertest.Step, cfg.definition.MaxTurns+1)
			for i := range steps {
				steps[i] = providertest.Calls("", providertest.Call("call", "unavailable", map[string]any{}))
			}
			p := providertest.New(steps...)
			ag := s.newAgent(p, store.Session{Agent: name}, cfg, nil, nil, agent.Options{})
			if err := ag.Run(t.Context(), agent.NewSession("s", ""), provider.UserMessage("go")); err != nil {
				t.Fatal(err)
			}
			if p.Calls() != cfg.definition.MaxTurns {
				t.Fatalf("calls = %d, want %d", p.Calls(), cfg.definition.MaxTurns)
			}
		})
	}
}

func TestAgentCompactionUsesItsOwnPrompts(t *testing.T) {
	s := runtimeServer(t)
	for _, d := range agentdef.Builtins() {
		t.Run(d.Name, func(t *testing.T) {
			cfg, err := (configBase{}).resolveSession(store.Session{Agent: d.Name}, "")
			if err != nil {
				t.Fatal(err)
			}
			p := providertest.New(providertest.Text("summary"), providertest.Text("updated"))
			ag := s.newAgent(p, store.Session{Agent: d.Name}, cfg, nil, nil, agent.Options{Compaction: agent.CompactionSettings{
				KeepRecentTokens: 100, Prompts: agent.CompactionPrompts{Summary: "global summary", Update: "global update", TurnPrefix: "global prefix"},
			}})
			sess := agent.NewSession("s", "")
			add := func() {
				for range 3 {
					sess.Conversation.Append(provider.UserMessage(strings.Repeat("text ", 200)))
					sess.Conversation.Append(provider.AssistantMessage("answer", nil))
				}
			}
			add()
			if err := ag.Compact(t.Context(), sess, ""); err != nil {
				t.Fatal(err)
			}
			add()
			if err := ag.Compact(t.Context(), sess, ""); err != nil {
				t.Fatal(err)
			}
			for i, prompt := range []string{d.Compaction.Summary, d.Compaction.Update} {
				req := p.Requests()[i]
				if got := req.Messages[len(req.Messages)-1].Content; got != prompt {
					t.Fatalf("summary instruction = %q, want %q", got, prompt)
				}
			}
		})
	}
}

// runtimeMCPStore counts attempts to load MCP configuration for a run.
type runtimeMCPStore struct {
	mcp.Store
	reads int
}

func (s *runtimeMCPStore) MCPServers(context.Context) ([]mcp.ServerConfig, error) {
	s.reads++
	return nil, nil
}

func TestAgentRunDoesNotReadMCPConfiguration(t *testing.T) {
	st := &runtimeMCPStore{}
	pool := mcp.NewPool(mcp.Options{Store: st})
	t.Cleanup(pool.Close)
	s := runtimeServer(t)
	s.deps.MCP = pool
	for _, name := range []string{"eika", "worker", "researcher"} {
		for _, choice := range [][]string{nil, {"mcp__private__*"}} {
			if got := s.mcpToolsFor(t.Context(), store.Session{Agent: name, ComputerID: "computer"}, choice); len(got) != 0 {
				t.Fatal("agent received MCP tools")
			}
		}
	}
	if st.reads != 0 {
		t.Fatal("agent run read MCP configuration")
	}
	s.mcpToolsFor(t.Context(), store.Session{}, nil)
	if st.reads != 1 {
		t.Fatal("legacy run did not read MCP configuration")
	}
}
