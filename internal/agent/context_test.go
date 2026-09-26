package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// ptr returns a pointer to v, for the optional fields of the options.
func ptr[T any](v T) *T { return &v }

// workspace returns an executor over a temporary directory holding files,
// keyed by workspace path.
func workspace(t *testing.T, files map[string]string) *local.Executor {
	t.Helper()
	e, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	for path, content := range files {
		if err := e.WriteFile(context.Background(), path, []byte(content)); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return e
}

// registry returns the built-in tools plus extra.
func registry(t *testing.T, extra ...tool.Tool) *tool.Registry {
	t.Helper()
	r, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	for _, x := range extra {
		if err := r.Register(x); err != nil {
			t.Fatalf("register %s: %v", x.Name(), err)
		}
	}
	return r
}

// statFails is a workspace whose every stat fails, so any attempt to read a
// context file fails the call that made it.
type statFails struct{ executor.Executor }

func (statFails) Stat(context.Context, string) (executor.FileInfo, error) {
	return executor.FileInfo{}, errors.New("the workspace is unreachable")
}

// callRecorder keeps the model calls a run reports. For each one it notes how
// many messages the store held at that moment, which is where the call's
// conversation ended in the store.
type callRecorder struct {
	store *agent.MemoryStore
	err   error

	mu     sync.Mutex
	calls  []agent.ModelCall
	stored []int
}

func (r *callRecorder) Record(_ context.Context, c agent.ModelCall) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
	if r.store != nil {
		r.stored = append(r.stored, len(r.store.Messages(c.SessionID)))
	}
	return r.err
}

func (r *callRecorder) recorded() ([]agent.ModelCall, []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agent.ModelCall(nil), r.calls...), append([]int(nil), r.stored...)
}

// jsonOf renders v for a failure message.
func jsonOf(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(data)
}

// kinds lists the kinds of the sections, in order.
func kinds(sections []agent.Section) []agent.SectionKind {
	var out []agent.SectionKind
	for _, s := range sections {
		out = append(out, s.Kind)
	}
	return out
}

func TestPreviewIsTheRequestARunSends(t *testing.T) {
	cases := []struct {
		name    string
		message string
	}{
		{"a run that continues the session", ""},
		{"a run with a new message", "and now?"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			exec := workspace(t, map[string]string{
				"AGENTS.md":     "always run make check",
				"api/AGENTS.md": "keep handlers small",
			})
			search := callTool{name: "mcp__docs__search", run: func(context.Context) string { return "found" }}
			p := providertest.New(providertest.Text("ok"))
			calls := &callRecorder{}
			a := agent.New(p, registry(t, search), agent.Options{
				Model:          "test-model",
				Executor:       exec,
				ContextDir:     "api",
				Instructions:   "Answer in French.",
				ContextWindow:  400_000,
				ThinkingSwitch: provider.SwitchThinking,
				Sampling: provider.Sampling{
					Temperature: ptr(0.3),
					TopK:        ptr(20),
					Stop:        []string{"END"},
					MaxOutput:   ptr(512),
				},
				Recorder: calls,
				Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			s := agent.NewSession("s1", "w1")
			s.Conversation.Append(provider.UserMessage("earlier"))
			prior := provider.AssistantMessageWithReasoning("answer", "a private thought", nil)
			prior.Metrics = &provider.MessageMetrics{RunID: "prior", ContextWindow: 8192}
			s.Conversation.Append(prior)

			preview, err := a.Preview(ctx, s)
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}
			if err := a.Run(ctx, s, c.message); err != nil {
				t.Fatalf("Run: %v", err)
			}

			want := preview.Request()
			if c.message != "" {
				want.Messages = append(want.Messages, provider.UserMessage(c.message))
			}
			sent := p.Requests()[0]
			if !reflect.DeepEqual(sent, want) {
				t.Errorf("sent %s\nwant the preview %s", jsonOf(sent), jsonOf(want))
			}
			recorded, _ := calls.recorded()
			if len(recorded) != 1 {
				t.Fatalf("recorded %d calls, want 1", len(recorded))
			}
			if got := recorded[0].Context.Request(); !reflect.DeepEqual(got, sent) {
				t.Errorf("recorded %s\nwant what was sent %s", jsonOf(got), jsonOf(sent))
			}
			if c.message == "" && !reflect.DeepEqual(recorded[0].Context, preview) {
				t.Errorf("recorded context %s\nwant the preview %s", jsonOf(recorded[0].Context), jsonOf(preview))
			}
		})
	}
}

func TestTheBasePromptCanBeReplaced(t *testing.T) {
	cases := []struct {
		name         string
		workspace    bool
		override     *string
		instructions string
		wantKinds    []agent.SectionKind
		wantBase     string
	}{
		{
			name:      "a workspace gets the workspace prompt",
			workspace: true,
			wantKinds: []agent.SectionKind{agent.SectionBase, agent.SectionContextFiles},
			wantBase:  agent.WorkspacePrompt,
		},
		{
			name:      "a chat gets the chat prompt",
			wantKinds: []agent.SectionKind{agent.SectionBase},
			wantBase:  agent.ChatPrompt,
		},
		{
			name:      "an override replaces the workspace prompt",
			workspace: true,
			override:  ptr("Work carefully."),
			wantKinds: []agent.SectionKind{agent.SectionBase, agent.SectionContextFiles},
			wantBase:  "Work carefully.",
		},
		{
			name:      "an override replaces the chat prompt",
			override:  ptr("Chat freely."),
			wantKinds: []agent.SectionKind{agent.SectionBase},
			wantBase:  "Chat freely.",
		},
		{
			name:      "an empty override leaves a workspace without a base prompt",
			workspace: true,
			override:  ptr(""),
			wantKinds: []agent.SectionKind{agent.SectionContextFiles},
		},
		{
			name:     "an empty override leaves a chat without a system prompt",
			override: ptr(""),
		},
		{
			name:         "an empty override keeps the instructions",
			override:     ptr(""),
			instructions: "Be brief.",
			wantKinds:    []agent.SectionKind{agent.SectionInstructions},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			p := providertest.New(providertest.Text("ok"))
			opts := agent.Options{
				BasePrompt:   c.override,
				Instructions: c.instructions,
				Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			if c.workspace {
				opts.Executor = workspace(t, map[string]string{"AGENTS.md": "always run make check"})
			}
			a := agent.New(p, nil, opts)
			s := agent.NewSession("s1", "")

			preview, err := a.Preview(ctx, s)
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}
			if got := kinds(preview.Sections); !reflect.DeepEqual(got, c.wantKinds) {
				t.Errorf("sections = %v, want %v", got, c.wantKinds)
			}
			base := ""
			for _, section := range preview.Sections {
				if section.Kind == agent.SectionBase {
					base = section.Text
				}
			}
			if base != c.wantBase {
				t.Errorf("base prompt = %q, want %q", base, c.wantBase)
			}
			system := preview.System()
			if c.wantBase != agent.WorkspacePrompt && strings.Contains(system, "sandboxed workspace") {
				t.Errorf("system prompt = %q, still carries the workspace prompt", system)
			}
			if c.wantBase != agent.ChatPrompt && strings.Contains(system, "no workspace") {
				t.Errorf("system prompt = %q, still carries the chat prompt", system)
			}
			if err := a.Run(ctx, s, "hi"); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if sent := p.Requests()[0].System; sent != system {
				t.Errorf("sent system prompt %q, want the preview's %q", sent, system)
			}
		})
	}
}

func TestContextFilesCanBeTurnedOff(t *testing.T) {
	cases := []struct {
		name      string
		skip      bool
		wantFiles []string
	}{
		{"they are read by default", false, []string{".config/eika/AGENTS.md", "AGENTS.md"}},
		{"turned off none are read", true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exec := workspace(t, map[string]string{
				".config/eika/AGENTS.md": "global rules",
				"AGENTS.md":              "always run make check",
			})
			a := agent.New(providertest.New(), nil, agent.Options{
				Executor:         exec,
				SkipContextFiles: c.skip,
				Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			preview, err := a.Preview(context.Background(), agent.NewSession("s1", "w1"))
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}
			var files []string
			for _, section := range preview.Sections {
				for _, f := range section.Files {
					files = append(files, f.Path)
				}
			}
			if !reflect.DeepEqual(files, c.wantFiles) {
				t.Errorf("context files = %v, want %v", files, c.wantFiles)
			}
			if got := strings.Contains(preview.System(), "always run make check"); got == c.skip {
				t.Errorf("system prompt carries the context file = %t, want %t", got, !c.skip)
			}
		})
	}
}

func TestSkippedContextFilesAreNeverRead(t *testing.T) {
	cases := []struct {
		name    string
		skip    bool
		wantErr bool
	}{
		{"a workspace that cannot be read fails the preview", false, true},
		{"skipping the files reads nothing", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := agent.New(providertest.New(), nil, agent.Options{
				Executor:         statFails{workspace(t, nil)},
				SkipContextFiles: c.skip,
				Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			_, err := a.Preview(context.Background(), agent.NewSession("s1", "w1"))
			if (err != nil) != c.wantErr {
				t.Errorf("Preview error = %v, want an error %t", err, c.wantErr)
			}
		})
	}
}

func TestEstimateTokensCountsFourBytesToAToken(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 1},
		{"abcde", 2},
		{"héllo", 2},
		{strings.Repeat("x", 400), 100},
	}
	for _, c := range cases {
		if got := agent.EstimateTokens(c.text); got != c.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

func TestPreviewEstimatesEverySection(t *testing.T) {
	exec := workspace(t, map[string]string{"AGENTS.md": strings.Repeat("rule ", 100)})
	a := agent.New(providertest.New(), registry(t), agent.Options{
		Executor:     exec,
		Instructions: "Be brief.",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s := agent.NewSession("s1", "w1")
	s.Conversation.Append(provider.UserMessage("hello"))
	s.Conversation.Append(provider.AssistantMessage("", []provider.ToolCall{providertest.Call("c1", "bash", map[string]any{"command": "ls"})}))
	s.Conversation.Append(provider.ToolResultMessage("c1", "a.txt", false))
	preview, err := a.Preview(context.Background(), s)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	if len(preview.Sections) != 3 {
		t.Fatalf("sections = %v, want three", kinds(preview.Sections))
	}
	for _, section := range preview.Sections {
		if section.Tokens == 0 || section.Tokens != agent.EstimateTokens(section.Text) {
			t.Errorf("%s section tokens = %d, want the estimate of its text, %d",
				section.Kind, section.Tokens, agent.EstimateTokens(section.Text))
		}
		for _, f := range section.Files {
			if f.Tokens != agent.EstimateTokens(f.Text) || f.Text != strings.TrimSpace(strings.Repeat("rule ", 100)) {
				t.Errorf("context file %s = %d tokens of %q, want the estimate of its content", f.Path, f.Tokens, f.Text)
			}
		}
	}
	if len(preview.Tools) == 0 {
		t.Fatal("preview carries no tools")
	}
	for _, def := range preview.Tools {
		data, err := json.Marshal(def.ToolDef)
		if err != nil {
			t.Fatalf("encode %s: %v", def.Name, err)
		}
		if def.Tokens != agent.EstimateTokens(string(data)) {
			t.Errorf("tool %s tokens = %d, want the estimate of its definition, %d", def.Name, def.Tokens, agent.EstimateTokens(string(data)))
		}
	}
	if def := preview.Tools[0]; def.Tokens != agent.ToolTokens(def.ToolDef) {
		t.Errorf("ToolTokens(%s) = %d, want the size the preview reports, %d", def.Name, agent.ToolTokens(def.ToolDef), def.Tokens)
	}
	want := 0
	if len(preview.MessageSizes) != len(preview.Messages) {
		t.Fatalf("%d message sizes for %d messages", len(preview.MessageSizes), len(preview.Messages))
	}
	for i, m := range preview.Messages {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("encode message: %v", err)
		}
		if size := agent.EstimateTokens(string(data)); preview.MessageSizes[i] != size {
			t.Errorf("message %d size = %d, want the estimate of its JSON, %d", i, preview.MessageSizes[i], size)
		}
		want += agent.EstimateTokens(string(data))
	}
	if preview.MessageTokens == 0 || preview.MessageTokens != want {
		t.Errorf("message tokens = %d, want the estimate of the messages, %d", preview.MessageTokens, want)
	}
}

func TestToolSchemasSayWhereEachToolComesFrom(t *testing.T) {
	none := func(context.Context) string { return "" }
	a := agent.New(providertest.New(), registry(t,
		callTool{name: "mcp__docs__search", run: none},
		callTool{name: "mcp_read_resource", run: none},
	), agent.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	preview, err := a.Preview(context.Background(), agent.NewSession("s1", ""))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	sources := map[string]agent.ToolSource{}
	var names []string
	for _, def := range preview.Tools {
		sources[def.Name] = def.Source
		names = append(names, def.Name)
	}
	want := map[string]agent.ToolSource{
		"bash":              agent.ToolBuiltin,
		"web_search":        agent.ToolBuiltin,
		"mcp__docs__search": agent.ToolMCP,
		"mcp_read_resource": agent.ToolMCP,
	}
	for name, source := range want {
		if sources[name] != source {
			t.Errorf("tool %s source = %q, want %q", name, sources[name], source)
		}
	}
	if sent := preview.Request().Tools; len(sent) != len(names) || sent[0].Name != names[0] {
		t.Errorf("request tools = %d, want the preview's %v in its order", len(sent), names)
	}
}

func TestRequestMessagesAreWhatTheProviderReceives(t *testing.T) {
	cases := []struct {
		name          string
		preserve      bool
		wantReasoning string
	}{
		{"reasoning that is not replayed is not sent", false, ""},
		{"reasoning that is replayed is sent", true, "a private thought"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := providertest.New(providertest.Text("ok"))
			a := agent.New(p, nil, agent.Options{
				PreserveThinking: c.preserve,
				Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			s := agent.NewSession("s1", "")
			s.Conversation.Append(provider.UserMessage("earlier"))
			prior := provider.AssistantMessageWithReasoning("answer", "a private thought", nil)
			prior.Metrics = &provider.MessageMetrics{RunID: "prior", ContextWindow: 8192}
			s.Conversation.Append(prior)

			preview, err := a.Preview(context.Background(), s)
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}
			if err := a.Run(context.Background(), s, "hi"); err != nil {
				t.Fatalf("Run: %v", err)
			}
			for name, messages := range map[string][]provider.Message{
				"preview": preview.Messages,
				"request": p.Requests()[0].Messages,
			} {
				m := messages[1]
				if m.Metrics != nil {
					t.Errorf("%s message carries metrics %+v", name, m.Metrics)
				}
				if m.Reasoning != c.wantReasoning || m.Content != "answer" {
					t.Errorf("%s message = %q with reasoning %q, want answer with %q", name, m.Content, m.Reasoning, c.wantReasoning)
				}
			}
			if kept := s.Conversation.Messages()[1]; kept.Reasoning != "a private thought" || kept.Metrics == nil {
				t.Errorf("the session's own message lost its reasoning or metrics: %+v", kept)
			}
		})
	}
}

func TestRecorderLearnsEveryModelCallOnce(t *testing.T) {
	rateLimited := &provider.Error{Op: "stream", StatusCode: 429, Retryable: true, Err: errors.New("slow down")}
	toolCall := providertest.Stream(
		provider.Event{Kind: provider.KindToolCall, ToolCall: providertest.Call("c1", "bash", map[string]any{"command": "true"})},
		provider.Event{Kind: provider.KindUsage, Usage: provider.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
		provider.Done("tool_calls"),
	)
	answer := providertest.Stream(
		provider.TextDelta("done"),
		provider.Event{Kind: provider.KindUsage, Usage: provider.Usage{InputTokens: 30, OutputTokens: 2, TotalTokens: 32}},
		provider.Done("stop"),
	)
	cases := []struct {
		name        string
		steps       []providertest.Step
		recordErr   error
		wantRunErr  bool
		wantUsage   []provider.Usage
		wantSent    []int
		wantRequest []int
	}{
		{
			name:        "a call that was retried is recorded once",
			steps:       []providertest.Step{providertest.Fail(rateLimited), toolCall, answer},
			wantUsage:   []provider.Usage{{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, {InputTokens: 30, OutputTokens: 2, TotalTokens: 32}},
			wantSent:    []int{1, 3},
			wantRequest: []int{1, 2},
		},
		{
			name:       "a call that failed is not recorded",
			steps:      []providertest.Step{providertest.Fail(errors.New("bad request"))},
			wantRunErr: true,
		},
		{
			name:        "a response that reported no usage is recorded with none",
			steps:       []providertest.Step{providertest.Text("hello")},
			wantUsage:   []provider.Usage{{}},
			wantSent:    []int{1},
			wantRequest: []int{0},
		},
		{
			name:        "a recorder that fails does not stop the run",
			steps:       []providertest.Step{toolCall, answer},
			recordErr:   errors.New("the database is down"),
			wantUsage:   []provider.Usage{{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, {InputTokens: 30, OutputTokens: 2, TotalTokens: 32}},
			wantSent:    []int{1, 3},
			wantRequest: []int{0, 1},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := providertest.New(c.steps...)
			store := agent.NewMemoryStore()
			calls := &callRecorder{store: store, err: c.recordErr}
			events := &recorder{}
			a := agent.New(p, registry(t), agent.Options{
				Model:        "test-model",
				Executor:     workspace(t, nil),
				Store:        store,
				Emitter:      events,
				Recorder:     calls,
				RetryBackoff: time.Millisecond,
				Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			err := a.Run(context.Background(), agent.NewSession("s1", "w1"), "go")
			if (err != nil) != c.wantRunErr {
				t.Fatalf("Run error = %v, want an error %t", err, c.wantRunErr)
			}

			recorded, stored := calls.recorded()
			if len(recorded) != len(c.wantUsage) {
				t.Fatalf("recorded %d calls, want %d", len(recorded), len(c.wantUsage))
			}
			if len(recorded) == 0 {
				return
			}
			var start event.TurnStart
			events.payloadOf(t, event.TypeTurnStart, &start)
			requests := p.Requests()
			for i, call := range recorded {
				if call.RunID != start.RunID || call.SessionID != "s1" {
					t.Errorf("call %d = run %q session %q, want run %q session s1", i, call.RunID, call.SessionID, start.RunID)
				}
				if call.Usage != c.wantUsage[i] {
					t.Errorf("call %d usage = %+v, want %+v", i, call.Usage, c.wantUsage[i])
				}
				if got := len(call.Context.Messages); got != c.wantSent[i] {
					t.Errorf("call %d sent %d messages, want %d", i, got, c.wantSent[i])
				}
				// The conversation the call sent ends at the newest stored
				// message: every message it sent is stored, its reply not.
				if stored[i] != len(call.Context.Messages) {
					t.Errorf("call %d was recorded with %d messages stored, want the %d it sent", i, stored[i], len(call.Context.Messages))
				}
				if call.Context.Parameters.Model != "test-model" {
					t.Errorf("call %d model = %q, want test-model", i, call.Context.Parameters.Model)
				}
				if sent := requests[c.wantRequest[i]]; !reflect.DeepEqual(call.Context.Request(), sent) {
					t.Errorf("call %d recorded %s\nwant what was sent %s", i, jsonOf(call.Context.Request()), jsonOf(sent))
				}
				// A record that keeps no messages is rebuilt from the stored
				// conversation down to where the call ended.
				kept := agent.Context{Sections: call.Context.Sections, Tools: call.Context.Tools, Parameters: call.Context.Parameters}
				if rebuilt := kept.WithMessages(store.Messages("s1")[:stored[i]]); !reflect.DeepEqual(rebuilt, call.Context) {
					t.Errorf("call %d rebuilt as %s\nwant the recorded %s", i, jsonOf(rebuilt), jsonOf(call.Context))
				}
			}
		})
	}
}

func TestRunSendsTheSamplingParameters(t *testing.T) {
	cases := []struct {
		name string
		opts agent.Options
		want provider.Sampling
	}{
		{"nothing set sends nothing", agent.Options{}, provider.Sampling{}},
		{
			"every set field is sent",
			agent.Options{Sampling: provider.Sampling{
				Temperature: ptr(0.0), TopP: ptr(0.95), TopK: ptr(40), MinP: ptr(0.05), Seed: ptr(int64(7)),
				Stop: []string{"END"}, MaxOutput: ptr(100), ReasoningEffort: ptr("high"),
			}},
			provider.Sampling{
				Temperature: ptr(0.0), TopP: ptr(0.95), TopK: ptr(40), MinP: ptr(0.05), Seed: ptr(int64(7)),
				Stop: []string{"END"}, MaxOutput: ptr(100), ReasoningEffort: ptr("high"),
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := providertest.New(providertest.Text("ok"))
			opts := c.opts
			opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
			a := agent.New(p, nil, opts)
			preview, err := a.Preview(context.Background(), agent.NewSession("s1", ""))
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}
			if !reflect.DeepEqual(preview.Parameters.Sampling, c.want) {
				t.Errorf("preview sampling = %s, want %s", jsonOf(preview.Parameters.Sampling), jsonOf(c.want))
			}
			if err := a.Run(context.Background(), agent.NewSession("s1", ""), "hi"); err != nil {
				t.Fatalf("Run: %v", err)
			}
			req := p.Requests()[0]
			if !reflect.DeepEqual(req.Sampling, c.want) {
				t.Errorf("sent sampling = %s, want %s", jsonOf(req.Sampling), jsonOf(c.want))
			}
		})
	}
}

func TestPreviewOfAnEmptyChatEncodesEmptyLists(t *testing.T) {
	a := agent.New(providertest.New(), nil, agent.Options{
		BasePrompt: ptr(""),
		Model:      "m",
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	preview, err := a.Preview(context.Background(), agent.NewSession("s1", ""))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	want := `{"sections":[],"tools":[],"messages":[],"message_tokens":0,"message_sizes":[],` +
		`"parameters":{"model":"m","sampling":{},"preserve_thinking":false}}`
	if got := jsonOf(preview); got != want {
		t.Errorf("preview JSON = %s, want %s", got, want)
	}
}

func TestPreviewRejectsASessionWithoutAConversation(t *testing.T) {
	a := agent.New(providertest.New(), nil, agent.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if _, err := a.Preview(context.Background(), &agent.Session{ID: "x"}); err == nil {
		t.Fatal("Preview returned no error")
	}
}
