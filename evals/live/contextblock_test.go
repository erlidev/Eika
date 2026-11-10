package live_test

import (
	"context"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/live"
)

func TestContextBlockPrefixPassesWhenEachCallFindsThePreviousPromptCached(t *testing.T) {
	cs := newChatServer(t,
		reply{prompt: 100, completion: 5, cacheN: 0, promptN: 100, promptMS: 90, timings: true, text: "Stored answers."},
		reply{prompt: 140, completion: 5, cacheN: 98, promptN: 42, promptMS: 30, timings: true, text: "It keeps the cache."},
		reply{prompt: 180, completion: 5, cacheN: 138, promptN: 42, promptMS: 30, timings: true, text: "A right to run."},
	)
	res := live.ContextBlockPrefix("fixed").Execute(context.Background(), connect(t, cs.URL))
	if !res.Passed {
		t.Fatalf("scenario failed: %s", res.Error)
	}
	if len(res.Calls) != 3 {
		t.Fatalf("%d calls, want one for each of the three turns", len(res.Calls))
	}
	// Each request carries the block of every earlier message, with the
	// scripted clock's own minute: seven minutes apart, never the same.
	last := cs.requests[2]["messages"].([]any)
	var times []string
	for _, m := range last {
		msg := m.(map[string]any)
		if text, _ := msg["content"].(string); msg["role"] == "user" {
			if i := strings.Index(text, "time: "); i >= 0 {
				times = append(times, text[i:i+27])
			}
		}
	}
	if len(times) != 3 || times[0] == times[1] || times[1] == times[2] {
		t.Errorf("the last request's user messages state the times %q, want three different ones", times)
	}
}

func TestContextBlockPrefixFailsWhenThePrefixChanged(t *testing.T) {
	cs := newChatServer(t,
		reply{prompt: 100, completion: 5, cacheN: 0, promptN: 100, promptMS: 90, timings: true, text: "Stored answers."},
		reply{prompt: 140, completion: 5, cacheN: 12, promptN: 128, promptMS: 90, timings: true, text: "It keeps the cache."},
		reply{prompt: 180, completion: 5, cacheN: 12, promptN: 168, promptMS: 90, timings: true, text: "A right to run."},
	)
	res := live.ContextBlockPrefix("fixed").Execute(context.Background(), connect(t, cs.URL))
	if res.Passed || !strings.Contains(res.Error, "the prefix changed between calls") {
		t.Fatalf("passed = %v, error = %q, want a failure that names the prefix", res.Passed, res.Error)
	}
}
