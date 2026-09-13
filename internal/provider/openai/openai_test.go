package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/config"
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
	t.Setenv("EIKA_TEST_KEY", "secret")
	p, err := openai.New(config.Model{
		Name:          "test-model",
		BaseURL:       baseURL,
		APIKeyEnv:     "EIKA_TEST_KEY",
		ContextWindow: 1000,
		MaxOutput:     100,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// collect drains a provider stream.
func collect(t *testing.T, ctx context.Context, p provider.Provider, req provider.Request) []provider.Event {
	t.Helper()
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

func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv("EIKA_TEST_MISSING", "")
	_, err := openai.New(config.Model{Name: "m", BaseURL: "http://x", APIKeyEnv: "EIKA_TEST_MISSING"})
	if err == nil {
		t.Fatal("New with an empty key returned no error")
	}
	if !strings.Contains(err.Error(), "EIKA_TEST_MISSING") {
		t.Errorf("error = %v, want it to name the variable", err)
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
	temp := 0.0
	collect(t, context.Background(), p, provider.Request{
		System:      "sys",
		Temperature: &temp,
		Messages: []provider.Message{
			provider.UserMessage("hi"),
			provider.AssistantMessage("", []provider.ToolCall{{ID: "c1", Name: "ls", Arguments: provider.ToolArguments(`{}`)}}),
			provider.ToolResultMessage("c1", "a.txt", false),
		},
		Tools: []provider.ToolDef{{Name: "ls", Description: "list", Schema: json.RawMessage(`{"type":"object"}`)}},
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
		t.Errorf("max_completion_tokens = %d, want the model default 100", sent.MaxCompletionTokens)
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
		ReasoningEffort:  "high",
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
