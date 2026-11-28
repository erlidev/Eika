package agent_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// stubGate answers every Decide the same way.
type stubGate struct {
	sentry.Gate // halt methods; unused here
	verdict     sentry.Verdict
	err         error
}

func (g stubGate) Decide(context.Context, sentry.Action) (sentry.Verdict, error) {
	return g.verdict, g.err
}
func (stubGate) Observe(context.Context, sentry.Observation) {}
func (stubGate) Ingest(_ context.Context, segs []sentry.Segment) ([]sentry.Ingested, error) {
	out := make([]sentry.Ingested, len(segs))
	for i, s := range segs {
		out[i] = sentry.Ingested{Text: s.Text}
	}
	return out, nil
}

// readOnlyTool is a callTool that declares itself read-only.
type readOnlyTool struct{ callTool }

func (readOnlyTool) ReadOnly() bool { return true }

// toolResult runs one call of name through a gated agent and returns the
// tool message and whether the tool ran.
func toolResult(t *testing.T, g sentry.Gate, name string, ro bool) (provider.Message, bool) {
	t.Helper()
	ran := false
	ct := callTool{name: name, run: func(context.Context) string { ran = true; return "ran" }}
	var x tool.Tool = ct
	if ro {
		x = readOnlyTool{callTool: ct}
	}
	f := newFixtureWith(t, agent.Options{Gate: g}, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", name, map[string]any{})),
		providertest.Text("done"),
	}, x)
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, m := range f.session.Conversation.Messages() {
		if m.Role == provider.RoleTool {
			return m, ran
		}
	}
	t.Fatal("no tool result")
	return provider.Message{}, false
}

func TestGateDenyBlocksTheCall(t *testing.T) {
	for _, d := range []sentry.Decision{sentry.Deny, sentry.Ask} {
		m, ran := toolResult(t, stubGate{verdict: sentry.Verdict{Decision: d, Reason: "not allowed here"}}, "probe", true)
		if ran || !m.IsError || m.Content != "Blocked: not allowed here" {
			t.Errorf("%s: ran=%v result=%+v", d, ran, m)
		}
	}
}

func TestGateFailsClosedForSideEffectingTools(t *testing.T) {
	g := stubGate{err: errors.New("audit down")}
	m, ran := toolResult(t, g, "probe", false)
	if ran || !m.IsError || !strings.HasPrefix(m.Content, "Blocked: ") {
		t.Errorf("side-effecting: ran=%v result=%+v", ran, m)
	}
	m, ran = toolResult(t, g, "probe", true)
	if !ran || m.IsError {
		t.Errorf("read-only: ran=%v result=%+v", ran, m)
	}
}

// mentionsGate matches the words, not "delegates".
var mentionsGate = regexp.MustCompile(`(?i)sentry|\bgate`)

func TestNothingTheModelReadsMentionsTheGate(t *testing.T) {
	texts := []string{agent.ChatPrompt}
	for _, d := range agentdef.Builtins() {
		texts = append(texts, d.Prompt, d.Description)
	}
	r, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range r.List() {
		texts = append(texts, x.Description(), string(x.Schema()))
	}
	for _, s := range texts {
		if mentionsGate.MatchString(s) {
			t.Errorf("model-facing text mentions the gate: %.80q", s)
		}
	}
}

// observeGate records what the agent observed.
type observeGate struct {
	stubGate
	seen []sentry.Observation
}

func (g *observeGate) Observe(_ context.Context, o sentry.Observation) { g.seen = append(g.seen, o) }

func TestReadsOfPrivateTextReachTheGate(t *testing.T) {
	g := &observeGate{stubGate: stubGate{verdict: sentry.Verdict{Decision: sentry.Allow}}}
	run := func(name string, args map[string]any, setup func(*fixture), extra ...tool.Tool) {
		f := newFixtureWith(t, agent.Options{Gate: g}, []providertest.Step{
			providertest.Calls("", providertest.Call("c1", name, args)),
			providertest.Text("done"),
		}, extra...)
		if setup != nil {
			setup(f)
		}
		if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("go")); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	run("file_read", map[string]any{"path": "a.txt"}, func(f *fixture) {
		if err := f.exec.WriteFile(context.Background(), "a.txt", []byte("plain file text")); err != nil {
			t.Fatal(err)
		}
	})
	run("bash", map[string]any{"command": "echo hi"}, nil)
	got := map[string]bool{}
	for _, o := range g.seen {
		if o.Kind == sentry.ObservePrivateRead {
			got[o.Text] = true
		}
	}
	if len(got) != 1 || !got["plain file text"] {
		t.Fatalf("private reads = %v", got)
	}
}
