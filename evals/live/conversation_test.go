package live_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/live"
	"github.com/erlidev/eika/internal/provider/providertest"
)

func conversationReplies() []reply {
	return []reply{
		{prompt: 100, completion: 3, text: "The bank and milk."},
		{prompt: 151, textTokens: 140, completion: 3, text: "A gradient."},
		{prompt: 181, textTokens: 170, completion: 3, text: "Both are notes."},
		{prompt: 80, completion: 3, text: "The bank."},
		{prompt: 90, completion: 3, text: "Monday."},
	}
}

func TestCountingConversationAcrossBothProtocols(t *testing.T) {
	for _, responses := range []bool{false, true} {
		t.Run(fmt.Sprint(responses), func(t *testing.T) {
			cs := newChatServer(t, conversationReplies()...)
			scenario := live.CountingConversation("fixed")
			if responses {
				scenario = live.OnResponses(scenario)
			}
			res := scenario.Execute(context.Background(), connect(t, cs.URL))
			if !res.Passed {
				t.Fatalf("scenario failed: %s", res.Error)
			}
			if len(res.Calls) != 5 {
				t.Fatalf("calls = %d, want 5", len(res.Calls))
			}
			for _, i := range []int{0, 3, 4} {
				c := res.Calls[i]
				if c.Method != "exact" || intOf(c.Drift) != 0 {
					t.Errorf("call %d = %+v", i, c)
				}
			}
			if res.Calls[1].Method == "exact" || res.Calls[2].Method != "anchored" || intOf(res.Calls[2].Drift) != 0 {
				t.Errorf("image calls = %+v", res.Calls[1:3])
			}
			for _, want := range []string{"call_2", "Read it.", "notes.md"} {
				if !strings.Contains(fmt.Sprint(cs.requests[0]), want) {
					t.Errorf("first request lacks %q", want)
				}
			}
			if !strings.Contains(fmt.Sprint(cs.requests[2]), "data:image/png;base64,") {
				t.Error("image missing before compaction")
			}
			after := fmt.Sprint(cs.requests[3])
			if !strings.Contains(after, "<summary>") || strings.Contains(after, "data:image/png;base64,") || strings.Contains(after, "call_2") {
				t.Errorf("compaction did not replace history: %s", after)
			}
		})
	}
}

func TestCountingConversationRejectsDrift(t *testing.T) {
	for _, i := range []int{0, 2} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			replies := conversationReplies()
			// The learned image offset is 11, so this also checks that the
			// image history is still checked after its first measurement.
			replies[i].textTokens = replies[i].prompt - 5
			if i == 2 {
				replies[i].textTokens = 165
			}
			cs := newChatServer(t, replies...)
			res := live.CountingConversation("fixed").Execute(context.Background(), connect(t, cs.URL))
			if res.Passed || !strings.Contains(res.Error, "drift") {
				t.Fatalf("result = %+v", res)
			}
		})
	}
}

func TestUsageFieldsRecordsCacheInclusionAndMissingFields(t *testing.T) {
	for _, responses := range []bool{false, true} {
		t.Run(fmt.Sprint(responses), func(t *testing.T) {
			cs := newChatServer(t,
				reply{prompt: 100, completion: 3, text: "A cache hit."},
				reply{prompt: 140, completion: 3, usageCached: ptr(100), text: "A file."},
			)
			s := live.UsageFields("fixed")
			if responses {
				s = live.OnResponses(s)
			}
			res := s.Execute(context.Background(), connect(t, cs.URL))
			if !res.Passed {
				t.Fatal(res.Error)
			}
			if !equal(res.Calls[1].PromptIncludesCached, ptr(true)) || res.Calls[0].PromptIncludesCached != nil {
				t.Errorf("calls = %+v", res.Calls)
			}
		})
	}
}

func TestUsageFieldsRejectsCountsThatExcludeCacheOrDisagree(t *testing.T) {
	for _, inconsistent := range []bool{false, true} {
		t.Run(fmt.Sprint(inconsistent), func(t *testing.T) {
			second := reply{prompt: 100, textTokens: 200, usageCached: ptr(60), completion: 3}
			if inconsistent {
				second = reply{prompt: 200, cacheN: 120, promptN: 100, timings: true, completion: 3}
			}
			cs := newChatServer(t, reply{prompt: 100, completion: 3}, second)
			res := live.UsageFields("fixed").Execute(context.Background(), connect(t, cs.URL))
			if res.Passed {
				t.Fatal("inconsistent usage passed")
			}
			if !inconsistent && !equal(res.Calls[1].PromptIncludesCached, ptr(false)) {
				t.Errorf("calls = %+v", res.Calls)
			}
		})
	}
}

func TestCountingConversationSkipsWithoutVision(t *testing.T) {
	target := &live.Target{Responses: providertest.New()}
	for _, s := range []live.Scenario{live.CountingConversation("fixed"), live.OnResponses(live.CountingConversation("fixed"))} {
		result := s.Execute(context.Background(), target)
		if result.Passed || result.Error != "" || result.SkipReason == "" || len(result.Calls) != 0 {
			t.Errorf("%s: %+v", s.Name, result)
		}
	}
}
