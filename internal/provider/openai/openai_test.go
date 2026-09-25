package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
)

// sseServer serves one canned Chat Completions stream and records the request
// body it received.
type sseServer struct {
	*httptest.Server
	body chan []byte
}

// newSSEServer returns a server that replies with the given SSE data lines.
func newSSEServer(t *testing.T, status int, lines ...string) *sseServer {
	t.Helper()
	s := &sseServer{body: make(chan []byte, 1)}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case s.body <- body:
		default:
		}
		if status != http.StatusOK {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":{"message":"nope","type":"rate_limit"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range lines {
			_, _ = io.WriteString(w, "data: "+line+"\n\n")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(s.Close)
	return s
}

// newProvider builds a provider pointed at the test server.
func newProvider(t *testing.T, baseURL string) provider.Provider {
	t.Helper()
	p, err := openai.New(provider.Endpoint{BaseURL: baseURL, APIKey: "secret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// collect drains a provider stream. A request that names no model gets the
// one every test server stands in for.
func collect(t *testing.T, ctx context.Context, p provider.Provider, req provider.Request) []provider.Event {
	t.Helper()
	if req.Model == "" {
		req.Model = "test-model"
	}
	ch, err := p.Stream(ctx, req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var events []provider.Event
	for e := range ch {
		events = append(events, e)
	}
	return events
}

func TestNewRequiresABaseURL(t *testing.T) {
	if _, err := openai.New(provider.Endpoint{APIKey: "key"}); err == nil {
		t.Fatal("New without a base URL returned no error")
	}
}

func TestStreamRequiresAModel(t *testing.T) {
	p := newProvider(t, "http://model.invalid")
	if _, err := p.Stream(context.Background(), provider.Request{}); err == nil {
		t.Fatal("Stream without a model returned no error")
	}
}

func TestStreamSendsTheKeyAndNotTheEnvironmentOne(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "from-the-environment")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	for _, key := range []string{"from-the-ui", ""} {
		p, err := openai.New(provider.Endpoint{BaseURL: srv.URL, APIKey: key})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		collect(t, context.Background(), p, provider.Request{Messages: []provider.Message{provider.UserMessage("hi")}})
		if strings.Contains(got, "from-the-environment") {
			t.Errorf("key %q: Authorization = %q, want the configured key", key, got)
		}
		if key != "" && got != "Bearer "+key {
			t.Errorf("Authorization = %q, want Bearer %s", got, key)
		}
	}
}

func TestModelsListsWhatTheEndpointServes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[
			{"id":"zeta","object":"model","created":1,"owned_by":"x"},
			{"id":"router/alpha","object":"model","created":1,"owned_by":"x","context_length":200000,"top_provider":{"max_completion_tokens":64000}},
			{"id":"served","object":"model","created":1,"owned_by":"x","max_model_len":32768}
		]}`)
	}))
	t.Cleanup(srv.Close)

	p, err := openai.New(provider.Endpoint{BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	models, err := p.(provider.Lister).Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	want := []provider.ModelInfo{
		{ID: "router/alpha", ContextWindow: 200000, MaxOutput: 64000},
		{ID: "served", ContextWindow: 32768},
		{ID: "zeta"},
	}
	if len(models) != len(want) {
		t.Fatalf("models = %+v, want %+v", models, want)
	}
	for i := range want {
		if models[i] != want[i] {
			t.Errorf("models[%d] = %+v, want %+v", i, models[i], want[i])
		}
	}
}

func TestModelsReportsARefusedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`)
	}))
	t.Cleanup(srv.Close)
	p, err := openai.New(provider.Endpoint{BaseURL: srv.URL, APIKey: "wrong"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.(provider.Lister).Models(context.Background())
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.StatusCode != http.StatusUnauthorized || perr.Retryable {
		t.Errorf("Models error = %v, want a non-retryable 401", err)
	}
}

func TestStreamText(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hel"}}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
	)
	p := newProvider(t, s.URL)
	events := collect(t, context.Background(), p, provider.Request{
		System:   "be brief",
		Messages: []provider.Message{provider.UserMessage("hi")},
	})

	var text strings.Builder
	var usage provider.Usage
	var last provider.Event
	for _, e := range events {
		switch e.Kind {
		case provider.KindTextDelta:
			text.WriteString(e.Text)
		case provider.KindUsage:
			usage = e.Usage
		}
		last = e
	}
	if text.String() != "Hello" {
		t.Errorf("text = %q, want %q", text.String(), "Hello")
	}
	if usage != (provider.Usage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7}) {
		t.Errorf("usage = %+v", usage)
	}
	if last.Kind != provider.KindDone || last.StopReason != "stop" {
		t.Errorf("last event = %+v, want done/stop", last)
	}
}

func TestStreamRejectsMissingFinishReason(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial"}}]}`,
	)
	p := newProvider(t, s.URL)
	events := collect(t, context.Background(), p, provider.Request{
		Messages: []provider.Message{provider.UserMessage("hi")},
	})
	last := events[len(events)-1]
	if last.Kind != provider.KindError || last.Err == nil || !strings.Contains(last.Err.Error(), "finish_reason") {
		t.Fatalf("last event = %+v, want a missing finish_reason error", last)
	}
	for _, e := range events {
		if e.Kind == provider.KindDone {
			t.Errorf("events contain done after a truncated stream: %+v", events)
			break
		}
	}
}

func TestStreamAssemblesToolCall(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"pa"}}]}}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.txt\"}"}}]},"finish_reason":"tool_calls"}]}`,
	)
	p := newProvider(t, s.URL)
	events := collect(t, context.Background(), p, provider.Request{
		Messages: []provider.Message{provider.UserMessage("read a.txt")},
		Tools: []provider.ToolDef{{
			Name:        "read",
			Description: "read a file",
			Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
	})

	var deltas int
	var call provider.ToolCall
	for _, e := range events {
		switch e.Kind {
		case provider.KindToolCallDelta:
			deltas++
		case provider.KindToolCall:
			call = e.ToolCall
		}
	}
	if deltas != 2 {
		t.Errorf("tool call deltas = %d, want 2", deltas)
	}
	if call.ID != "call_1" || call.Name != "read" || string(call.Arguments) != `{"path":"a.txt"}` {
		t.Errorf("tool call = %+v", call)
	}
}

func TestStreamDropsToolCallsCutOffByTheTokenLimit(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"pa"}}]}}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`,
	)
	p := newProvider(t, s.URL)
	events := collect(t, context.Background(), p, provider.Request{
		Messages: []provider.Message{provider.UserMessage("read a.txt")},
	})

	for _, e := range events {
		if e.Kind == provider.KindToolCall {
			t.Errorf("a truncated tool call was assembled: %+v", e.ToolCall)
		}
	}
	last := events[len(events)-1]
	if last.Kind != provider.KindDone || last.StopReason != "length" {
		t.Errorf("last event = %+v, want done/length", last)
	}
}

func TestStreamSendsToolsAndModel(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
	)
	p := newProvider(t, s.URL)
	collect(t, context.Background(), p, provider.Request{
		System: "sys",
		Messages: []provider.Message{
			provider.UserMessage("hi"),
			provider.AssistantMessage("", []provider.ToolCall{{ID: "c1", Name: "ls", Arguments: provider.ToolArguments(`{}`)}}),
			provider.ToolResultMessage("c1", "a.txt", false),
		},
		Tools:    []provider.ToolDef{{Name: "ls", Description: "list", Schema: json.RawMessage(`{"type":"object"}`)}},
		Sampling: provider.Sampling{Temperature: ptr(0.0), MaxOutput: ptr(100)},
	})

	var sent struct {
		Model    string `json:"model"`
		Messages []struct {
			Role      string `json:"role"`
			Content   any    `json:"content"`
			ToolCalls []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		MaxCompletionTokens int      `json:"max_completion_tokens"`
		Temperature         *float64 `json:"temperature"`
	}
	if err := json.Unmarshal(<-s.body, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Model != "test-model" {
		t.Errorf("model = %q", sent.Model)
	}
	if len(sent.Messages) != 4 || sent.Messages[0].Role != "system" || sent.Messages[3].Role != "tool" {
		t.Fatalf("messages = %+v", sent.Messages)
	}
	if len(sent.Messages[2].ToolCalls) != 1 || sent.Messages[2].ToolCalls[0].ID != "c1" {
		t.Errorf("assistant tool calls = %+v", sent.Messages[2])
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Function.Name != "ls" {
		t.Errorf("tools = %+v", sent.Tools)
	}
	if sent.MaxCompletionTokens != 100 {
		t.Errorf("max_completion_tokens = %d, want the requested 100", sent.MaxCompletionTokens)
	}
	if sent.Temperature == nil || *sent.Temperature != 0 {
		t.Errorf("temperature = %v, want an explicit 0", sent.Temperature)
	}
}

func TestStreamReplaysExactToolArgumentText(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
	)
	p := newProvider(t, s.URL)
	collect(t, context.Background(), p, provider.Request{
		Messages: []provider.Message{
			provider.UserMessage("use both"),
			provider.AssistantMessage("", []provider.ToolCall{
				{ID: "valid", Name: "accept_string", Arguments: provider.ToolArguments(`"value"`)},
				{ID: "malformed", Name: "read", Arguments: provider.ToolArguments(`{"path":`)},
			}),
			provider.ToolResultMessage("valid", "done", false),
			provider.ToolResultMessage("malformed", "invalid arguments", true),
		},
	})

	var sent struct {
		Messages []struct {
			ToolCalls []struct {
				Function struct {
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(<-s.body, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	calls := sent.Messages[1].ToolCalls
	if len(calls) != 2 {
		t.Fatalf("tool calls = %+v, want two", calls)
	}
	if calls[0].Function.Arguments != `"value"` {
		t.Errorf("valid arguments = %q, want exact JSON string", calls[0].Function.Arguments)
	}
	if calls[1].Function.Arguments != `{"path":` {
		t.Errorf("malformed arguments = %q, want exact model text", calls[1].Function.Arguments)
	}
}

func TestStreamPreservesCompatibleChatCompletionsReasoning(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"think "}}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"carefully","content":"done"},"finish_reason":"stop"}]}`,
	)
	p := newProvider(t, s.URL)
	events := collect(t, context.Background(), p, provider.Request{
		Sampling:         provider.Sampling{ReasoningEffort: ptr("high")},
		PreserveThinking: true,
		Messages: []provider.Message{
			provider.UserMessage("first"),
			provider.AssistantMessageWithReasoning("answer", "prior thought", nil),
			provider.UserMessage("continue"),
		},
	})

	var reasoning, content strings.Builder
	for _, e := range events {
		switch e.Kind {
		case provider.KindReasoningDelta:
			reasoning.WriteString(e.ReasoningDelta)
		case provider.KindTextDelta:
			content.WriteString(e.Text)
		}
	}
	if reasoning.String() != "think carefully" {
		t.Errorf("reasoning = %q, want preserved reasoning", reasoning.String())
	}
	if content.String() != "done" {
		t.Errorf("content = %q, want done", content.String())
	}

	var sent struct {
		ReasoningEffort  string `json:"reasoning_effort"`
		PreserveThinking bool   `json:"preserve_thinking"`
		Messages         []struct {
			Role             string `json:"role"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(<-s.body, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.ReasoningEffort != "high" || !sent.PreserveThinking {
		t.Errorf("reasoning settings = %q, %v; want high, true", sent.ReasoningEffort, sent.PreserveThinking)
	}
	if len(sent.Messages) != 3 || sent.Messages[1].Role != "assistant" || sent.Messages[1].ReasoningContent != "prior thought" {
		t.Errorf("messages = %+v, want replayed assistant reasoning", sent.Messages)
	}
}

func TestStreamSendsNoneInTheFieldTheThinkingSwitchNames(t *testing.T) {
	// Each switch puts "none" in exactly one field: an endpoint may reject a
	// field it does not know, so the others must stay out of the request.
	cases := []struct {
		name   string
		effort string
		sw     provider.ThinkingSwitch
		want   string
	}{
		{"default switch", "none", "", `{"reasoning_effort":"none"}`},
		{"reasoning_effort", "none", provider.SwitchReasoningEffort, `{"reasoning_effort":"none"}`},
		{"chat_template_kwargs", "none", provider.SwitchTemplate,
			`{"chat_template_kwargs":{"enable_thinking":false,"thinking":false}}`},
		{"thinking", "none", provider.SwitchThinking, `{"thinking":{"type":"disabled"}}`},
		{"other efforts ignore the switch", "high", provider.SwitchThinking, `{"reasoning_effort":"high"}`},
		{"endpoint default", "", provider.SwitchTemplate, `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSSEServer(t, http.StatusOK,
				`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`,
			)
			p := newProvider(t, s.URL)
			collect(t, context.Background(), p, provider.Request{
				Sampling:       provider.Sampling{ReasoningEffort: ptr(c.effort)},
				ThinkingSwitch: c.sw,
				Messages:       []provider.Message{provider.UserMessage("hi")},
			})

			var sent map[string]json.RawMessage
			if err := json.Unmarshal(<-s.body, &sent); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			got := map[string]json.RawMessage{}
			for _, field := range []string{"reasoning_effort", "chat_template_kwargs", "thinking"} {
				if v, ok := sent[field]; ok {
					got[field] = v
				}
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("encode fields: %v", err)
			}
			if string(encoded) != c.want {
				t.Errorf("thinking fields = %s, want %s", encoded, c.want)
			}
		})
	}
}

func TestStreamReadsRetryAfterAsADate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", time.Now().Add(30*time.Second).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down"}}`)
	}))
	t.Cleanup(server.Close)

	p := newProvider(t, server.URL)
	events := collect(t, context.Background(), p, provider.Request{
		Messages: []provider.Message{provider.UserMessage("hi")},
	})
	if len(events) != 1 || events[0].Kind != provider.KindError {
		t.Fatalf("events = %+v, want one error event", events)
	}
	after := provider.RetryAfter(events[0].Err)
	if after <= 0 || after > 31*time.Second {
		t.Errorf("RetryAfter = %v, want roughly 30s", after)
	}
}

func TestStreamClassifiesErrors(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		wantRetryable bool
	}{
		{"rate limited", http.StatusTooManyRequests, true},
		{"server error", http.StatusInternalServerError, true},
		{"bad request", http.StatusBadRequest, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSSEServer(t, c.status)
			p := newProvider(t, s.URL)
			events := collect(t, context.Background(), p, provider.Request{
				Messages: []provider.Message{provider.UserMessage("hi")},
			})
			if len(events) != 1 || events[0].Kind != provider.KindError {
				t.Fatalf("events = %+v, want one error event", events)
			}
			err := events[0].Err
			if provider.Retryable(err) != c.wantRetryable {
				t.Errorf("Retryable(%v) = %v, want %v", err, provider.Retryable(err), c.wantRetryable)
			}
			if c.wantRetryable && provider.RetryAfter(err) == 0 {
				t.Errorf("RetryAfter(%v) = 0, want the Retry-After header", err)
			}
		})
	}
}

func TestStreamRelaysReasoningWithoutPreserveThinking(t *testing.T) {
	// Showing the model think is the client's business; replaying it to the
	// endpoint is preserve_thinking's, so reasoning arrives either way.
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"weighing it"}}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`,
	)
	p := newProvider(t, s.URL)
	events := collect(t, context.Background(), p, provider.Request{
		Messages: []provider.Message{
			provider.UserMessage("first"),
			provider.AssistantMessageWithReasoning("answer", "prior thought", nil),
			provider.UserMessage("continue"),
		},
	})

	var reasoning strings.Builder
	for _, e := range events {
		if e.Kind == provider.KindReasoningDelta {
			reasoning.WriteString(e.ReasoningDelta)
		}
	}
	if reasoning.String() != "weighing it" {
		t.Errorf("reasoning = %q, want the streamed reasoning", reasoning.String())
	}

	var sent struct {
		PreserveThinking bool `json:"preserve_thinking"`
		Messages         []struct {
			ReasoningContent string `json:"reasoning_content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(<-s.body, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.PreserveThinking {
		t.Error("preserve_thinking was sent although the request did not ask for it")
	}
	if len(sent.Messages) != 3 || sent.Messages[1].ReasoningContent != "" {
		t.Errorf("messages = %+v, want no replayed reasoning", sent.Messages)
	}
}

func TestStreamRelaysUsageAsSoonAsAChunkReportsIt(t *testing.T) {
	// An endpoint with continuous usage statistics reports on every chunk;
	// each distinct report is relayed, so a caller can measure a decode rate
	// while the response is still arriving.
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"one "}}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"two"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`,
	)
	p := newProvider(t, s.URL)
	events := collect(t, context.Background(), p, provider.Request{
		Messages: []provider.Message{provider.UserMessage("hi")},
	})

	var reported []int
	for _, e := range events {
		if e.Kind == provider.KindUsage {
			reported = append(reported, e.Usage.OutputTokens)
		}
	}
	if len(reported) != 3 || reported[0] != 1 || reported[2] != 3 {
		t.Errorf("usage events = %v, want one per distinct report", reported)
	}
}

func TestStreamReportsOneUsageEventWhenTheEndpointReportsOnce(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`,
	)
	p := newProvider(t, s.URL)
	events := collect(t, context.Background(), p, provider.Request{
		Messages: []provider.Message{provider.UserMessage("hi")},
	})

	usage := 0
	for _, e := range events {
		if e.Kind == provider.KindUsage {
			usage++
		}
	}
	if usage != 1 {
		t.Errorf("usage events = %d, want exactly one", usage)
	}
}

// ptr returns a pointer to v, for the optional fields of a request.
func ptr[T any](v T) *T { return &v }

func TestStreamSendsEverySamplingParameterThatIsSet(t *testing.T) {
	// A field that is not set stays out of the body: an endpoint may refuse
	// a field it does not know, and the endpoint's default is what nil means.
	fields := []string{
		"temperature", "top_p", "top_k", "min_p", "frequency_penalty", "presence_penalty",
		"seed", "stop", "max_completion_tokens", "max_tokens", "reasoning_effort",
	}
	cases := []struct {
		name string
		req  provider.Request
		want string
	}{
		{"nothing set", provider.Request{}, `{}`},
		{
			"every field set",
			provider.Request{Sampling: provider.Sampling{
				Temperature:      ptr(0.7),
				TopP:             ptr(0.9),
				TopK:             ptr(40),
				MinP:             ptr(0.05),
				FrequencyPenalty: ptr(0.5),
				PresencePenalty:  ptr(-0.5),
				Seed:             ptr(int64(7)),
				Stop:             []string{"END", "\n\n"},
				MaxOutput:        ptr(256),
				ReasoningEffort:  ptr("high"),
			}},
			`{"frequency_penalty":0.5,"max_completion_tokens":256,"min_p":0.05,"presence_penalty":-0.5,` +
				`"reasoning_effort":"high","seed":7,"stop":["END","\n\n"],"temperature":0.7,"top_k":40,"top_p":0.9}`,
		},
		{
			"zero values are values",
			provider.Request{Sampling: provider.Sampling{
				Temperature:      ptr(0.0),
				TopP:             ptr(0.0),
				MinP:             ptr(0.0),
				FrequencyPenalty: ptr(0.0),
				PresencePenalty:  ptr(0.0),
				Seed:             ptr(int64(0)),
			}},
			`{"frequency_penalty":0,"min_p":0,"presence_penalty":0,"seed":0,"temperature":0,"top_p":0}`,
		},
		{"an empty stop list sends none", provider.Request{Sampling: provider.Sampling{Stop: []string{}}}, `{}`},
		{"an empty effort is the endpoint's", provider.Request{Sampling: provider.Sampling{ReasoningEffort: ptr("")}}, `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSSEServer(t, http.StatusOK,
				`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`,
			)
			p := newProvider(t, s.URL)
			req := c.req
			req.Messages = []provider.Message{provider.UserMessage("hi")}
			collect(t, context.Background(), p, req)

			var sent map[string]json.RawMessage
			if err := json.Unmarshal(<-s.body, &sent); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			got := map[string]json.RawMessage{}
			for _, field := range fields {
				if v, ok := sent[field]; ok {
					got[field] = v
				}
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("encode fields: %v", err)
			}
			if string(encoded) != c.want {
				t.Errorf("sampling fields = %s, want %s", encoded, c.want)
			}
		})
	}
}
