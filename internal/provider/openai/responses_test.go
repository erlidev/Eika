package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
)

// fixture reads a file of internal/provider/testdata/responses.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "responses", filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

// responsesServer replays one stream, or an error status, and keeps the body
// of every request it was sent.
type responsesServer struct {
	*httptest.Server
	bodies [][]byte
	path   string
}

func newResponsesServer(t *testing.T, status int, contentType string, body []byte) *responsesServer {
	t.Helper()
	s := &responsesServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.bodies = append(s.bodies, raw)
		s.path = r.URL.Path
		w.Header().Set("Content-Type", contentType)
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "3")
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s
}

// stream replays an SSE fixture.
func stream(t *testing.T, name string) *responsesServer {
	return newResponsesServer(t, http.StatusOK, "text/event-stream", fixture(t, name))
}

// newResponses builds the Responses provider on a test server.
func newResponses(t *testing.T, url string, hosted bool) provider.Provider {
	t.Helper()
	p, err := openai.NewResponses(provider.Endpoint{BaseURL: url, APIKey: "secret", API: provider.APIResponses, Hosted: hosted})
	if err != nil {
		t.Fatalf("NewResponses: %v", err)
	}
	return p
}

// sent decodes the body of the first request a server received.
func (s *responsesServer) sent(t *testing.T) map[string]any {
	t.Helper()
	if len(s.bodies) == 0 {
		t.Fatal("the server received no request")
	}
	var body map[string]any
	if err := json.Unmarshal(s.bodies[0], &body); err != nil {
		t.Fatalf("decode the request: %v\n%s", err, s.bodies[0])
	}
	return body
}

// document decodes JSON into a value that compares with DeepEqual.
func document(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

// response is a whole stream, assembled.
type response struct {
	text, reasoning string
	items           []json.RawMessage
	calls           []provider.ToolCall
	usage           provider.Usage
	timings         provider.Timings
	usages          int
	stop            string
	err             error
}

func read(t *testing.T, p provider.Provider, req provider.Request) response {
	t.Helper()
	if req.Model == "" {
		req.Model = "qwen3.8-27b"
	}
	events, err := p.Stream(context.Background(), req)
	if err != nil {
		return response{err: err}
	}
	var r response
	for e := range events {
		switch e.Kind {
		case provider.KindTextDelta:
			r.text += e.Text
		case provider.KindReasoningDelta:
			r.reasoning += e.ReasoningDelta
		case provider.KindReasoningItem:
			r.items = append(r.items, e.ReasoningItem)
		case provider.KindToolCall:
			r.calls = append(r.calls, e.ToolCall)
		case provider.KindUsage:
			r.usage, r.timings = e.Usage, e.Timings
			r.usages++
		case provider.KindDone:
			r.stop = e.StopReason
		case provider.KindError:
			r.err = e.Err
		}
	}
	return r
}

// bench is the conversation the llama.cpp recordings were made with.
var bench = provider.Request{
	Model:            "qwen3.8-27b",
	System:           "You are Eika. Use tools when asked.",
	PreserveThinking: true,
	Sampling:         provider.Sampling{MaxOutput: ptr(400), Temperature: ptr(0.0)},
	Tools: []provider.ToolDef{{
		Name: "ls", Description: "List a directory",
		Schema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
	}},
	Messages: []provider.Message{provider.UserMessage("List the files in /tmp using the ls tool, then tell me how many there are.")},
}

func TestResponsesRoundTripsTheRecordedLlamaCppToolCall(t *testing.T) {
	s := stream(t, "llamacpp/tool_call.sse")
	r := read(t, newResponses(t, s.URL, false), bench)
	if r.err != nil {
		t.Fatalf("stream: %v", r.err)
	}
	if s.path != "/responses" {
		t.Errorf("path = %q, want /responses", s.path)
	}
	if got, want := s.sent(t), document(t, fixture(t, "llamacpp/tool_call_request.json")); !reflect.DeepEqual(got, want) {
		t.Errorf("request differs from the recorded one:\n got  %v\n want %v", got, want)
	}
	want := provider.ToolCall{ID: "call_YgrJpvwhoAzClkf28PeIO8UH2bajADOx", Name: "ls", Arguments: `{"path":"/tmp"}`}
	if !reflect.DeepEqual(r.calls, []provider.ToolCall{want}) || r.stop != "tool_calls" || !strings.HasPrefix(r.reasoning, "The user is asking") || len(r.items) != 1 {
		t.Errorf("calls %+v, stop %q, reasoning %q, %d items", r.calls, r.stop, r.reasoning, len(r.items))
	}
}

func TestResponsesReadsUsageAndTimingsFromTheCompletedEvent(t *testing.T) {
	s := stream(t, "llamacpp/answer.sse")
	r := read(t, newResponses(t, s.URL, false), bench)
	if r.err != nil {
		t.Fatalf("stream: %v", r.err)
	}
	// llama.cpp states timings beside the response object, and no reasoning count.
	want := provider.Usage{
		InputTokens: provider.Reported(415), OutputTokens: provider.Reported(54), TotalTokens: provider.Reported(469),
		CachedTokens: provider.Reported(391), PrefilledTokens: provider.Reported(24),
	}
	if r.usage != want || r.usages != 1 {
		t.Errorf("usage = %+v in %d events, want %+v in 1", r.usage, r.usages, want)
	}
	if !r.timings.HasPrompt() || !r.timings.HasDecode() || r.timings.DecodeTokens != 54 || r.timings.Source != provider.TimedByEndpoint {
		t.Errorf("timings = %+v, want both phases from the endpoint", r.timings)
	}
	if r.text != "Here are the files in /tmp:\n\n1. a.txt\n2. b.txt\n3. c.txt\n\nThere are **3** files in total." || r.stop != "stop" {
		t.Errorf("text = %q, stop %q", r.text, r.stop)
	}
}

func TestResponsesSendsBackTheReasoningItemAsReturned(t *testing.T) {
	first := read(t, newResponses(t, stream(t, "llamacpp/tool_call.sse").URL, false), bench)
	call := first.calls[0]
	reply := provider.AssistantMessageWithReasoning("", first.reasoning, first.calls)
	reply.ReasoningItems = first.items

	req := bench
	req.Messages = append(slices.Clone(bench.Messages), reply, provider.ToolResultMessage(call.ID, "a.txt\nb.txt\nc.txt", false))
	s := stream(t, "llamacpp/answer.sse")
	read(t, newResponses(t, s.URL, false), req)
	if got, want := s.sent(t), document(t, fixture(t, "llamacpp/answer_request.json")); !reflect.DeepEqual(got, want) {
		t.Errorf("second request differs from the recorded one:\n got  %v\n want %v", got, want)
	}
}

func TestResponsesBuildsAnItemFromReasoningTextForALocalServerOnly(t *testing.T) {
	// A message a Chat Completions endpoint produced has reasoning text and no
	// item. A local server is sent an item holding the text; a hosted API
	// accepts only items it returned and gets none.
	history := []provider.Message{
		provider.UserMessage("hi"),
		provider.AssistantMessageWithReasoning("hello", "greet back", nil),
		provider.UserMessage("again"),
	}
	for _, c := range []struct {
		name   string
		hosted bool
		want   []string
	}{
		{"local", false, []string{"message", "reasoning", "message", "message"}},
		{"hosted", true, []string{"message", "message", "message"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := stream(t, "llamacpp/answer.sse")
			req := bench
			req.Messages = history
			read(t, newResponses(t, s.URL, c.hosted), req)
			var kinds []string
			for _, it := range s.sent(t)["input"].([]any) {
				kinds = append(kinds, it.(map[string]any)["type"].(string))
			}
			if !reflect.DeepEqual(kinds, c.want) {
				t.Errorf("input items = %v, want %v", kinds, c.want)
			}
			if !c.hosted {
				item := s.sent(t)["input"].([]any)[1].(map[string]any)
				text := item["content"].([]any)[0].(map[string]any)
				if text["type"] != "reasoning_text" || text["text"] != "greet back" {
					t.Errorf("synthesised item = %v", item)
				}
			}
		})
	}
}

func TestResponsesSendsAHostedAPIOnlyWhatTheSpecificationDefines(t *testing.T) {
	topK, minP, seed, freq := 20, 0.05, int64(7), 0.5
	req := bench
	req.Sampling = provider.Sampling{
		MaxOutput: ptr(100), Temperature: ptr(0.2), TopP: ptr(0.9),
		TopK: &topK, MinP: &minP, Seed: &seed, FrequencyPenalty: &freq, PresencePenalty: &freq,
		Stop: []string{"END"}, ReasoningEffort: ptr("high"),
	}
	local := map[string]any{"top_k": 20.0, "min_p": 0.05, "seed": 7.0, "frequency_penalty": 0.5, "presence_penalty": 0.5, "stop": []any{"END"}, "preserve_thinking": true}
	for _, hosted := range []bool{true, false} {
		s := stream(t, "llamacpp/answer.sse")
		read(t, newResponses(t, s.URL, hosted), req)
		body := s.sent(t)
		for field, want := range map[string]any{"max_output_tokens": 100.0, "temperature": 0.2, "top_p": 0.9, "reasoning": map[string]any{"effort": "high"}} {
			if !reflect.DeepEqual(body[field], want) {
				t.Errorf("hosted %v: %s = %v, want %v", hosted, field, body[field], want)
			}
		}
		for field, want := range local {
			if got, present := body[field]; hosted && present {
				t.Errorf("hosted: %s = %v was sent to a hosted API", field, got)
			} else if !hosted && !reflect.DeepEqual(got, want) {
				t.Errorf("local: %s = %v, want %v", field, got, want)
			}
		}
		for _, tool := range body["tools"].([]any) {
			strict, set := tool.(map[string]any)["strict"]
			if set != hosted || (set && strict != false) {
				t.Errorf("hosted %v: tool strict = %v (set %v): a hosted API defaults to strict, a local server does not know the field", hosted, strict, set)
			}
		}
	}
}

func TestResponsesReportsAResponseCutOffByTheOutputLimit(t *testing.T) {
	// llama.cpp reports a response that hit max_output_tokens as completed:
	// the output count reaching the limit is how it shows. The recording is
	// cut at 20 tokens, all reasoning.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		raw, _ := os.ReadFile(filepath.Join("..", "testdata", "usage", "llamacpp", "responses_cut_in_reasoning.sse"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	req := bench
	req.Sampling.MaxOutput = ptr(20)
	r := read(t, newResponses(t, srv.URL, false), req)
	if r.err != nil || r.stop != "length" || len(r.items) != 1 || r.text != "" {
		t.Errorf("stop %q, %d items, text %q, err %v; want length with the reasoning kept", r.stop, len(r.items), r.text, r.err)
	}
	// The same recording under a larger limit is a response that finished.
	req.Sampling.MaxOutput = ptr(21)
	if r := read(t, newResponses(t, srv.URL, false), req); r.stop != "stop" {
		t.Errorf("stop under a larger limit = %q, want stop", r.stop)
	}
}

func TestResponsesDropsAToolCallTheOutputLimitCutOff(t *testing.T) {
	// The completed event of a response cut off in a function call's
	// arguments: the call must not run.
	stream := "event: response.output_item.added\ndata: " + `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"c1","name":"ls","arguments":""}}` + "\n\n" +
		"event: response.function_call_arguments.delta\ndata: " + `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"pa"}` + "\n\n" +
		"event: response.incomplete\ndata: " + `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":5,"output_tokens":4,"total_tokens":9}}}` + "\n\n"
	s := newResponsesServer(t, http.StatusOK, "text/event-stream", []byte(stream))
	r := read(t, newResponses(t, s.URL, false), bench)
	if r.stop != "length" || len(r.calls) != 0 || r.err != nil {
		t.Errorf("stop %q, calls %+v, err %v; want length and no call", r.stop, r.calls, r.err)
	}
}

func TestResponsesClassifiesFailures(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		ctype     string
		body      func(t *testing.T) []byte
		retryable bool
		overflow  bool
	}{
		{"failed response is a server error", 200, "text/event-stream", func(t *testing.T) []byte { return fixture(t, "openai/failed.sse") }, true, false},
		{"llama.cpp overflow, recorded", 400, "application/json", func(t *testing.T) []byte { return fixture(t, "llamacpp/overflow.json") }, false, true},
		{"rate limit", 429, "application/json", func(*testing.T) []byte { return []byte(`{"error":{"message":"slow down","type":"rate_limit"}}`) }, true, false},
		{"stream ends with no completed response", 200, "text/event-stream", func(*testing.T) []byte {
			return []byte("data: " + `{"type":"response.output_text.delta","delta":"hi"}` + "\n\n")
		}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newResponsesServer(t, c.status, c.ctype, c.body(t))
			r := read(t, newResponses(t, s.URL, false), bench)
			if r.err == nil {
				t.Fatal("no error")
			}
			if got := provider.Retryable(r.err); got != c.retryable {
				t.Errorf("Retryable = %v, want %v (%v)", got, c.retryable, r.err)
			}
			if got := provider.ContextOverflow(r.err); got != c.overflow {
				t.Errorf("ContextOverflow = %v, want %v (%v)", got, c.overflow, r.err)
			}
			if c.status == 429 && provider.RetryAfter(r.err).Seconds() != 3 {
				t.Errorf("RetryAfter = %v, want 3s", provider.RetryAfter(r.err))
			}
			var perr *provider.Error
			if c.status != 200 && !errors.As(r.err, &perr) {
				t.Errorf("error %v is not a *provider.Error", r.err)
			}
		})
	}
}
