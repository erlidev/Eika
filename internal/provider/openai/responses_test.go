package openai_test

import (
	"bytes"
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

func TestResponsesSendsTheRequestLlamaCppWasRecordedAnswering(t *testing.T) {
	s := stream(t, "llamacpp/tool_call.sse")
	read(t, newResponses(t, s.URL, false), bench)
	if s.path != "/responses" {
		t.Errorf("path = %q, want /responses", s.path)
	}
	if got, want := s.sent(t), document(t, fixture(t, "llamacpp/tool_call_request.json")); !reflect.DeepEqual(got, want) {
		t.Errorf("request differs from the recorded one:\n got  %v\n want %v", got, want)
	}
}

func TestResponsesStreamsTheRecordedToolCallWithItsReasoning(t *testing.T) {
	s := stream(t, "llamacpp/tool_call.sse")
	r := read(t, newResponses(t, s.URL, false), bench)
	if r.err != nil {
		t.Fatalf("stream: %v", r.err)
	}
	if !strings.HasPrefix(r.reasoning, "The user is asking to list the files in /tmp") {
		t.Errorf("reasoning = %q", r.reasoning)
	}
	want := provider.ToolCall{ID: "call_YgrJpvwhoAzClkf28PeIO8UH2bajADOx", Name: "ls", Arguments: `{"path":"/tmp"}`}
	if !reflect.DeepEqual(r.calls, []provider.ToolCall{want}) {
		t.Errorf("tool calls = %+v, want %+v", r.calls, want)
	}
	if r.stop != "tool_calls" {
		t.Errorf("stop = %q, want tool_calls", r.stop)
	}
	if len(r.items) != 1 {
		t.Fatalf("got %d reasoning items, want 1", len(r.items))
	}
	// The item is exactly the one the server sent in response.output_item.done.
	var item struct{ ID, Type string }
	if err := json.Unmarshal(r.items[0], &item); err != nil || item.Type != "reasoning" || item.ID != "rs_ac9QKLaYBUrmiCC7JtF8UhunKXfFChhp" {
		t.Errorf("reasoning item = %s", r.items[0])
	}
	if !bytes.Contains(fixture(t, "llamacpp/tool_call.sse"), append([]byte(`"item":`), r.items[0]...)) {
		t.Errorf("the item is not the bytes the server sent:\n%s", r.items[0])
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

func TestResponsesKeepsAReasoningItemWhatEverItHolds(t *testing.T) {
	// An item holds fields this code has never heard of; sending it back must
	// not drop, reorder-by-meaning, or re-encode any of them.
	item := json.RawMessage(`{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"a <b> & é \"q\""}],"encrypted_content":"gAAAA+/==","status":null,"vendor":{"n":1.50,"k":[true,null]}}`)
	s := stream(t, "llamacpp/answer.sse")
	req := bench
	msg := provider.AssistantMessageWithReasoning("done", "", nil)
	msg.ReasoningItems = []json.RawMessage{item}
	req.Messages = []provider.Message{provider.UserMessage("hi"), msg, provider.UserMessage("again")}
	read(t, newResponses(t, s.URL, true), req)

	items := s.sent(t)["input"].([]any)
	if len(items) != 4 {
		t.Fatalf("input holds %d items, want user, reasoning, message, user", len(items))
	}
	if got, want := items[1], document(t, item); !reflect.DeepEqual(got, want) {
		t.Errorf("reasoning item sent = %v, want %v", got, want)
	}
	if !bytes.Contains(s.bodies[0], []byte(`"a <b> & é \"q\""`)) {
		t.Errorf("the item's text was escaped or changed in the body: %s", s.bodies[0])
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

func TestResponsesSendsNoReasoningWhenTheModelDoesNotPreserveIt(t *testing.T) {
	s := stream(t, "llamacpp/answer.sse")
	req := bench
	req.PreserveThinking = false
	msg := provider.AssistantMessageWithReasoning("hello", "greet back", nil)
	msg.ReasoningItems = []json.RawMessage{json.RawMessage(`{"id":"rs_1","type":"reasoning","summary":[]}`)}
	req.Messages = []provider.Message{provider.UserMessage("hi"), msg, provider.UserMessage("again")}
	read(t, newResponses(t, s.URL, false), req)
	body := s.sent(t)
	if bytes.Contains(s.bodies[0], []byte("reasoning")) || body["preserve_thinking"] != nil {
		t.Errorf("a model that does not preserve its thinking was sent reasoning: %s", s.bodies[0])
	}
}

func TestResponsesDropsAReasoningItemNothingFollowsOnAHostedAPI(t *testing.T) {
	// A hosted API refuses a reasoning item that has no output item after it,
	// which is what a response cut off inside its reasoning leaves. A local
	// server takes it, as Chat Completions does.
	cut := provider.AssistantMessageWithReasoning("", "thinking", nil)
	cut.ReasoningItems = []json.RawMessage{json.RawMessage(`{"id":"rs_1","type":"reasoning","summary":[]}`)}
	for _, hosted := range []bool{true, false} {
		s := stream(t, "llamacpp/answer.sse")
		req := bench
		req.Messages = []provider.Message{provider.UserMessage("hi"), cut, provider.UserMessage("go on")}
		read(t, newResponses(t, s.URL, hosted), req)
		if got, want := len(s.sent(t)["input"].([]any)), map[bool]int{true: 2, false: 3}[hosted]; got != want {
			t.Errorf("hosted %v: input holds %d items, want %d", hosted, got, want)
		}
	}
}

func TestResponsesAsksAHostedAPIForEncryptedReasoningOnlyWhenItWillSendItBack(t *testing.T) {
	for _, c := range []struct {
		name     string
		hosted   bool
		preserve bool
		include  bool
	}{
		{"hosted, preserved", true, true, true},
		{"hosted, not preserved", true, false, false},
		{"local, preserved", false, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := stream(t, "openai/tool_call_encrypted.sse")
			req := bench
			req.PreserveThinking = c.preserve
			read(t, newResponses(t, s.URL, c.hosted), req)
			body := s.sent(t)
			got, _ := body["include"].([]any)
			if has := len(got) == 1 && got[0] == "reasoning.encrypted_content"; has != c.include {
				t.Errorf("include = %v, want encrypted content: %v", body["include"], c.include)
			}
			if body["store"] != false {
				t.Errorf("store = %v, want false", body["store"])
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

func TestResponsesSendsNoneInTheFieldTheThinkingSwitchNames(t *testing.T) {
	none := provider.EffortNone
	for _, c := range []struct {
		name   string
		sw     provider.ThinkingSwitch
		hosted bool
		field  string
	}{
		{"effort", "", false, "reasoning"},
		{"template", provider.SwitchTemplate, false, "chat_template_kwargs"},
		{"thinking", provider.SwitchThinking, false, "thinking"},
		{"template on a hosted api", provider.SwitchTemplate, true, "reasoning"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := stream(t, "llamacpp/answer.sse")
			req := bench
			req.Sampling.ReasoningEffort, req.ThinkingSwitch = &none, c.sw
			read(t, newResponses(t, s.URL, c.hosted), req)
			body := s.sent(t)
			for _, field := range []string{"reasoning", "chat_template_kwargs", "thinking"} {
				if _, present := body[field]; present != (field == c.field) {
					t.Errorf("%s present = %v, want only %s", field, present, c.field)
				}
			}
			if c.field == "reasoning" && !reflect.DeepEqual(body["reasoning"], map[string]any{"effort": "none"}) {
				t.Errorf("reasoning = %v", body["reasoning"])
			}
		})
	}
}

func TestResponsesSendsImagesAsDataURLsBeforeTheText(t *testing.T) {
	s := stream(t, "llamacpp/answer.sse")
	req := bench
	picture := provider.Message{Role: provider.RoleUser, Content: "what is this?", Images: []provider.Image{{MediaType: "image/png", Data: []byte{1, 2, 3}, Width: 1, Height: 1}}}
	req.Messages = []provider.Message{picture}
	read(t, newResponses(t, s.URL, false), req)
	content := s.sent(t)["input"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content = %v, want the image and the text", content)
	}
	image, text := content[0].(map[string]any), content[1].(map[string]any)
	if image["type"] != "input_image" || image["image_url"] != "data:image/png;base64,AQID" || text["type"] != "input_text" || text["text"] != "what is this?" {
		t.Errorf("content = %v", content)
	}
}

func TestResponsesChatBodyIsTheChatProvidersBody(t *testing.T) {
	// Counting renders this body; there is one serialiser of it.
	req := bench
	req.Messages = []provider.Message{
		provider.UserMessage("hi"),
		provider.AssistantMessageWithReasoning("", "thinking", []provider.ToolCall{{ID: "c1", Name: "ls", Arguments: `{"path":"/"}`}}),
		provider.ToolResultMessage("c1", "a b", false),
	}
	chat := newProvider(t, "http://model.invalid").(provider.ChatEncoder)
	responses := newResponses(t, "http://model.invalid", false).(provider.ChatEncoder)
	want, err := chat.ChatBody(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := responses.ChatBody(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document(t, got), document(t, want)) {
		t.Errorf("ChatBody differs between the protocols:\n got  %s\n want %s", got, want)
	}
}

func TestResponsesListsModelsLikeTheChatProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"m","object":"model","created":1,"owned_by":"x","max_model_len":4096}]}`)
	}))
	t.Cleanup(srv.Close)
	models, err := newResponses(t, srv.URL, false).(provider.Lister).Models(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "m" || models[0].ContextWindow != 4096 {
		t.Errorf("Models = %+v, %v", models, err)
	}
}

func TestResponsesRequiresAModelAndABaseURL(t *testing.T) {
	if _, err := openai.NewResponses(provider.Endpoint{}); err == nil {
		t.Error("NewResponses without a base URL returned no error")
	}
	if _, err := newResponses(t, "http://model.invalid", false).Stream(context.Background(), provider.Request{}); err == nil {
		t.Error("Stream without a model returned no error")
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

func TestResponsesReportsAnIncompleteResponseAsCutOff(t *testing.T) {
	s := stream(t, "openai/incomplete.sse")
	r := read(t, newResponses(t, s.URL, true), bench)
	if r.err != nil || r.stop != "length" || r.text != "A cache hit is when" {
		t.Errorf("stop %q, text %q, err %v; want length with the text so far", r.stop, r.text, r.err)
	}
	if reasoning, ok := r.usage.ReasoningTokens.Value(); !ok || reasoning != 0 {
		t.Errorf("reasoning tokens = %v, want a stated 0", r.usage.ReasoningTokens)
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

func TestResponsesAssemblesACallFromItsFragmentsWhenNoItemCompletes(t *testing.T) {
	stream := "data: " + `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"c1","name":"now","arguments":""}}` + "\n\n" +
		"data: " + `{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"c2","name":"ls","arguments":""}}` + "\n\n" +
		"data: " + `{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\":\"/\"}"}` + "\n\n" +
		"data: " + `{"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":4,"total_tokens":9}}}` + "\n\n"
	r := read(t, newResponses(t, newResponsesServer(t, http.StatusOK, "text/event-stream", []byte(stream)).URL, false), bench)
	want := []provider.ToolCall{{ID: "c1", Name: "now", Arguments: `{}`}, {ID: "c2", Name: "ls", Arguments: `{"path":"/"}`}}
	if !reflect.DeepEqual(r.calls, want) {
		t.Errorf("calls = %+v, want %+v (no arguments become an empty object)", r.calls, want)
	}
}

func TestResponsesTakesTheOutputOfAFinalResponseThatSentNoItems(t *testing.T) {
	stream := "data: " + `{"type":"response.completed","response":{"status":"completed","output":[{"id":"rs_1","type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"hm"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]},{"type":"function_call","call_id":"c1","name":"ls","arguments":"{}"}],"usage":{"input_tokens":5,"output_tokens":4,"total_tokens":9}}}` + "\n\n"
	r := read(t, newResponses(t, newResponsesServer(t, http.StatusOK, "text/event-stream", []byte(stream)).URL, false), bench)
	if r.text != "hello" || len(r.items) != 1 || len(r.calls) != 1 || r.stop != "tool_calls" {
		t.Errorf("text %q, %d items, %d calls, stop %q", r.text, len(r.items), len(r.calls), r.stop)
	}
}

func TestResponsesReadsAVLLMStream(t *testing.T) {
	// Unverified fixture, written from vLLM's source.
	s := stream(t, "vllm/tool_call.sse")
	r := read(t, newResponses(t, s.URL, false), bench)
	if r.err != nil || r.reasoning != "The user wants /tmp listed." || len(r.items) != 1 || len(r.calls) != 1 {
		t.Fatalf("reasoning %q, %d items, %d calls, err %v", r.reasoning, len(r.items), len(r.calls), r.err)
	}
	if string(r.calls[0].Arguments) != `{"path": "/tmp"}` {
		t.Errorf("arguments = %s, want the text vLLM sent", r.calls[0].Arguments)
	}
	if reasoning, ok := r.usage.ReasoningTokens.Value(); !ok || reasoning != 9 {
		t.Errorf("reasoning tokens = %v, want the 9 vLLM states", r.usage.ReasoningTokens)
	}
	if cached, ok := r.usage.CachedTokens.Value(); !ok || cached != 0 {
		t.Errorf("cached tokens = %v, want a stated 0", r.usage.CachedTokens)
	}
}

func TestResponsesReadsAHostedStreamWithEncryptedReasoning(t *testing.T) {
	// Unverified fixture, written from the OpenAI API reference.
	s := stream(t, "openai/tool_call_encrypted.sse")
	r := read(t, newResponses(t, s.URL, true), bench)
	if r.err != nil || len(r.items) != 1 || len(r.calls) != 1 || r.calls[0].ID != "call_Yq3wLh9sT2" {
		t.Fatalf("%d items, calls %+v, err %v", len(r.items), r.calls, r.err)
	}
	var item struct {
		Encrypted string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(r.items[0], &item); err != nil || !strings.HasPrefix(item.Encrypted, "gAAAAABpA4tS") || !strings.HasSuffix(item.Encrypted, "Oe6=") {
		t.Errorf("encrypted content = %q (%v)", item.Encrypted, err)
	}
	if reasoning, _ := r.usage.ReasoningTokens.Value(); reasoning != 64 {
		t.Errorf("reasoning tokens = %v, want 64", r.usage.ReasoningTokens)
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
		{"context_length_exceeded event", 200, "text/event-stream", func(t *testing.T) []byte { return fixture(t, "openai/context_length_exceeded.sse") }, false, true},
		{"llama.cpp overflow, recorded", 400, "application/json", func(t *testing.T) []byte { return fixture(t, "llamacpp/overflow.json") }, false, true},
		{"rate limit", 429, "application/json", func(*testing.T) []byte { return []byte(`{"error":{"message":"slow down","type":"rate_limit"}}`) }, true, false},
		{"bad request", 400, "application/json", func(*testing.T) []byte { return []byte(`{"error":{"message":"Cannot determine type of 'item'"}}`) }, false, false},
		{"server error", 500, "application/json", func(*testing.T) []byte { return []byte(`{"error":{"message":"boom"}}`) }, true, false},
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

func TestResponsesSendsTheKeyAndNotTheEnvironmentOne(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "from-the-environment")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(fixture(t, "llamacpp/answer.sse"))
	}))
	t.Cleanup(srv.Close)
	read(t, newResponses(t, srv.URL, false), bench)
	if got != "Bearer secret" {
		t.Errorf("Authorization = %q, want the configured key", got)
	}
}
