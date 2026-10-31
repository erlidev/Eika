package live

import (
	"context"
	"fmt"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/probe"
)

// OnResponses returns the scenario s played under the Responses API: the same
// requests through the Responses provider, counted from the chat body that
// provider builds. The server converts what it is sent into a chat request
// before rendering it, so a scenario that requires zero drift under chat
// requires it here as well, and fails if the server's conversion left out
// or changed anything the chat request holds.
func OnResponses(s Scenario) Scenario {
	return Scenario{
		Name: "responses_" + s.Name,
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			if t.Responses == nil {
				return nil, fmt.Errorf("the target has no Responses provider")
			}
			session := *t
			session.Provider = t.Responses
			session.Source = t.ResponsesSource
			session.Counter = counter.New(t.ResponsesSource)
			return s.Run(ctx, &session)
		},
	}
}

// ResponsesConformance is the scenario that runs the harness's Responses
// conformance check against the bench for real: a streamed tool-call round
// trip with the model's reasoning item sent back, usage with a cached count,
// and the second request counted to the token before it is sent. It makes two
// small model requests and requires the check to pass on a server that has the
// route, which is what decides whether such a server is spoken to in the
// Responses API.
func ResponsesConformance() Scenario {
	return Scenario{
		Name: "responses_conformance",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			if t.Responses == nil {
				return nil, fmt.Errorf("the target has no Responses provider")
			}
			check := probe.ResponsesCheck{
				Provider: t.Responses, Model: t.Model, Build: t.Server.Build, PreserveThinking: true,
			}
			if t.Probe.Capabilities.Counting == probe.CountChat {
				ctr := counter.New(t.ResponsesSource)
				check.Predict = func(ctx context.Context, req provider.Request) (provider.Prediction, error) {
					return ctr.Peek(ctx, req), nil
				}
			}
			start := time.Now()
			found := probe.CheckResponses(ctx, check)
			call := Call{
				Label:       fmt.Sprintf("conformance: %s, reasoning %t, cache metrics %t, counted %t", found.Verdict, found.Reasoning, found.CacheMetrics, found.Counted),
				StopReason:  string(found.Verdict),
				WallSeconds: time.Since(start).Seconds(),
			}
			if found.Counted {
				drift := found.Drift
				call.Method, call.Drift = string(provider.MethodExact), &drift
			}
			if found.Verdict != probe.Passed {
				return []Call{call}, fmt.Errorf("the Responses conformance check %s: %s", found.Verdict, found.Reason)
			}
			if t.Probe.Capabilities.Counting == probe.CountChat && !found.Counted {
				return []Call{call}, fmt.Errorf("the second request was not counted exactly, though the endpoint counts exactly")
			}
			return []Call{call}, nil
		},
	}
}

// ToolArguments is the scenario that counts a request whose history holds tool
// calls with arguments a server may write differently from how it read them:
// spaces and newlines in the JSON, keys out of order, nested values, a number
// with a fraction, escapes and characters outside ASCII, and a call with an empty
// object. A server that parses the arguments and writes them again
// into the prompt does so for the request that was counted as well, so the
// two agree unless a protocol changed them on the way.
func ToolArguments(nonce string) Scenario {
	return Scenario{
		Name: "tool_arguments",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			maxOutput, temperature := 16, 0.0
			effort := provider.EffortNone
			req := provider.Request{
				System:         fmt.Sprintf("Session %s.\nYou are Eika.", nonce),
				ThinkingSwitch: provider.SwitchTemplate,
				Sampling:       provider.Sampling{MaxOutput: &maxOutput, Temperature: &temperature, ReasoningEffort: &effort},
				Tools: []provider.ToolDef{
					{Name: "edit", Description: "Edit a file.", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`)},
					{Name: "now", Description: "The time.", Schema: []byte(`{"type":"object","properties":{}}`)},
				},
				Messages: []provider.Message{
					provider.UserMessage("Fix the typo in my notes, then tell me the time."),
					provider.AssistantMessage("", []provider.ToolCall{
						{ID: "call_1", Name: "edit", Arguments: "{ \"path\" : \"/home/ünï/notes.md\",\n  \"lines\": [3, 4.5, -0.25e2],\n  \"after\": {\"z\": 1, \"y\": [true, null, \"a\\\"b\\\\c\\n\"]},\n  \"text\": \"naïve café ☕ \\u00e9\" }"},
						{ID: "call_2", Name: "now", Arguments: "{}"},
					}),
					provider.ToolResultMessage("call_1", "done: 2 lines changed\n\tindented", false),
					provider.ToolResultMessage("call_2", "14:05 ☕", false),
				},
			}
			call, _, err := t.Complete(ctx, "unusual tool arguments", req)
			if err != nil {
				return []Call{call}, err
			}
			return []Call{call}, requireExact(call)
		},
	}
}

// ThinkingSwitches is the scenario that counts the same short request with
// each way a model's thinking can be asked for or turned off: the standard
// reasoning effort field, the chat template's own switch, a reasoning effort
// other than none, and the request field some APIs use. Each is a field the
// protocol carries in its own place, so a protocol that drops or moves one
// renders another preamble than the one counted.
func ThinkingSwitches(nonce string) Scenario {
	return Scenario{
		Name: "thinking_switches",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			variants := []struct {
				label       string
				switchField provider.ThinkingSwitch
				effort      string
			}{
				{"effort none by reasoning_effort", provider.SwitchReasoningEffort, provider.EffortNone},
				{"effort none by template", provider.SwitchTemplate, provider.EffortNone},
				{"effort none by thinking", provider.SwitchThinking, provider.EffortNone},
				{"effort low", provider.SwitchReasoningEffort, "low"},
				{"no effort", "", ""},
			}
			var calls []Call
			for _, v := range variants {
				temperature, maxOutput := 0.0, 16
				req := provider.Request{
					System:         fmt.Sprintf("Session %s.\nYou are Eika.", nonce),
					Messages:       []provider.Message{provider.UserMessage("Say hello.")},
					ThinkingSwitch: v.switchField,
					Sampling:       provider.Sampling{MaxOutput: &maxOutput, Temperature: &temperature},
				}
				if v.effort != "" {
					req.Sampling.ReasoningEffort = &v.effort
				}
				call, _, err := t.Complete(ctx, v.label, req)
				calls = append(calls, call)
				if err != nil {
					return calls, err
				}
				if err := requireExact(call); err != nil {
					return calls, err
				}
			}
			return calls, nil
		},
	}
}

// CutOffReasoning is the scenario that sends a conversation whose earlier
// answer was cut off inside its reasoning, so the assistant message the loop
// kept holds reasoning and nothing else. A turn of such a message is what a
// protocol is most likely to render differently, and the request after it
// must still be counted to the token. The loop runs two turns with an output
// limit the model's thinking exceeds; the second request carries the first's
// reasoning-only message.
func CutOffReasoning(nonce string) Scenario {
	return Scenario{
		Name: "cut_off_reasoning",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			temperature := 0.0
			calls, recorded, err := playLoop(ctx, t, loopOptions{
				system:   fmt.Sprintf("Session %s.\nYou are Eika. Think carefully before you answer.", nonce),
				sampling: provider.Sampling{MaxOutput: new(40), Temperature: &temperature},
				asks: []string{
					"Work out, step by step, whether 17 times 23 is more than 400.",
					"Now answer yes or no.",
				},
			})
			if err != nil {
				return calls, err
			}
			if len(recorded) != 2 {
				return calls, fmt.Errorf("the loop recorded %d calls, want 2", len(recorded))
			}
			for _, c := range calls {
				if err := requireExact(c); err != nil {
					return calls, err
				}
				if c.Drift == nil || *c.Drift != 0 {
					return calls, fmt.Errorf("%s: drift %s, want 0", c.Label, c.drift())
				}
			}
			return calls, nil
		},
	}
}

// ResponsesDrift is the scenario that counts a conversation with tools and
// reasoning sent under the Responses API. The server converts the Responses
// input to a chat request itself, and exact counting renders the equivalent
// chat body the provider builds, so the two agree only if the server's
// conversion keeps what the chat body says: the reasoning of earlier turns,
// the tool calls and their arguments, the thinking switch. It runs the real
// loop on one session, with thinking on and the reasoning replayed, three
// turns that call a tool, and requires the prediction of every request to be
// exact with no drift.
func ResponsesDrift(nonce string) Scenario {
	return Scenario{
		Name: "responses_drift",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			if t.Responses == nil {
				return nil, fmt.Errorf("the target has no Responses provider")
			}
			session := *t
			session.Provider = t.Responses
			session.Counter = counter.New(t.ResponsesSource)
			temperature := 0.0
			calls, recorded, err := playLoop(ctx, &session, loopOptions{
				system:   fmt.Sprintf("Session %s.\nYou are Eika. Use the look_up tool when the user asks about their notes. Think briefly, then answer in one short sentence.", nonce),
				sampling: provider.Sampling{MaxOutput: new(900), Temperature: &temperature},
				asks: []string{
					"Look up both my bank note and my groceries note, then tell me what they say.",
					"Without looking anything up, which one mentioned a day of the week?",
					"Now look the groceries note up again and count the items.",
				},
			})
			if err != nil {
				return calls, err
			}
			if len(recorded) < 3 {
				return calls, fmt.Errorf("the loop recorded %d calls, want at least one per turn", len(recorded))
			}
			for _, c := range calls {
				if err := requireExact(c); err != nil {
					return calls, err
				}
				if c.Drift == nil || *c.Drift != 0 {
					return calls, fmt.Errorf("%s: drift %s under responses, want 0: the server's conversion of the input must render the chat request", c.Label, c.drift())
				}
			}
			return calls, nil
		},
	}
}
