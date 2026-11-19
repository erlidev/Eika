package server

import (
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/agentdef"
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
			names := func(reg *tool.Registry) []string {
				var out []string
				for _, entry := range reg.List() {
					out = append(out, entry.Name())
				}
				return out
			}
			reg := s.runTools(sess, nil, []tool.Tool{namedTool{name: "mcp__secret__read"}})
			if got := names(reg); !slices.Equal(got, want) {
				t.Fatalf("tools = %v, want %v", got, want)
			}
			if !reflect.DeepEqual(reg.Schemas(), s.runTools(sess, nil, nil).Schemas()) {
				t.Fatal("schema changed with MCP")
			}
			// A pinned list narrows the definition's and never widens it.
			everything := append(slices.Clone(want), "bash", "ask_user", "project_create", "subagent_start", "mcp__secret__*", "mcp__secret__read")
			if got := names(s.runTools(sess, everything, []tool.Tool{namedTool{name: "mcp__secret__read"}})); !slices.Equal(got, want) {
				t.Fatalf("tools with a wider pinned list = %v, want %v", got, want)
			}
			if got := names(s.runTools(sess, want[:1], nil)); !slices.Equal(got, want[:1]) {
				t.Fatalf("tools with a narrower pinned list = %v, want %v", got, want[:1])
			}
			if got := names(s.runTools(sess, []string{}, nil)); len(got) != 0 {
				t.Fatalf("tools with an empty pinned list = %v, want none", got)
			}
			if slices.Contains(want, "ask_user") != (name != "eika") {
				t.Fatal("only subagents can ask Eika questions")
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

func TestProjectAndDelegationToolsAreOnlyAvailableThroughEikaDefinition(t *testing.T) {
	s := runtimeServer(t)
	projectNames := []string{"project_create", "project_list", "project_open", "project_update",
		"subagent_cancel", "subagent_message", "subagent_start", "subagent_status"}
	for _, name := range projectNames {
		if _, ok := s.allTools().Get(name); ok {
			t.Errorf("legacy tool list contains %s", name)
		}
		if _, err := s.checkToolChoice(t.Context(), []string{name}, false); err == nil {
			t.Errorf("legacy choice accepts %s", name)
		}
		for _, sess := range []store.Session{{}, {ComputerID: "computer"}} {
			for _, choice := range [][]string{nil, {name}} {
				if _, ok := s.runTools(sess, choice, nil).Get(name); ok {
					t.Errorf("legacy run offers %s", name)
				}
			}
		}
		for _, agent := range []string{"eika", "worker", "researcher"} {
			sess := store.Session{Agent: agent, ComputerID: "computer"}
			_, ok := s.runTools(sess, nil, nil).Get(name)
			if ok != (agent == "eika") {
				t.Errorf("%s offers %s: %v", agent, name, ok)
			}
		}
	}
}

func TestBorrowedComputerIsInUseUntilLastRelease(t *testing.T) {
	s := runtimeServer(t)
	s.idle = newIdleComputers(s)
	s.runs = newRuns(s)
	active, err := s.runs.reserve("home", "", job{}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer s.runs.abandon(active)
	first, err := s.runs.borrowComputer("home", "computer")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.runs.borrowComputer("home", "computer")
	if err != nil {
		t.Fatal(err)
	}
	if !s.idle.inUse(store.Computer{ID: "computer"}) {
		t.Fatal("borrowed computer is idle")
	}
	first()
	if !s.idle.inUse(store.Computer{ID: "computer"}) {
		t.Fatal("second borrower lost its lease")
	}
	second()
	if s.idle.inUse(store.Computer{ID: "computer"}) {
		t.Fatal("released computer is still in use")
	}
	if _, err := s.runs.borrowComputer("missing", "computer"); err == nil {
		t.Fatal("borrow without a run succeeded")
	}
}

func TestABackgroundTurnIsKnownAsOneFromTheMomentItHoldsTheSession(t *testing.T) {
	// A message posted while a background turn is being built reads the
	// reservation from another goroutine; it must see the turn as one it can
	// join, never a plain run in its way.
	s := runtimeServer(t)
	s.idle = newIdleComputers(s)
	s.runs = newRuns(s)
	seen := make(chan bool)
	go func() {
		for {
			if active := s.runs.active("home"); active != nil {
				seen <- active.notify
				return
			}
			runtime.Gosched()
		}
	}()
	active, err := s.runs.reserve("home", "", job{notify: true}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer s.runs.abandon(active)
	if !<-seen {
		t.Fatal("the reservation was seen before it was marked a background turn")
	}
}
