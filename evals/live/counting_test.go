package live_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/live"
	"github.com/erlidev/eika/internal/provider"
)

func TestAnExactPredictionThatDriftsFailsTheScenario(t *testing.T) {
	// The tokenizer says 40 tokens for the first request and the server counts
	// 43: the bug the counter exists to catch.
	cs := newChatServer(t, reply{prompt: 43, textTokens: 40, completion: 2, text: "ok"})
	res := live.Scenario{
		Name: "drifting",
		Run: func(ctx context.Context, tg *live.Target) ([]live.Call, error) {
			call, _, err := tg.Complete(ctx, "only", requestFor("hi"))
			return []live.Call{call}, err
		},
	}.Execute(context.Background(), connect(t, cs.URL))
	if res.Passed || !strings.Contains(res.Error, "exact counting must not drift") {
		t.Fatalf("passed = %v, error = %q, want a failure about drift", res.Passed, res.Error)
	}
	if c := res.Calls[0]; c.Drift == nil || *c.Drift != -3 {
		t.Errorf("call = %+v, want a drift of -3", c)
	}
	if s := res.Summary; s.Drift == nil || *s.Drift != 3 {
		t.Errorf("summary drift = %v, want the absolute total 3", s.Drift)
	}
}

func TestToolsAndReasoningSendsToolsCallsAndReplayedReasoning(t *testing.T) {
	cs := newChatServer(t,
		reply{prompt: 500, completion: 5, text: "Two lines."},
		reply{prompt: 560, completion: 5, text: "The bank."},
		reply{prompt: 600, completion: 5, text: "No."},
	)
	res := live.ToolsAndReasoning("fixed").Execute(context.Background(), connect(t, cs.URL))
	if !res.Passed {
		t.Fatalf("scenario failed: %s", res.Error)
	}
	if len(res.Calls) != 3 {
		t.Fatalf("%d calls, want 3", len(res.Calls))
	}
	for _, c := range res.Calls {
		if c.Method != "exact" || c.Drift == nil || *c.Drift != 0 {
			t.Errorf("%v, want exact with no drift", c)
		}
	}
	first := fmt.Sprint(cs.requests[0])
	for _, want := range []string{"list_dir", "read_file", "tool_calls", "reasoning_content", "call_2", "enable_thinking"} {
		if !strings.Contains(first, want) {
			t.Errorf("the first request lacks %q: %s", want, first)
		}
	}
	// The last request leaves the thinking switch out.
	if strings.Contains(fmt.Sprint(cs.requests[2]), "enable_thinking") {
		t.Errorf("the third request still carries the thinking switch: %v", cs.requests[2])
	}
}

func TestImageInputIsNeverCalledExactAndLearnsThePictureCost(t *testing.T) {
	// The tokenizer counts 29 tokens of text and the server 40: the 11
	// between are the picture. The second request holds two pictures and 45
	// tokens of text: 45 + 2 x 11.
	cs := newChatServer(t,
		reply{prompt: 40, textTokens: 29, completion: 3, text: "A gradient."},
		reply{prompt: 67, textTokens: 45, completion: 3, text: "The same."},
	)
	res := live.ImageInput().Execute(context.Background(), connect(t, cs.URL))
	if !res.Passed {
		t.Fatalf("scenario failed: %s", res.Error)
	}
	first, second := res.Calls[0], res.Calls[1]
	if first.Method != string(provider.MethodEstimated) || first.Drift == nil || *first.Drift == 0 {
		t.Errorf("first = %v, want an estimate that is off", first)
	}
	if second.Method != string(provider.MethodAnchored) || second.Drift == nil || *second.Drift != 0 {
		t.Errorf("second = %v, want anchored with no drift", second)
	}
	if !strings.Contains(fmt.Sprint(cs.requests[0]), "data:image/png;base64,") {
		t.Error("the picture did not reach the request")
	}
}

func TestImageInputDoesNothingAgainstAServerWithoutVision(t *testing.T) {
	cs := newChatServer(t)
	target := connect(t, cs.URL)
	target.Server.Flags.Vision = false
	res := live.ImageInput().Execute(context.Background(), target)
	if !res.Passed || len(res.Calls) != 0 {
		t.Errorf("result = %+v, want a pass with no calls", res)
	}
}
