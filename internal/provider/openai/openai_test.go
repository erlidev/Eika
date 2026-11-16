package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
func collect(t *testing.T, p provider.Provider, req provider.Request) []provider.Event {
	t.Helper()
	if req.Model == "" {
		req.Model = "test-model"
	}
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var events []provider.Event
	for e := range ch {
		events = append(events, e)
	}
	return events
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
		collect(t, p, provider.Request{Messages: []provider.Message{provider.UserMessage("hi")}})
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
			{"id":"router/alpha","object":"model","created":1,"owned_by":"x","context_length":200000,"top_provider":{"max_completion_tokens":64000},"architecture":{"input_modalities":["text","image"]}},
			{"id":"router/beta","object":"model","created":1,"owned_by":"x","architecture":{"modality":"text+image->text"}},
			{"id":"router/gamma","object":"model","created":1,"owned_by":"x","architecture":{"input_modalities":["text"],"modality":"text->text+image"}},
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
		{ID: "router/alpha", ContextWindow: 200000, MaxOutput: 64000, ImageInput: true},
		{ID: "router/beta", ImageInput: true},
		{ID: "router/gamma"},
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

func TestStreamText(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hel"}}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
	)
	p := newProvider(t, s.URL)
	events := collect(t, p, provider.Request{
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
	if want := (provider.Usage{InputTokens: provider.Reported(5), OutputTokens: provider.Reported(2), TotalTokens: provider.Reported(7)}); usage != want {
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
	events := collect(t, p, provider.Request{
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
	events := collect(t, p, provider.Request{
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
	events := collect(t, p, provider.Request{
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

func TestStreamSendsNoneInTheFieldTheThinkingSwitchNames(t *testing.T) {
	// Each switch puts "none" in exactly one field: an endpoint may reject a
	// field it does not know, so the others must stay out of the request.
	cases := []struct {
		name   string
		effort string
		sw     provider.ThinkingSwitch
		want   string
	}{
		{"reasoning_effort", "none", provider.SwitchReasoningEffort, `{"reasoning_effort":"none"}`},
		{"chat_template_kwargs", "none", provider.SwitchTemplate,
			`{"chat_template_kwargs":{"enable_thinking":false,"thinking":false}}`},
		{"other efforts ignore the switch", "high", provider.SwitchThinking, `{"reasoning_effort":"high"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSSEServer(t, http.StatusOK,
				`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`,
			)
			p := newProvider(t, s.URL)
			collect(t, p, provider.Request{
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
			events := collect(t, p, provider.Request{
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
		{
			"zero values are values, and the rest are named as the endpoint knows them",
			provider.Request{Sampling: provider.Sampling{
				Temperature:      ptr(0.0),
				TopP:             ptr(0.0),
				MinP:             ptr(0.0),
				FrequencyPenalty: ptr(0.0),
				PresencePenalty:  ptr(0.0),
				Seed:             ptr(int64(0)),
				TopK:             ptr(40),
				Stop:             []string{"END"},
				MaxOutput:        ptr(256),
			}},
			`{"frequency_penalty":0,"max_completion_tokens":256,"min_p":0,"presence_penalty":0,"seed":0,"stop":["END"],"temperature":0,"top_k":40,"top_p":0}`,
		},
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
			collect(t, p, req)

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
