package counter_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/openai"
	"github.com/erlidev/eika/internal/provider/probe"
)

// recorder serves canned answers and keeps the bodies it received per path.
type recorder struct {
	*httptest.Server
	mu     sync.Mutex
	bodies map[string][]string
	auth   string
}

func newRecorder(t *testing.T, answer func(path string, body map[string]any) (any, int)) *recorder {
	t.Helper()
	r := &recorder{bodies: map[string][]string{}}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		r.mu.Lock()
		r.bodies[req.URL.Path] = append(r.bodies[req.URL.Path], string(raw))
		r.auth = req.Header.Get("Authorization")
		r.mu.Unlock()
		out, status := answer(req.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *recorder) sent(path string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.bodies[path])
}

func encoder(t *testing.T) provider.ChatEncoder {
	t.Helper()
	p, err := openai.New(provider.Endpoint{BaseURL: "http://unused.invalid/v1"})
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}
	return p.(provider.ChatEncoder)
}

func TestLlamaCppRendersTheRequestTheProviderSends(t *testing.T) {
	srv := newRecorder(t, func(path string, _ map[string]any) (any, int) {
		switch path {
		case "/apply-template":
			return map[string]any{"prompt": "<|im_start|>user\nhi<|im_end|>\n"}, http.StatusOK
		case "/tokenize":
			return map[string]any{"tokens": []int{1, 2, 3, 4}}, http.StatusOK
		}
		return nil, http.StatusNotFound
	})
	routes := counter.NewRoutes(counter.Config{
		BaseURL: srv.URL + "/v1", APIKey: "k", Backend: probe.LlamaCpp, Counting: probe.CountChat,
	}, encoder(t))
	if routes.Level() != counter.LevelChat {
		t.Fatalf("Level = %v, want chat", routes.Level())
	}

	effort := provider.EffortNone
	maxOut := 64
	req := provider.Request{
		Model: "qwen", System: "be brief", ThinkingSwitch: provider.SwitchTemplate, PreserveThinking: true,
		Sampling: provider.Sampling{ReasoningEffort: &effort, MaxOutput: &maxOut},
		Tools:    []provider.ToolDef{{Name: "ls", Description: "list", Schema: []byte(`{"type":"object"}`)}},
		Messages: []provider.Message{
			provider.UserMessage("hi"),
			provider.AssistantMessageWithReasoning("ok", "mulling it over", []provider.ToolCall{{ID: "c1", Name: "ls", Arguments: `{"p":1}`}}),
			provider.ToolResultMessage("c1", "a b", false),
		},
	}
	prompt, err := routes.Prompt(context.Background(), req)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.HasPrefix(prompt, "<|im_start|>user") {
		t.Errorf("prompt = %q", prompt)
	}

	// Whatever changes the rendered prompt is in the body /apply-template got:
	// the system prompt, the tools, the thinking switch, the replayed reasoning
	// and the tool call.
	sent := srv.sent("/apply-template")
	if len(sent) != 1 {
		t.Fatalf("/apply-template got %d requests, want 1", len(sent))
	}
	want, err := encoder(t).ChatBody(req)
	if err != nil {
		t.Fatalf("ChatBody: %v", err)
	}
	if sent[0] != string(want) {
		t.Errorf("/apply-template body is not the provider's chat body, byte for byte:\n got  %s\n want %s", sent[0], want)
	}
	for _, needle := range []string{`"be brief"`, `"ls"`, `"chat_template_kwargs"`, `"enable_thinking":false`, `"preserve_thinking":true`, `"reasoning_content":"mulling it over"`, `"tool_call_id":"c1"`} {
		if !strings.Contains(sent[0], needle) {
			t.Errorf("/apply-template body lacks %s: %s", needle, sent[0])
		}
	}
	if srv.auth != "Bearer k" {
		t.Errorf("Authorization = %q, want the key", srv.auth)
	}

	n, err := routes.Text(context.Background(), prompt)
	if err != nil || n != 4 {
		t.Errorf("Text = %d, %v; want 4", n, err)
	}
	if _, err := routes.Request(context.Background(), req); !errors.Is(err, counter.ErrUnsupported) {
		t.Errorf("Request on llama.cpp: %v, want ErrUnsupported", err)
	}
}

func TestLlamaCppBoundariesAreControlTokensOnly(t *testing.T) {
	srv := newRecorder(t, func(path string, body map[string]any) (any, int) {
		if path != "/tokenize" {
			return nil, http.StatusNotFound
		}
		content, _ := body["content"].(string)
		parsed, _ := body["parse_special"].(bool)
		switch {
		case content == "<|im_start|>" && parsed:
			return map[string]any{"tokens": []int{1}}, http.StatusOK
		case content == "<|im_start|>":
			return map[string]any{"tokens": []int{7, 8, 9, 10, 11, 12}}, http.StatusOK
		case content == "<|word|>": // a plain vocabulary entry: one token either way
			return map[string]any{"tokens": []int{5}}, http.StatusOK
		}
		return map[string]any{"tokens": []int{1, 2, 3}}, http.StatusOK
	})
	routes := counter.NewRoutes(counter.Config{BaseURL: srv.URL, Backend: probe.LlamaCpp, Counting: probe.CountChat}, nil)

	prompt := "<|im_start|>user\nhi <|word|> there<|im_end|>\n<|im_start|>assistant\n"
	cuts, err := routes.Boundaries(context.Background(), prompt)
	if err != nil {
		t.Fatalf("Boundaries: %v", err)
	}
	want := []int{0, strings.LastIndex(prompt, "<|im_start|>")}
	if !slices.Equal(cuts, want) {
		t.Errorf("cuts = %v, want %v: only <|im_start|> is a control token here", cuts, want)
	}
	// Each candidate is asked about once, however often it appears.
	before := len(srv.sent("/tokenize"))
	if _, err := routes.Boundaries(context.Background(), prompt); err != nil {
		t.Fatalf("Boundaries: %v", err)
	}
	if after := len(srv.sent("/tokenize")); after != before {
		t.Errorf("the second call made %d more requests, want 0", after-before)
	}
}

func TestVLLMCountsTheWholeChatRequest(t *testing.T) {
	srv := newRecorder(t, func(path string, _ map[string]any) (any, int) {
		if path == "/tokenize" {
			return map[string]any{"count": 42, "tokens": []int{1, 2}, "max_model_len": 4096}, http.StatusOK
		}
		return nil, http.StatusNotFound
	})
	routes := counter.NewRoutes(counter.Config{BaseURL: srv.URL + "/v1", Model: "m", Backend: probe.VLLM, Counting: probe.CountChat}, encoder(t))
	req := provider.Request{Model: "m", Tools: []provider.ToolDef{{Name: "ls", Schema: []byte(`{}`)}},
		Messages: []provider.Message{provider.UserMessage("hi")}}

	n, err := routes.Request(context.Background(), req)
	if err != nil || n != 42 {
		t.Fatalf("Request = %d, %v; want the server's count", n, err)
	}
	body := srv.sent("/tokenize")[0]
	for _, needle := range []string{`"messages"`, `"tools"`} {
		if !strings.Contains(body, needle) {
			t.Errorf("/tokenize body lacks %s: %s", needle, body)
		}
	}
	if _, err := routes.Prompt(context.Background(), req); !errors.Is(err, counter.ErrUnsupported) {
		t.Errorf("Prompt on vLLM: %v, want ErrUnsupported", err)
	}
	if n, err := routes.Text(context.Background(), "hello"); err != nil || n != 42 {
		t.Errorf("Text = %d, %v", n, err)
	}
	if got := srv.sent("/tokenize")[1]; !strings.Contains(got, `"prompt":"hello"`) || !strings.Contains(got, `"add_special_tokens":false`) {
		t.Errorf("text request = %s, want a prompt without special tokens", got)
	}
}

func TestTextOnlyBackendsAndLevels(t *testing.T) {
	srv := newRecorder(t, func(path string, _ map[string]any) (any, int) {
		switch path {
		case "/tokenize":
			return map[string]any{"tokens": []int{1}, "count": 7}, http.StatusOK
		case "/v1/token/encode":
			return map[string]any{"tokens": []int{1, 2}, "length": 9}, http.StatusOK
		}
		return nil, http.StatusNotFound
	})
	cases := []struct {
		name     string
		backend  probe.Backend
		counting probe.Counting
		level    counter.Level
		want     int
	}{
		{"sglang text", probe.SGLang, probe.CountText, counter.LevelText, 7},
		{"tabby chat is counted as text", probe.Tabby, probe.CountChat, counter.LevelText, 9},
		{"vllm text", probe.VLLM, probe.CountText, counter.LevelText, 7},
		{"no tokenizer", probe.OpenAI, probe.CountNone, counter.LevelNone, 0},
		{"llama.cpp without routes", probe.LlamaCpp, probe.CountNone, counter.LevelNone, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			routes := counter.NewRoutes(counter.Config{BaseURL: srv.URL, Backend: c.backend, Counting: c.counting}, nil)
			if routes.Level() != c.level {
				t.Fatalf("Level = %v, want %v", routes.Level(), c.level)
			}
			n, err := routes.Text(context.Background(), "hello")
			if c.level == counter.LevelNone {
				if !errors.Is(err, counter.ErrUnsupported) {
					t.Errorf("Text: %v, want ErrUnsupported", err)
				}
				return
			}
			if err != nil || n != c.want {
				t.Errorf("Text = %d, %v; want %d", n, err, c.want)
			}
		})
	}
}

func TestRoutesReportAFailedRoute(t *testing.T) {
	srv := newRecorder(t, func(string, map[string]any) (any, int) {
		return map[string]any{"error": "nope"}, http.StatusBadRequest
	})
	routes := counter.NewRoutes(counter.Config{BaseURL: srv.URL, Backend: probe.LlamaCpp, Counting: probe.CountChat}, encoder(t))
	if _, err := routes.Text(context.Background(), "hi"); err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Errorf("Text error = %v, want the status", err)
	}
}
