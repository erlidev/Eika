package live_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/erlidev/eika/evals/live"
	"github.com/erlidev/eika/internal/provider"
)

// chatServer answers /v1/chat/completions with the next scripted reply, in
// llama.cpp's streaming shape, and records each request body. Everything else
// is the bench server of benchServer.
type chatServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []map[string]any
}

// reply is one scripted response: the usage block and the timings llama.cpp
// attaches to the last chunk. Without timings the block is left out, as a
// server that does not measure leaves it.
type reply struct {
	prompt, completion int
	cacheN, promptN    int
	promptMS           float64
	timings            bool
	text               string
	// usageCached, when set, is the cached count the usage block states
	// beside the prompt count, as a server with prompt_tokens_details does.
	usageCached *int
	// textTokens is what the server's tokenizer counts in the request, when it
	// differs from the prompt count because the request holds a picture.
	textTokens int
}

func requestFor(text string) provider.Request {
	return provider.Request{Messages: []provider.Message{provider.UserMessage(text)}}
}

func newChatServer(t *testing.T, replies ...reply) *chatServer {
	t.Helper()
	bench := benchServer(t)
	cs := &chatServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			// A different prompt for each request, as a growing conversation
			// renders: the counter remembers the count of a prompt it has seen.
			cs.mu.Lock()
			n := len(cs.requests)
			cs.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"prompt":"the rendered prompt of request %d"}`, n)
			return
		case "/tokenize":
			// The tokenizer counts the prompt of the request about to be
			// sent, which is the next scripted reply's.
			cs.mu.Lock()
			n := len(cs.requests)
			cs.mu.Unlock()
			count := 0
			if n < len(replies) {
				if count = replies[n].textTokens; count == 0 {
					count = replies[n].prompt
				}
			}
			_, _ = fmt.Fprintf(w, `{"tokens":[%s]}`, strings.TrimSuffix(strings.Repeat("7,", count), ","))
			return
		case "/v1/chat/completions":
		default:
			bench.Config.Handler.ServeHTTP(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cs.mu.Lock()
		n := len(cs.requests)
		cs.requests = append(cs.requests, body)
		cs.mu.Unlock()
		if n >= len(replies) {
			http.Error(w, "no scripted reply", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, sse(replies[n]))
	}))
	t.Cleanup(cs.Close)
	return cs
}

// sse renders a reply as llama.cpp streams it: a delta with the text, the
// finish chunk, then a chunk with the usage and the timings.
func sse(r reply) string {
	text, _ := json.Marshal(r.text)
	var b strings.Builder
	fmt.Fprintf(&b, "data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":%s}}]}\n\n", text)
	b.WriteString("data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}]}\n\n")
	details := ""
	if r.usageCached != nil {
		details = fmt.Sprintf(`,"prompt_tokens_details":{"cached_tokens":%d}`, *r.usageCached)
	}
	tail := fmt.Sprintf(`{"choices":[],"usage":{"completion_tokens":%d,"prompt_tokens":%d,"total_tokens":%d%s}`,
		r.completion, r.prompt, r.prompt+r.completion, details)
	if r.timings {
		tail += fmt.Sprintf(`,"timings":{"cache_n":%d,"prompt_n":%d,"prompt_ms":%g,"predicted_n":%d,"predicted_ms":40}`,
			r.cacheN, r.promptN, r.promptMS, r.completion)
	}
	fmt.Fprintf(&b, "data: %s}\n\ndata: [DONE]\n\n", tail)
	return b.String()
}

func connect(t *testing.T, url string) *live.Target {
	t.Helper()
	target, err := live.Connect(context.Background(), live.Env{live.EndpointVar: url, live.KeyVar: testKey})
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestCompleteReadsCacheAndPrefillFromARecordedStream(t *testing.T) {
	bench := benchServer(t)
	stream := fixture(t, "stream.sse")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(stream)
			return
		case "/tokenize":
			_, _ = fmt.Fprintf(w, `{"tokens":[%s]}`, strings.TrimSuffix(strings.Repeat("7,", 18), ","))
			return
		}
		bench.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	res := live.Scenario{
		Name: "recorded",
		Run: func(ctx context.Context, tg *live.Target) ([]live.Call, error) {
			call, text, err := tg.Complete(ctx, "only", requestFor("Say hi in three words."))
			if text != "Hello there friend" {
				t.Errorf("text = %q", text)
			}
			return []live.Call{call}, err
		},
	}.Execute(context.Background(), connect(t, srv.URL))

	if !res.Passed {
		t.Fatalf("scenario failed: %s", res.Error)
	}
	// llama.cpp b11942: prompt_tokens 18 counts the 14 cached ones, and
	// prompt_n 4 is what it evaluated.
	c := res.Calls[0]
	if intOf(c.PromptTokens) != 18 || intOf(c.CompletionTokens) != 4 {
		t.Errorf("tokens = %d prompt, %d completion", intOf(c.PromptTokens), intOf(c.CompletionTokens))
	}
	if c.PrefilledTokens == nil || *c.PrefilledTokens != 4 || c.CachedTokens == nil || *c.CachedTokens != 14 {
		t.Errorf("%v, want 4 prefilled and 14 cached", c)
	}
	if c.CacheHitRate == nil || math.Abs(*c.CacheHitRate-14.0/18.0) > 1e-9 {
		t.Errorf("%v, want a hit rate of 14/18", c)
	}
	if c.PrefillSeconds == nil || math.Abs(*c.PrefillSeconds-0.060765) > 1e-9 {
		t.Errorf("%v, want 0.060765 s of prefill", c)
	}
	if c.Method != "exact" || c.Predicted == nil || *c.Predicted != 18 || c.Drift == nil || *c.Drift != 0 ||
		res.Summary.Drift == nil || *res.Summary.Drift != 0 {
		t.Errorf("%v, want an exact prediction of 18 tokens with no drift", c)
	}
}

func TestCompleteWithoutTimingsLeavesCacheAndPrefillAbsent(t *testing.T) {
	cs := newChatServer(t, reply{prompt: 30, completion: 3, text: "ok"})
	res := live.Scenario{
		Name: "untimed",
		Run: func(ctx context.Context, tg *live.Target) ([]live.Call, error) {
			call, _, err := tg.Complete(ctx, "only", requestFor("hi"))
			return []live.Call{call}, err
		},
	}.Execute(context.Background(), connect(t, cs.URL))

	if !res.Passed {
		t.Fatalf("scenario failed: %s", res.Error)
	}
	c := res.Calls[0]
	if c.PrefilledTokens != nil || c.CachedTokens != nil || c.CacheHitRate != nil || c.PrefillSeconds != nil {
		t.Errorf("a number was invented for an endpoint that reported none: %+v", c)
	}
	if res.Summary.CachedTokens != nil || res.Summary.CacheHitRate != nil || res.Summary.PrefillSeconds != nil {
		t.Errorf("summary invented a number: %+v", res.Summary)
	}
	if intOf(res.Summary.PromptTokens) != 30 {
		t.Errorf("summary prompt tokens = %v, want 30", res.Summary.PromptTokens)
	}
}

func TestCompleteTakesTheCachedCountFromUsageNotFromPrefill(t *testing.T) {
	// A server with no prompt_n (so nothing to subtract from the prompt count)
	// that states the cached count in its usage block: the count is read, and
	// a stated 0 stays a 0 rather than an absent value.
	zero, some := 0, 30
	cs := newChatServer(t,
		reply{prompt: 40, completion: 3, text: "a", usageCached: &zero},
		reply{prompt: 50, completion: 3, text: "b", usageCached: &some},
	)
	res := live.Scenario{
		Name: "usage cached",
		Run: func(ctx context.Context, tg *live.Target) ([]live.Call, error) {
			first, _, err := tg.Complete(ctx, "first", requestFor("hi"))
			if err != nil {
				return []live.Call{first}, err
			}
			second, _, err := tg.Complete(ctx, "second", requestFor("hi again"))
			return []live.Call{first, second}, err
		},
	}.Execute(context.Background(), connect(t, cs.URL))
	if !res.Passed {
		t.Fatalf("scenario failed: %s", res.Error)
	}
	first, second := res.Calls[0], res.Calls[1]
	if first.CachedTokens == nil || *first.CachedTokens != 0 || first.CacheHitRate == nil || *first.CacheHitRate != 0 {
		t.Errorf("first = %+v, want a stated 0 cached and a hit rate of 0", first)
	}
	if second.CachedTokens == nil || *second.CachedTokens != 30 || second.PrefilledTokens != nil || second.ReasoningTokens != nil {
		t.Errorf("second = %+v, want 30 cached and no prefilled or reasoning count, which nothing stated", second)
	}
	if s := res.Summary; s.CachedTokens == nil || *s.CachedTokens != 30 || s.CacheHitRate == nil || math.Abs(*s.CacheHitRate-30.0/90.0) > 1e-9 {
		t.Errorf("summary = %+v, want 30 of 90 cached", s)
	}
}

func TestCacheReuseReportsBothTurnsAndBoundsTheOutput(t *testing.T) {
	cs := newChatServer(t,
		reply{prompt: 400, completion: 20, cacheN: 0, promptN: 400, promptMS: 900, timings: true, text: "A hit is when the answer is already stored."},
		reply{prompt: 440, completion: 20, cacheN: 398, promptN: 42, promptMS: 80, timings: true, text: "Opening a file twice."},
	)
	res := live.CacheReuse("fixed").Execute(context.Background(), connect(t, cs.URL))
	if !res.Passed {
		t.Fatalf("scenario failed: %s", res.Error)
	}
	if len(res.Calls) != 2 || res.Calls[0].Label != "turn 1" || res.Calls[1].Label != "turn 2" {
		t.Fatalf("calls = %+v", res.Calls)
	}
	s := res.Summary
	if s.Calls != 2 || intOf(s.PromptTokens) != 840 || s.CachedTokens == nil || *s.CachedTokens != 398 {
		t.Errorf("summary = %+v", s)
	}
	if s.PrefillSeconds == nil || math.Abs(*s.PrefillSeconds-0.98) > 1e-9 {
		t.Errorf("summary = %+v, want 0.98 s of prefill", s)
	}
	if s.Drift == nil || *s.Drift != 0 || s.CountSeconds == nil {
		t.Errorf("summary = %+v, want no drift and a count time", s)
	}

	// The second request replays the first reply, and both are bounded.
	if len(cs.requests) != 2 {
		t.Fatalf("%d requests, want 2", len(cs.requests))
	}
	for i, body := range cs.requests {
		if got := body["max_completion_tokens"]; got != float64(64) {
			t.Errorf("request %d: max_completion_tokens = %v, want 64", i+1, got)
		}
		if got := body["model"]; got != "qwen3.8-27b" {
			t.Errorf("request %d: model = %v", i+1, got)
		}
	}
	messages, _ := cs.requests[1]["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("second request has %d messages, want system, user, assistant, user", len(messages))
	}
	if first, _ := messages[0].(map[string]any); !strings.Contains(fmt.Sprint(first["content"]), "Session fixed.") {
		t.Errorf("system prompt does not start with the nonce: %v", first["content"])
	}
	if reply, _ := messages[2].(map[string]any); reply["content"] != "A hit is when the answer is already stored." {
		t.Errorf("the second request does not replay the first reply: %v", reply["content"])
	}
}

func TestCacheReuseFailsWhenTheSecondTurnMissesTheCache(t *testing.T) {
	cs := newChatServer(t,
		reply{prompt: 400, completion: 20, cacheN: 0, promptN: 400, promptMS: 900, timings: true, text: "a"},
		reply{prompt: 440, completion: 20, cacheN: 12, promptN: 428, promptMS: 950, timings: true, text: "b"},
	)
	res := live.CacheReuse("fixed").Execute(context.Background(), connect(t, cs.URL))
	if res.Passed || !strings.Contains(res.Error, "did not reuse") {
		t.Fatalf("passed = %v, error = %q, want a failure about the prefix", res.Passed, res.Error)
	}
	if len(res.Calls) != 2 {
		t.Errorf("the failed scenario kept %d calls, want both for the report", len(res.Calls))
	}
}

func TestCacheReuseFailsWhenTheServerReportsNoCachedCount(t *testing.T) {
	cs := newChatServer(t,
		reply{prompt: 400, completion: 20, text: "a"},
		reply{prompt: 440, completion: 20, text: "b"},
	)
	res := live.CacheReuse("fixed").Execute(context.Background(), connect(t, cs.URL))
	if res.Passed || !strings.Contains(res.Error, "no cached token count") {
		t.Fatalf("passed = %v, error = %q", res.Passed, res.Error)
	}
}

func TestScenariosAreNamedAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range live.Scenarios() {
		if s.Name == "" || s.Run == nil || seen[s.Name] {
			t.Errorf("scenario %+v has no name, no body, or a repeated name", s)
		}
		seen[s.Name] = true
	}
	if len(seen) == 0 {
		t.Error("the suite has no scenarios")
	}
}
