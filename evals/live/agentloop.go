package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/tool"
)

// agentLoopTimeout bounds the whole scenario: the loop retries and waits on
// its own, so the per-call bound of Complete does not apply to it.
const agentLoopTimeout = 5 * time.Minute

// lookUp is the one tool of the agent scenarios: it answers from a fixed
// table, so they need no computer.
type lookUp struct{}

func (lookUp) Name() string { return "look_up" }
func (lookUp) Description() string {
	return "Look up a fact in the user's notes by topic. Topics: bank, groceries."
}
func (lookUp) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"topic":{"type":"string","description":"The topic to look up."}},"required":["topic"]}`)
}
func (lookUp) Standalone() {}
func (lookUp) Call(_ context.Context, _ tool.CallContext, args json.RawMessage) (tool.Result, error) {
	var in struct {
		Topic string `json:"topic"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Errorf("the arguments are not a topic: %v", err), nil
	}
	switch in.Topic {
	case "bank":
		return tool.Text("Call the bank on Monday about the standing order."), nil
	case "groceries":
		return tool.Text("Milk, bread, two lemons."), nil
	}
	return tool.Errorf("no note on %q", in.Topic), nil
}

// callRecords is an agent.Recorder that keeps the calls it is told about.
type callRecords struct {
	mu    sync.Mutex
	calls []agent.ModelCall
}

func (r *callRecords) Record(_ context.Context, c agent.ModelCall) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
	return nil
}

func (r *callRecords) all() []agent.ModelCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agent.ModelCall(nil), r.calls...)
}

// loopOptions say what an agent scenario varies.
type loopOptions struct {
	// system is the base prompt, and sampling the sampling of every call.
	system   string
	sampling provider.Sampling
	// asks are the user messages, one turn each.
	asks []string
	// clock, when set, is the loop's clock; nil is the real one.
	clock func() time.Time
}

// playLoop runs the real agent loop against the bench, on one session with the
// scenario's token counter, and returns what the loop recorded for each call
// as scenario calls: the prediction it made before sending, the prompt the
// server measured, the drift between them, and the reasoning count with its
// source. The loop does not report how long a call took, so wall time is
// left at zero.
func playLoop(ctx context.Context, t *Target, o loopOptions) ([]Call, []agent.ModelCall, error) {
	ctx, cancel := context.WithTimeout(ctx, agentLoopTimeout)
	defer cancel()
	tools, err := tool.NewRegistry(lookUp{})
	if err != nil {
		return nil, nil, err
	}
	records := &callRecords{}
	a := agent.New(t.Provider, tools, agent.Options{
		Model:            t.Model,
		ContextWindow:    32768,
		BasePrompt:       &o.system,
		Sampling:         o.sampling,
		ThinkingSwitch:   provider.SwitchTemplate,
		PreserveThinking: true,
		Recorder:         records,
		Counter:          t.Counter,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:            o.clock,
	})
	s := agent.NewSession("live", "")
	var runErr error
	for _, ask := range o.asks {
		if runErr = a.Run(ctx, s, provider.UserMessage(ask)); runErr != nil {
			runErr = fmt.Errorf("run %q: %w", ask, runErr)
			break
		}
	}
	recorded := records.all()
	calls := make([]Call, 0, len(recorded))
	for i, r := range recorded {
		c := newCall(fmt.Sprintf("loop call %d", i+1), r.Usage, provider.Timings{}, "", 0)
		c.ReasoningTokens, c.ReasoningSource = r.Reasoning.Ptr(), r.ReasoningSource
		if p := r.Prediction; p.Method != "" {
			tokens := p.Tokens
			c.Predicted, c.Method, c.Drift = &tokens, string(p.Method), r.Drift
		}
		calls = append(calls, c)
	}
	return calls, recorded, runErr
}

// AgentLoop is the scenario that runs the real agent loop against the bench
// and reads what it recorded for each call: the prediction the session's
// counter made before sending, the prompt the server measured, and the drift
// between them, which must be zero on every call, since the server renders and
// tokenises each request itself. Two turns on one session, each asking for a
// tool, so the loop sends at least two requests and usually four.
func AgentLoop(nonce string) Scenario {
	return Scenario{
		Name: "agent_loop",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			sampling := quiet()
			sampling.MaxOutput = new(160)
			calls, _, err := playLoop(ctx, t, loopOptions{
				system:   fmt.Sprintf("Session %s.\nYou are Eika. Use the look_up tool when the user asks about their notes, then answer in one short sentence.", nonce),
				sampling: sampling,
				asks:     []string{"What do my notes say about the bank?", "And what about groceries?"},
			})
			if err != nil {
				return calls, err
			}
			if len(calls) < 2 {
				return calls, fmt.Errorf("the loop recorded %d calls, want at least one per turn", len(calls))
			}
			for _, c := range calls {
				if err := requireExact(c); err != nil {
					return calls, err
				}
				if c.Drift == nil || *c.Drift != 0 {
					return calls, fmt.Errorf("%s: drift %s, want 0: the loop's prediction must be the prompt the server measured", c.Label, c.drift())
				}
			}
			return calls, nil
		},
	}
}

// reasoningBound is the most tokens the count of a reasoning text may differ
// from the tokens the server generated for it, in a response cut off inside its
// reasoning. The bench showed none (60 and 60, five runs), but the text is
// tokenised again from scratch and not read back from the generation, so a
// small difference is allowed.
const reasoningBound = 3

// ReasoningCount is the scenario that checks the number the loop records for
// reasoning against the server. llama.cpp states no reasoning count, so the
// loop tokenises the reasoning text and labels the number as tokenised. A
// response cut off inside its reasoning is all reasoning, so its completion
// tokens are what the tokenised count approximates: the scenario holds the two
// to reasoningBound and requires the label.
func ReasoningCount(nonce string) Scenario {
	return Scenario{
		Name: "reasoning_count",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			temperature := 0.0
			calls, recorded, err := playLoop(ctx, t, loopOptions{
				system:   fmt.Sprintf("Session %s.\nYou are Eika. Think carefully before you answer.", nonce),
				sampling: provider.Sampling{MaxOutput: new(60), Temperature: &temperature},
				asks:     []string{"Work out, step by step, whether 17 times 23 is more than 400."},
			})
			if err != nil {
				return calls, err
			}
			if len(recorded) != 1 {
				return calls, fmt.Errorf("the loop recorded %d calls, want 1", len(recorded))
			}
			c := recorded[0]
			if c.Usage.ReasoningTokens.Reported() {
				// A server that counts its own reasoning has nothing to check.
				return calls, nil
			}
			tokenised, ok := c.Reasoning.Value()
			generated, stated := c.Usage.OutputTokens.Value()
			switch {
			case !ok || c.ReasoningSource != agent.ReasoningTokenised:
				return calls, fmt.Errorf("the reasoning count is %v from %q, want a tokenised count", c.Reasoning, c.ReasoningSource)
			case !stated:
				return calls, fmt.Errorf("the server stated no completion count")
			case abs(tokenised-generated) > reasoningBound:
				return calls, fmt.Errorf("the reasoning text tokenises to %d and the server generated %d tokens, more than %d apart", tokenised, generated, reasoningBound)
			}
			return calls, nil
		},
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
