package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/provider"
)

// Verdict is the outcome of the Responses conformance check.
type Verdict string

// The outcomes of the check.
const (
	// Passed means the endpoint took a streamed tool-call round trip with the
	// reasoning it returned, stated its usage with a cached count, and, where
	// the check could ask, was counted to the token.
	Passed Verdict = "passed"
	// Failed means the endpoint did something else, and Reason says what.
	Failed Verdict = "failed"
	// Skipped means the check could not run, for want of a model to ask, and
	// Reason says why.
	Skipped Verdict = "skipped"
)

// Conformance is what the Responses conformance check found out. It is stored
// in the provider's capabilities with the build of the server and the model it
// ran against, which is how a later probe knows whether the check is still
// current.
type Conformance struct {
	// Verdict is the outcome. Only Passed chooses the Responses API.
	Verdict Verdict `json:"verdict"`
	// Reason says why the check failed or was skipped, and is empty when it
	// passed.
	Reason string `json:"reason,omitempty"`
	// Model is the model the check asked, and Build the server's build when
	// it did. A change of either makes the check stale.
	Model string `json:"model,omitempty"`
	Build string `json:"build,omitempty"`
	// CheckedAt is when the check ran.
	CheckedAt time.Time `json:"checked_at"`
	// Reasoning is whether the model returned a reasoning item that the
	// second request sent back. A model that does not reason has none to
	// send, and the check says so rather than claiming it was exercised.
	Reasoning bool `json:"reasoning"`
	// CacheMetrics is whether the usage of the second request stated how many
	// prompt tokens were cached.
	CacheMetrics bool `json:"cache_metrics"`
	// Counted is whether the second request was also counted by the token
	// counter before it was sent, and Drift how far that count was from the
	// endpoint's own. A nonzero drift fails the check.
	Counted bool `json:"counted"`
	Drift   int  `json:"drift"`
}

// Current reports whether the check was made against the same build and the
// same model, so that running it again would learn nothing.
func (c Conformance) Current(build, model string) bool {
	return c.Verdict != "" && c.Build == build && c.Model == model
}

// Bounds on what the check sends. It asks a real model, so each request has a
// small output limit and a time limit of its own.
const (
	// firstMaxOutput bounds the first request, which must reach a tool call:
	// a model that reasons first needs room for its reasoning.
	firstMaxOutput = 512
	// secondMaxOutput bounds the second, which only has to be accepted.
	secondMaxOutput = 256
	// callTimeout bounds one request.
	callTimeout = 90 * time.Second
)

// ResponsesCheck is what CheckResponses needs.
type ResponsesCheck struct {
	// Provider speaks the Responses API to the endpoint under test.
	Provider provider.Provider
	// Model is the model to ask, and Build the server's build, both recorded
	// with the result.
	Model, Build string
	// PreserveThinking is whether the requests ask for reasoning and send it
	// back, as the model it stands for would.
	PreserveThinking bool
	// Predict counts a request the way the session's token counter does,
	// before it is sent. It is nil when the endpoint has no exact counting,
	// and the drift is then not checked.
	Predict func(ctx context.Context, req provider.Request) (provider.Prediction, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// CheckResponses sends the endpoint two small requests and reports whether the
// Responses API works on it the way the loop needs (docs/design/runtime.md
// section 1). The first asks for a tool call and streams it, with the model's
// reasoning; the second sends the call, its result and the reasoning item back
// as stored, and streams the answer. Both must complete and state their usage,
// the second with a cached count, and the second must be counted to the token
// by Predict when there is one. It sends real model requests, bounded in
// tokens and in time, and never fails: a problem is the verdict.
func CheckResponses(ctx context.Context, c ResponsesCheck) Conformance {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	out := Conformance{Model: c.Model, Build: c.Build, CheckedAt: now().UTC()}
	fail := func(format string, args ...any) Conformance {
		out.Verdict, out.Reason = Failed, fmt.Sprintf(format, args...)
		return out
	}

	tools := []provider.ToolDef{{
		Name:        "echo",
		Description: "Repeat a word back.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"word":{"type":"string"}},"required":["word"]}`),
	}}
	temperature := 0.0
	request := func(maxOutput int, messages ...provider.Message) provider.Request {
		return provider.Request{
			Model:            c.Model,
			System:           "You test a connection. When asked to echo a word, call the echo tool with it.",
			Messages:         messages,
			Tools:            tools,
			Sampling:         provider.Sampling{MaxOutput: &maxOutput, Temperature: &temperature},
			PreserveThinking: c.PreserveThinking,
		}
	}

	ask := provider.UserMessage("Echo the word ping.")
	first, err := collect(ctx, c.Provider, request(firstMaxOutput, ask))
	switch {
	case err != nil:
		return fail("the first request failed: %v", err)
	case first.stop == "length":
		return fail("the model used its %d tokens of output before it called the tool", firstMaxOutput)
	case len(first.message.ToolCalls) != 1 || first.message.ToolCalls[0].Name != "echo":
		return fail("the model did not call the tool it was asked for")
	case !first.usage.InputTokens.Reported() || !first.usage.OutputTokens.Reported():
		return fail("the first response stated no token usage")
	}
	out.Reasoning = len(first.message.ReasoningItems) > 0

	call := first.message.ToolCalls[0]
	second := request(secondMaxOutput, ask, first.message, provider.ToolResultMessage(call.ID, "ping", false))
	predicted := 0
	if c.Predict != nil {
		if p, err := c.Predict(ctx, second); err == nil && p.Method == provider.MethodExact {
			out.Counted, predicted = true, p.Tokens
		}
	}
	reply, err := collect(ctx, c.Provider, second)
	switch {
	case err != nil:
		return fail("sending the tool result and the reasoning back failed: %v", err)
	case !reply.usage.InputTokens.Reported():
		return fail("the second response stated no token usage")
	case !reply.usage.CachedTokens.Reported():
		return fail("the second response did not say how many prompt tokens were cached")
	}
	out.CacheMetrics = true
	if out.Counted {
		prompt, _ := reply.usage.InputTokens.Value()
		out.Drift = predicted - prompt
		if out.Drift != 0 {
			return fail("exact counting is %d tokens off the endpoint's own count under the Responses API", out.Drift)
		}
	}
	out.Verdict = Passed
	return out
}

// answer is one response read to the end.
type answer struct {
	message provider.Message
	stop    string
	usage   provider.Usage
}

// collect streams one request to the end within the check's time limit and
// assembles the assistant message it produced, with its reasoning items as the
// endpoint returned them.
func collect(ctx context.Context, p provider.Provider, req provider.Request) (answer, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	events, err := p.Stream(ctx, req)
	if err != nil {
		return answer{}, err
	}
	var (
		text, reasoning strings.Builder
		a               answer
		done            bool
	)
	for e := range events {
		switch e.Kind {
		case provider.KindTextDelta:
			text.WriteString(e.Text)
		case provider.KindReasoningDelta:
			reasoning.WriteString(e.ReasoningDelta)
		case provider.KindReasoningItem:
			a.message.ReasoningItems = append(a.message.ReasoningItems, e.ReasoningItem)
		case provider.KindToolCall:
			a.message.ToolCalls = append(a.message.ToolCalls, e.ToolCall)
		case provider.KindUsage:
			a.usage = a.usage.Merge(e.Usage)
		case provider.KindDone:
			a.stop, done = e.StopReason, true
		case provider.KindError:
			return answer{}, e.Err
		}
	}
	if !done {
		if err := ctx.Err(); err != nil {
			return answer{}, err
		}
		return answer{}, errors.New("the response ended without finishing")
	}
	a.message.Role = provider.RoleAssistant
	a.message.Content, a.message.Reasoning = text.String(), reasoning.String()
	return a, nil
}
