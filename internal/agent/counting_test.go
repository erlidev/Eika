package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/tool"
)

// thinking gives a scripted step some reasoning before its other events.
func thinking(reasoning string, step providertest.Step) providertest.Step {
	step.Events = append([]provider.Event{{Kind: provider.KindReasoningDelta, ReasoningDelta: reasoning}}, step.Events...)
	return step
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func lookTool(t *testing.T) *tool.Registry {
	t.Helper()
	look := callTool{name: "look", run: func(context.Context) string { return "notes.md todo.txt projects" }}
	tools, err := tool.NewRegistry(look)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return tools
}

// Counting must not change what is sent: the same conversation, run with a
// counter that renders and tokenises, with one that estimates, and with one
// whose server does not answer, sends the same bytes.
func TestCountingDoesNotChangeWhatIsSent(t *testing.T) {
	steps := func() []providertest.Step {
		return []providertest.Step{
			thinking("let me look", providertest.Calls("checking", providertest.Call("c1", "look", nil))),
			providertest.Text("Your home holds a few files."),
			providertest.Text("Nothing else."),
		}
	}
	sent := func(t *testing.T, c *counter.Counter) []string {
		t.Helper()
		p := providertest.New(steps()...)
		a := agent.New(p, lookTool(t), agent.Options{
			Model: "fake", ContextWindow: 100_000, PreserveThinking: true,
			Sampling: provider.Sampling{MaxOutput: ptr(4000)}, Executor: computer(t, nil),
			Counter: c, Logger: quietLogger(),
		})
		s := agent.NewSession("s1", "w1")
		for _, text := range []string{"what is in my home directory?", "and anything else?"} {
			if err := a.Run(t.Context(), s, provider.UserMessage(text)); err != nil {
				t.Fatalf("Run: %v", err)
			}
		}
		// The loop mints a different id for every call in every run, and the
		// request sends the backend's. Name them in the order they appear.
		ids := map[string]string{}
		var out []string
		for _, req := range p.Requests() {
			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("encode request: %v", err)
			}
			text := string(raw)
			for _, m := range req.Messages {
				for _, c := range m.ToolCalls {
					if _, seen := ids[c.ID]; !seen {
						ids[c.ID] = fmt.Sprintf("minted-%d", len(ids)+1)
					}
					text = strings.ReplaceAll(text, c.ID, ids[c.ID])
				}
			}
			out = append(out, text)
		}
		return out
	}

	counted := providertest.NewTokenServer(providertest.RoutesPrompt)
	cases := []struct {
		name    string
		counter *counter.Counter
	}{
		{"a server that renders and tokenises", counter.New(counted)},
		{"a server that tokenises text", counter.New(providertest.NewTokenServer(providertest.RoutesText))},
		{"a server that cannot be reached", counter.New(failingSource{counted})},
	}
	want := sent(t, counter.NewEstimating())
	if len(want) != 3 {
		t.Fatalf("the conversation made %d calls, want 3", len(want))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sent(t, tc.counter)
			if len(got) != len(want) {
				t.Fatalf("made %d calls, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("request %d differs when counted by %s:\n got  %s\n want %s", i+1, tc.name, got[i], want[i])
				}
			}
		})
	}
	if counted.RenderCalls() == 0 {
		t.Error("the rendering server was never asked to count; the comparison proves nothing")
	}
}

// failingSource is an endpoint whose routes all fail.
type failingSource struct{ *providertest.TokenServer }

func (failingSource) Prompt(context.Context, provider.Request) (string, error) {
	return "", errors.New("connection refused")
}

func (failingSource) Text(context.Context, string) (int, error) {
	return 0, errors.New("connection refused")
}
