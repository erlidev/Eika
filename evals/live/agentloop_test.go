package live_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/live"
)

func TestAgentLoopRecordsEveryCallExactlyPredicted(t *testing.T) {
	cs := newChatServer(t,
		reply{prompt: 180, completion: 5, text: "Call the bank on Monday."},
		reply{prompt: 215, completion: 5, text: "Milk, bread and lemons."},
	)
	res := live.AgentLoop("fixed").Execute(context.Background(), connect(t, cs.URL))
	if !res.Passed {
		t.Fatalf("scenario failed: %s", res.Error)
	}
	if len(res.Calls) != 2 {
		t.Fatalf("%d calls, want one for each of the two turns", len(res.Calls))
	}
	for _, c := range res.Calls {
		if c.Method != "exact" || c.Drift == nil || *c.Drift != 0 {
			t.Errorf("%v, want exact with no drift", c)
		}
	}
	if first := fmt.Sprint(cs.requests[0]); !strings.Contains(first, "look_up") || !strings.Contains(first, "enable_thinking") {
		t.Errorf("the first request lacks the tool or the thinking switch: %s", first)
	}
}

func TestReasoningCountLabelsATokenisedCountAndHoldsItToTheServers(t *testing.T) {
	const thought = "17 times 20 is 340 and 17 times 3 is 51 so"
	for _, tc := range []struct {
		name      string
		tokenised int
		wantPass  bool
	}{
		{"the tokenised count matches what was generated", 60, true},
		{"the tokenised count is a little off", 58, true},
		{"the tokenised count is far off", 30, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := newChatServer(t, reply{prompt: 90, completion: 60, text: "", reasoning: thought, reasoningTokens: tc.tokenised})
			res := live.ReasoningCount("fixed").Execute(context.Background(), connect(t, cs.URL))
			if res.Passed != tc.wantPass {
				t.Fatalf("passed = %v, error = %q, want %v", res.Passed, res.Error, tc.wantPass)
			}
			if len(res.Calls) != 1 {
				t.Fatalf("%d calls, want 1", len(res.Calls))
			}
			c := res.Calls[0]
			if c.ReasoningTokens == nil || *c.ReasoningTokens != tc.tokenised || c.ReasoningSource != "tokenised" {
				t.Errorf("reasoning = %v %q, want %d from the tokenizer, labelled so", c.ReasoningTokens, c.ReasoningSource, tc.tokenised)
			}
		})
	}
}

func TestAgentLoopFailsOnDriftInAnExactPrediction(t *testing.T) {
	// The tokenizer counts 150 for the first request and the server 153.
	cs := newChatServer(t,
		reply{prompt: 153, textTokens: 150, completion: 5, text: "Call the bank."},
		reply{prompt: 190, completion: 5, text: "Milk."},
	)
	res := live.AgentLoop("fixed").Execute(context.Background(), connect(t, cs.URL))
	if res.Passed || !strings.Contains(res.Error, "drift") {
		t.Fatalf("passed = %v, error = %q, want a failure about drift", res.Passed, res.Error)
	}
}
