package live_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/live"
)

// answer is a reply long enough that the conversation is more than a
// compaction keeps.
func answer(n int) string {
	return fmt.Sprintf("Answer %d. %s", n, strings.Repeat("A server keeps the start of a prompt it has read so that it reads only what is new. ", 4))
}

func prefixReplies(afterCompaction, last int) []reply {
	return []reply{
		{prompt: 100, completion: 5, cacheN: 0, promptN: 100, promptMS: 90, timings: true, text: answer(1)},
		{prompt: 140, completion: 5, cacheN: 98, promptN: 42, promptMS: 30, timings: true, text: answer(2)},
		{prompt: 180, completion: 5, cacheN: 138, promptN: 42, promptMS: 30, timings: true, text: answer(3)},
		// The summaries the compaction asks for, one of the history and one of
		// the start of the turn it cut; the loop does not record them.
		{prompt: 200, completion: 5, cacheN: 150, promptN: 50, promptMS: 40, timings: true, text: "## Goal\nKeep the prefix."},
		{prompt: 210, completion: 5, cacheN: 190, promptN: 20, promptMS: 40, timings: true, text: "## Original Request\nWhich matters more."},
		{prompt: 120, completion: 5, cacheN: afterCompaction, promptN: 120 - afterCompaction, promptMS: 60, timings: true, text: answer(4)},
		{prompt: 160, completion: 5, cacheN: last, promptN: 160 - last, promptMS: 30, timings: true, text: answer(5)},
	}
}

func TestPrefixAcrossCompactionPassesWhenEveryCallButTheFirstAfterTheCompactionFindsTheLastPromptCached(t *testing.T) {
	cs := newChatServer(t, prefixReplies(20, 108)...)
	res := live.PrefixAcrossCompaction("fixed").Execute(context.Background(), connect(t, cs.URL))
	if !res.Passed {
		t.Fatalf("scenario failed: %s", res.Error)
	}
	if len(res.Calls) != 5 {
		t.Fatalf("%d calls, want one for each of the five turns", len(res.Calls))
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "compaction replaced the prefix") {
		t.Errorf("notes = %q, want the cold call after the compaction reported", res.Notes)
	}
	// The loops differ in the output limit, which the server must not read as
	// part of the prompt.
	limits := map[any]bool{}
	for _, i := range []int{0, 2, 5} {
		limits[cs.requests[i]["max_completion_tokens"]] = true
	}
	if len(limits) != 3 {
		t.Errorf("the loops sent the output limits %v, want three different ones", limits)
	}
}

func TestPrefixAcrossCompactionFailsWhenTheCallAfterTheColdOneDoesNotHoldAgain(t *testing.T) {
	cs := newChatServer(t, prefixReplies(20, 30)...)
	res := live.PrefixAcrossCompaction("fixed").Execute(context.Background(), connect(t, cs.URL))
	if res.Passed || !strings.Contains(res.Error, "the prefix changed between calls") {
		t.Fatalf("passed = %v, error = %q, want a failure that names the prefix", res.Passed, res.Error)
	}
}

func TestPrefixAcrossCompactionFailsWhenARestartLosesThePrefix(t *testing.T) {
	replies := prefixReplies(20, 108)
	// The call after the first restart finds only the head of its prompt.
	replies[2].cacheN = 12
	cs := newChatServer(t, replies...)
	res := live.PrefixAcrossCompaction("fixed").Execute(context.Background(), connect(t, cs.URL))
	if res.Passed || !strings.Contains(res.Error, "loop call 3") {
		t.Fatalf("passed = %v, error = %q, want a failure at the call after the restart", res.Passed, res.Error)
	}
}

func TestResponsesPrefixAcrossCompaction(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			replies := prefixReplies(20, 108)
			if lost {
				replies[6].cacheN = 30
			}
			for i := range replies {
				replies[i].usageCached = ptr(replies[i].cacheN)
			}
			cs := newChatServer(t, replies...)
			res := live.OnResponses(live.PrefixAcrossCompaction("fixed")).Execute(context.Background(), connect(t, cs.URL))
			if res.Passed == lost {
				t.Fatalf("lost = %t, passed = %t, error = %q", lost, res.Passed, res.Error)
			}
			if lost && !strings.Contains(res.Error, "prefix changed") {
				t.Fatalf("unexpected error: %s", res.Error)
			}
		})
	}
}
