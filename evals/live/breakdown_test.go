package live_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/live"
)

// wordServer is a bench whose tokenizer counts words and whose template wraps
// every message, and the tool list, in a marker word of its own: a server
// whose breakdown is known without a model. extra adds that many tokens to the
// prompt the chat route reports, as a template that charges more than its
// markers would (or, negative, a server that counts less than its tokenizer).
func wordServer(t *testing.T, extra int) *httptest.Server {
	t.Helper()
	bench := benchServer(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			var body struct {
				Messages []map[string]any `json:"messages"`
				Tools    json.RawMessage  `json:"tools"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			prompt := render(body.Messages, body.Tools)
			_ = json.NewEncoder(w).Encode(map[string]string{"prompt": prompt})
		case "/tokenize":
			var asked struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&asked)
			_, _ = fmt.Fprintf(w, `{"tokens":[%s]}`, tokenList(len(strings.Fields(asked.Content))))
		case "/v1/chat/completions":
			var body struct {
				Messages []map[string]any `json:"messages"`
				Tools    json.RawMessage  `json:"tools"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			prompt := len(strings.Fields(render(body.Messages, body.Tools))) + extra
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n"+
				"data: {\"choices\":[],\"usage\":{\"completion_tokens\":1,\"prompt_tokens\":%d,\"total_tokens\":%d}}\n\ndata: [DONE]\n\n", prompt, prompt+1)
		default:
			bench.Config.Handler.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func tokenList(n int) string {
	return strings.TrimSuffix(strings.Repeat("7,", n), ",")
}

// render is the word server's template: a marker and the role before each
// message, a marker before the tools, and a marker for the reply.
func render(messages []map[string]any, tools json.RawMessage) string {
	var b strings.Builder
	if len(tools) > 0 && string(tools) != "null" {
		b.WriteString("<tools> " + string(tools) + " ")
	}
	for _, m := range messages {
		fmt.Fprintf(&b, "<msg> %v ", m["role"])
		for _, key := range []string{"content", "reasoning_content"} {
			if s, ok := m[key].(string); ok {
				b.WriteString(s + " ")
			}
		}
		if calls, ok := m["tool_calls"].([]any); ok {
			for _, c := range calls {
				fn, _ := c.(map[string]any)["function"].(map[string]any)
				fmt.Fprintf(&b, "%v %v ", fn["name"], fn["arguments"])
			}
		}
	}
	b.WriteString("<reply>")
	return b.String()
}

func TestContextBreakdownPartsAndRemainderAddUpToThePromptTheServerCounted(t *testing.T) {
	srv := wordServer(t, 0)
	res := live.ContextBreakdown("fixed").Execute(context.Background(), connect(t, srv.URL))
	if !res.Passed {
		t.Fatalf("scenario failed: %s", res.Error)
	}
	if len(res.Calls) != 1 || res.Calls[0].PromptTokens == nil {
		t.Fatalf("calls = %+v, want the one call with its prompt count", res.Calls)
	}
}

func TestContextBreakdownFailsWhenTheServerCountsLessThanTheParts(t *testing.T) {
	// Fewer tokens in the prompt than the parts add up to: a part counted
	// twice, which a scaled breakdown would have hidden.
	srv := wordServer(t, -40)
	res := live.ContextBreakdown("fixed").Execute(context.Background(), connect(t, srv.URL))
	if res.Passed || !strings.Contains(res.Error, "remainder is -") {
		t.Fatalf("passed = %v, error = %q, want a failure naming the negative remainder", res.Passed, res.Error)
	}
}

func TestContextBreakdownFailsWhenTheServerCountsMuchMoreThanTheParts(t *testing.T) {
	// Tokens the parts do not explain: a part left out.
	srv := wordServer(t, 500)
	res := live.ContextBreakdown("fixed").Execute(context.Background(), connect(t, srv.URL))
	if res.Passed || !strings.Contains(res.Error, "leave out part of the") {
		t.Fatalf("passed = %v, error = %q, want a failure naming the unexplained tokens", res.Passed, res.Error)
	}
}
