package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/tool"
)

// callLog is an agent.Recorder that keeps what it is told.
type callLog struct {
	mu    sync.Mutex
	calls []agent.ModelCall
}

func (l *callLog) Record(_ context.Context, c agent.ModelCall) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, c)
	return nil
}

func (l *callLog) all() []agent.ModelCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]agent.ModelCall(nil), l.calls...)
}

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

// The prediction before every call is the prompt the server then measures,
// over a conversation with a tool round, replayed reasoning and a compaction,
// run through the real loop against a server that renders and tokenises.
func TestEveryCallIsPredictedExactlyAcrossToolsReasoningAndACompaction(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt,
		thinking("I should look at the files first.", providertest.Calls("checking", providertest.Call("c1", "look", nil))),
		thinking("The listing is short, so I can answer directly.", providertest.Text("Your home holds notes.md, todo.txt and projects.")),
		providertest.Text("Nothing else is there."),
		providertest.Text("SUMMARY of the work so far."),
		thinking("Back to work.", providertest.Text("Understood, carrying on.")),
	)
	log := &callLog{}
	store := newMemoryStore()
	a := agent.New(server, lookTool(t), agent.Options{
		Model: "fake", ContextWindow: 100_000, PreserveThinking: true,
		Compaction: agent.CompactionSettings{KeepRecentTokens: 20},
		Executor:   computer(t, nil), Store: store, Recorder: log,
		Counter: counter.New(server), Logger: quietLogger(),
	})
	s := agent.NewSession("s1", "w1")
	run := func(text string) {
		t.Helper()
		if err := a.Run(t.Context(), s, provider.UserMessage(text)); err != nil {
			t.Fatalf("Run %q: %v", text, err)
		}
	}
	run("hello, what is in my home directory?")
	run("is there anything else?")

	// What the compaction reports is the size of the conversation before it
	// and after, counted the way a call is.
	before, err := a.Preview(s)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if err := a.Compact(t.Context(), s, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	after, err := a.Preview(s)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	compactions := store.compactions["s1"]
	if len(compactions) != 1 {
		t.Fatalf("stored %d compactions, want 1", len(compactions))
	}
	if want := server.PromptTokens(before.Request()); compactions[0].TokensBefore != want {
		t.Errorf("compaction before = %d tokens, want the %d the server reads in the conversation", compactions[0].TokensBefore, want)
	}
	if want := server.PromptTokens(after.Request()); compactions[0].TokensAfter != want {
		t.Errorf("compaction after = %d tokens, want the %d the server reads in what is left", compactions[0].TokensAfter, want)
	}

	run("please carry on from there")

	calls := log.all()
	if len(calls) != 5-1 {
		t.Fatalf("recorded %d calls, want 4 (the summary is not a run's call)", len(calls))
	}
	for i, c := range calls {
		measured, stated := c.Usage.InputTokens.Value()
		if !stated {
			t.Fatalf("call %d: the fake server stated no prompt count", i+1)
		}
		if c.Prediction.Method != provider.MethodExact || c.Prediction.Tokens != measured {
			t.Errorf("call %d: predicted %d (%s), measured %d; want exact and equal", i+1, c.Prediction.Tokens, c.Prediction.Method, measured)
		}
		if c.Drift == nil || *c.Drift != 0 {
			t.Errorf("call %d: drift = %v, want 0", i+1, deref(c.Drift))
		}
		if c.ContextWindow != 100_000 {
			t.Errorf("call %d: window = %d", i+1, c.ContextWindow)
		}
	}
	// The fake server states no reasoning count, so the number is the
	// tokenisation of the reasoning text, labelled so.
	if got, ok := calls[0].Reasoning.Value(); !ok || got != providertest.Tokens("I should look at the files first.") || calls[0].ReasoningSource != agent.ReasoningTokenised {
		t.Errorf("call 1 reasoning = %v %q, want the tokenised count of its reasoning", calls[0].Reasoning, calls[0].ReasoningSource)
	}
	if calls[2].Reasoning.Reported() || calls[2].ReasoningSource != "" {
		t.Errorf("call 3 had no reasoning and its endpoint stated none, so its count is %v %q, want none", calls[2].Reasoning, calls[2].ReasoningSource)
	}
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
		var out []string
		for _, req := range p.Requests() {
			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("encode request: %v", err)
			}
			out = append(out, string(raw))
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

// stalledSource is an endpoint whose routes never answer.
type stalledSource struct{ *providertest.TokenServer }

func (stalledSource) Prompt(ctx context.Context, _ provider.Request) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func TestACounterThatFailsOrStallsNeverStopsACall(t *testing.T) {
	for _, tc := range []struct {
		name    string
		source  counter.Source
		timeout time.Duration
	}{
		{"failing", failingSource{providertest.NewTokenServer(providertest.RoutesPrompt)}, 0},
		{"stalled", stalledSource{providertest.NewTokenServer(providertest.RoutesPrompt)}, 50 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &callLog{}
			a := agent.New(providertest.New(providertest.Text("an answer")), nil, agent.Options{
				Model: "fake", ContextWindow: 100_000, Recorder: log,
				Counter: counter.New(tc.source), CountTimeout: tc.timeout, Logger: quietLogger(),
			})
			s := agent.NewSession("s1", "")
			start := time.Now()
			if err := a.Run(t.Context(), s, provider.UserMessage("hello")); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if took := time.Since(start); took > 5*time.Second {
				t.Errorf("the run took %v; a stalled tokenizer must not hold it", took)
			}
			calls := log.all()
			if len(calls) != 1 || calls[0].Prediction.Method != provider.MethodEstimated || calls[0].Prediction.Tokens <= 0 {
				t.Errorf("recorded %+v, want one call with an estimated prediction", calls)
			}
		})
	}
}

// An exact prediction with any drift is a bug, so it is logged with the
// numbers and nothing of the request, and the drift is on the record.
func TestAnExactPredictionThatDriftsIsWarnedAboutWithoutTheRequest(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt, providertest.Text("noted"))
	var logs lockedBuffer
	log := &callLog{}
	a := agent.New(server, nil, agent.Options{
		Model: "fake", Recorder: log, Counter: counter.New(skewedSource{server}),
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	s := agent.NewSession("s1", "")
	if err := a.Run(t.Context(), s, provider.UserMessage("the launch code is swordfish")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := log.all()
	if len(calls) != 1 || calls[0].Drift == nil || *calls[0].Drift == 0 || calls[0].Prediction.Method != provider.MethodExact {
		t.Fatalf("recorded %+v, want one exact prediction with drift", calls)
	}
	out := logs.String()
	for _, want := range []string{"exact token prediction drifted", "predicted=", "measured=", "drift="} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "swordfish") {
		t.Errorf("log holds the content of the request: %s", out)
	}
}

// skewedSource renders every request with words the server will not read.
type skewedSource struct{ *providertest.TokenServer }

func (s skewedSource) Prompt(ctx context.Context, req provider.Request) (string, error) {
	prompt, err := s.TokenServer.Prompt(ctx, req)
	return prompt + " three extra words", err
}

type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestAReasoningCountSaysWhereItCameFrom(t *testing.T) {
	stated := providertest.Usage(40, 20, 60)
	stated.ReasoningTokens = provider.Reported(7)
	reasoning := provider.Event{Kind: provider.KindReasoningDelta, ReasoningDelta: "weighing the options"}
	for _, tc := range []struct {
		name       string
		step       providertest.Step
		counter    *counter.Counter
		wantCount  int
		wantSource string
	}{
		{"the endpoint states it",
			providertest.Stream(reasoning, provider.TextDelta("ok"), provider.Event{Kind: provider.KindUsage, Usage: stated}, provider.Done("stop")),
			counter.New(providertest.NewTokenServer(providertest.RoutesText)), 7, agent.ReasoningReported},
		{"the endpoint states none and can tokenise",
			providertest.Stream(reasoning, provider.TextDelta("ok"), provider.Done("stop")),
			counter.New(providertest.NewTokenServer(providertest.RoutesText)), providertest.Tokens("weighing the options"), agent.ReasoningTokenised},
		{"the endpoint states none and cannot tokenise",
			providertest.Stream(reasoning, provider.TextDelta("ok"), provider.Done("stop")),
			counter.NewEstimating(), -1, ""},
		{"there was no reasoning and none is stated",
			providertest.Text("ok"),
			counter.New(providertest.NewTokenServer(providertest.RoutesText)), -1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &callLog{}
			a := agent.New(providertest.New(tc.step), nil, agent.Options{Recorder: log, Counter: tc.counter, Logger: quietLogger()})
			if err := a.Run(t.Context(), agent.NewSession("s1", ""), provider.UserMessage("hi")); err != nil {
				t.Fatalf("Run: %v", err)
			}
			calls := log.all()
			if len(calls) != 1 {
				t.Fatalf("recorded %d calls", len(calls))
			}
			got, ok := calls[0].Reasoning.Value()
			if tc.wantCount < 0 && (ok || calls[0].ReasoningSource != "") {
				t.Errorf("reasoning = %d %q, want no count", got, calls[0].ReasoningSource)
			}
			if tc.wantCount >= 0 && (!ok || got != tc.wantCount || calls[0].ReasoningSource != tc.wantSource) {
				t.Errorf("reasoning = %d (stated %v) %q, want %d %q", got, ok, calls[0].ReasoningSource, tc.wantCount, tc.wantSource)
			}
		})
	}
}

// A session resumed after a restart has a measured call in its stored messages
// and a counter that has measured nothing; its first prediction starts from the
// measurement rather than from the bytes alone.
func TestAResumedSessionIsSizedFromItsStoredMeasurement(t *testing.T) {
	log := &callLog{}
	a := agent.New(providertest.New(providertest.Text("ok")), nil, agent.Options{Recorder: log, Logger: quietLogger()})
	reply := provider.AssistantMessage(filler("a1", 1000), nil)
	reply.Metrics = &provider.MessageMetrics{RunID: "r0", Context: providertest.Usage(2500, 1000, 3500)}
	s := sessionOf([]provider.Message{provider.UserMessage(filler("u0", 1000)), reply})
	if err := a.Run(t.Context(), s, provider.UserMessage("next")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := log.all()
	if len(calls) != 1 {
		t.Fatalf("recorded %d calls", len(calls))
	}
	// 2500 measured for the system prompt and the first message, and the reply
	// and the new message at the ratio that measurement showed: between 4000
	// and 5200, where bytes alone would say about 2,200.
	if got := calls[0].Prediction.Tokens; got < 4000 || got > 5200 {
		t.Errorf("first prediction = %d, want 4000 to 5200 from the stored 2500 and what was added", got)
	}
	if calls[0].Prediction.Method != provider.MethodEstimated {
		t.Errorf("method = %s, want estimated: the endpoint tokenises nothing", calls[0].Prediction.Method)
	}
}
