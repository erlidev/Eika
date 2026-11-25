package probe_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/provider/providertest"
)

var reasoningItem = json.RawMessage(`{"id":"rs_1","type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"echo it"}],"encrypted_content":""}`)

// toolCall is a first response: reasoning, a call to echo, and usage.
func toolCall(usage provider.Usage, reasoning bool) providertest.Step {
	events := []provider.Event{}
	if reasoning {
		events = append(events,
			provider.Event{Kind: provider.KindReasoningDelta, ReasoningDelta: "echo it"},
			provider.Event{Kind: provider.KindReasoningItem, ReasoningItem: reasoningItem})
	}
	call := providertest.Call("call_1", "echo", map[string]string{"word": "ping"})
	events = append(events,
		provider.Event{Kind: provider.KindToolCall, ToolCall: call},
		provider.Event{Kind: provider.KindUsage, Usage: usage},
		provider.Done("tool_calls"))
	return providertest.Stream(events...)
}

// answer is a second response with the given usage.
func answer(usage provider.Usage) providertest.Step {
	return providertest.Stream(
		provider.TextDelta("ping"),
		provider.Event{Kind: provider.KindUsage, Usage: usage},
		provider.Done("stop"))
}

func stated(input, output, cached int) provider.Usage {
	u := providertest.Usage(input, output, input+output)
	u.CachedTokens = provider.Reported(cached)
	return u
}

func TestCheckResponsesPassesAToolCallRoundTripWithReasoning(t *testing.T) {
	p := providertest.New(toolCall(providertest.Usage(40, 20, 60), true), answer(stated(70, 3, 60)))
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	got := probe.CheckResponses(context.Background(), probe.ResponsesCheck{
		Provider: p, Model: "qwen", Build: "b11942", PreserveThinking: true, Now: func() time.Time { return now },
	})
	want := probe.Conformance{
		Verdict: probe.Passed, Model: "qwen", Build: "b11942", CheckedAt: now, Reasoning: true, CacheMetrics: true,
	}
	if got != want {
		t.Errorf("conformance = %+v, want %+v", got, want)
	}

	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("the check sent %d requests, want 2", len(reqs))
	}
	for i, r := range reqs {
		if r.Model != "qwen" || r.Sampling.MaxOutput == nil || *r.Sampling.MaxOutput > 512 || !r.PreserveThinking || len(r.Tools) != 1 {
			t.Errorf("request %d = %+v: want the model, a small output limit, preserved thinking and one tool", i+1, r)
		}
	}
	// The second request carries the first response as it was stored, with
	// its reasoning item untouched, and the tool's result.
	msgs := reqs[1].Messages
	if len(msgs) != 3 || msgs[1].Role != provider.RoleAssistant || msgs[2].Role != provider.RoleTool || msgs[2].ToolCallID != "call_1" {
		t.Fatalf("second request messages = %+v", msgs)
	}
	if !reflect.DeepEqual(msgs[1].ReasoningItems, provider.ReasoningItems{reasoningItem}) || msgs[1].Reasoning != "echo it" || len(msgs[1].ToolCalls) != 1 {
		t.Errorf("assistant message = %+v, want the reasoning item, the reasoning text and the call", msgs[1])
	}
}

func TestCheckResponsesFailsForWhatTheLoopCannotUse(t *testing.T) {
	counted := func(tokens int, method provider.Method) func(context.Context, provider.Request) (provider.Prediction, error) {
		return func(context.Context, provider.Request) (provider.Prediction, error) {
			return provider.Prediction{Tokens: tokens, Method: method}, nil
		}
	}
	cases := []struct {
		name    string
		steps   []providertest.Step
		predict func(context.Context, provider.Request) (provider.Prediction, error)
		reason  string
		drift   int
		counted bool
	}{
		{"the first request fails", []providertest.Step{providertest.Fail(errors.New("404 page not found"))}, nil, "the first request failed: 404 page not found", 0, false},
		{"the model runs out of tokens", []providertest.Step{providertest.Stream(provider.Done("length"))}, nil, "the model used its 512 tokens of output before it called the tool", 0, false},
		{"the model answers in words", []providertest.Step{providertest.Text("ping")}, nil, "the model did not call the tool it was asked for", 0, false},
		{"no usage", []providertest.Step{toolCall(provider.Usage{}, true)}, nil, "the first response stated no token usage", 0, false},
		{
			"the reasoning item is refused",
			[]providertest.Step{toolCall(providertest.Usage(40, 20, 60), true), providertest.Fail(errors.New("Item 'rs_1' was provided without its required following item"))},
			nil, "sending the tool result and the reasoning back failed: Item 'rs_1' was provided without its required following item", 0, false,
		},
		{
			"no cached count",
			[]providertest.Step{toolCall(providertest.Usage(40, 20, 60), true), answer(providertest.Usage(70, 3, 73))},
			nil, "the second response did not say how many prompt tokens were cached", 0, false,
		},
		{
			"exact counting drifts",
			[]providertest.Step{toolCall(providertest.Usage(40, 20, 60), true), answer(stated(70, 3, 60))},
			counted(73, provider.MethodExact), "exact counting is 3 tokens off the endpoint's own count under the Responses API", 3, true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := probe.CheckResponses(context.Background(), probe.ResponsesCheck{
				Provider: providertest.New(c.steps...), Model: "m", PreserveThinking: true, Predict: c.predict,
			})
			if got.Verdict != probe.Failed || got.Reason != c.reason || got.Drift != c.drift || got.Counted != c.counted {
				t.Errorf("conformance = %+v, want failed %q, drift %d, counted %v", got, c.reason, c.drift, c.counted)
			}
		})
	}
}

func TestCheckResponsesCountsTheSecondRequestTheWayTheSessionDoes(t *testing.T) {
	var asked provider.Request
	predict := func(_ context.Context, req provider.Request) (provider.Prediction, error) {
		asked = req
		return provider.Prediction{Tokens: 70, Method: provider.MethodExact}, nil
	}
	p := providertest.New(toolCall(providertest.Usage(40, 20, 60), true), answer(stated(70, 3, 60)))
	got := probe.CheckResponses(context.Background(), probe.ResponsesCheck{Provider: p, Model: "m", PreserveThinking: true, Predict: predict})
	if got.Verdict != probe.Passed || !got.Counted || got.Drift != 0 {
		t.Errorf("conformance = %+v, want passed, counted, no drift", got)
	}
	if !reflect.DeepEqual(asked, p.Requests()[1]) {
		t.Errorf("the count was of %+v, not the request sent %+v", asked, p.Requests()[1])
	}

	// A count that is not exact says nothing about drift, and does not fail the check.
	for _, c := range []struct {
		name    string
		predict func(context.Context, provider.Request) (provider.Prediction, error)
	}{
		{"estimated", func(context.Context, provider.Request) (provider.Prediction, error) {
			return provider.Prediction{Tokens: 999, Method: provider.MethodEstimated}, nil
		}},
		{"failed", func(context.Context, provider.Request) (provider.Prediction, error) {
			return provider.Prediction{}, errors.New("no route")
		}},
	} {
		p := providertest.New(toolCall(providertest.Usage(40, 20, 60), true), answer(stated(70, 3, 60)))
		got := probe.CheckResponses(context.Background(), probe.ResponsesCheck{Provider: p, Model: "m", Predict: c.predict})
		if got.Verdict != probe.Passed || got.Counted {
			t.Errorf("%s count: %+v, want passed and not counted", c.name, got)
		}
	}
}

func TestConformanceIsCurrentForTheBuildAndModelItRanOn(t *testing.T) {
	c := probe.Conformance{Verdict: probe.Passed, Build: "b11942", Model: "qwen"}
	for _, tc := range []struct {
		build, model string
		current      bool
	}{
		{"b11942", "qwen", true},
		{"b12000", "qwen", false},
		{"b11942", "llama", false},
		{"b11942", "", false},
	} {
		if got := c.Current(tc.build, tc.model); got != tc.current {
			t.Errorf("Current(%q, %q) = %v, want %v", tc.build, tc.model, got, tc.current)
		}
	}
	if (probe.Conformance{}).Current("", "") {
		t.Error("a conformance that never ran is current")
	}
	skipped := probe.Conformance{Verdict: probe.Skipped, Build: "b1"}
	if !skipped.Current("b1", "") || skipped.Current("b1", "qwen") {
		t.Error("a skipped check is stale once a model exists, and not before")
	}
}
