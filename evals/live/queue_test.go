package live_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/evals/live"
)

// countingServer is a chat endpoint whose tokenizer counts words, so that a
// prompt's size is whatever the scenario builds, and whose model answers
// "ok" after a pause. It records the order the chat requests arrive in and
// how many were in flight at once.
type countingServer struct {
	*httptest.Server
	pause time.Duration

	mu       sync.Mutex
	arrived  []string
	inFlight int
	atOnce   int
}

func newCountingServer(t *testing.T, pause time.Duration) *countingServer {
	t.Helper()
	bench := benchServer(t)
	ws := &countingServer{pause: pause}
	render := func(body map[string]any) string {
		var parts []string
		msgs, _ := body["messages"].([]any)
		for _, m := range msgs {
			msg, _ := m.(map[string]any)
			if s, ok := msg["content"].(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	}
	ws.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]string{"prompt": render(body)})
		case "/tokenize":
			var asked struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&asked)
			_, _ = fmt.Fprintf(w, `{"tokens":[%s]}`, strings.TrimSuffix(strings.Repeat("7,", len(strings.Fields(asked.Content))), ","))
		case "/v1/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			ws.mu.Lock()
			ws.arrived = append(ws.arrived, render(body))
			ws.inFlight++
			ws.atOnce = max(ws.atOnce, ws.inFlight)
			ws.mu.Unlock()
			time.Sleep(ws.pause)
			ws.mu.Lock()
			ws.inFlight--
			ws.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, sse(reply{prompt: len(strings.Fields(render(body))), completion: 1, text: "ok"}))
		default:
			bench.Config.Handler.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(ws.Close)
	return ws
}

func TestQueuePriorityLetsTheInteractiveCallOvertake(t *testing.T) {
	ws := newCountingServer(t, 20*time.Millisecond)
	res := live.QueuePriority("fixed").Execute(context.Background(), connect(t, ws.URL))
	if !res.Passed {
		t.Fatalf("failed: %s", res.Error)
	}
	if len(res.Calls) != 3 || res.Calls[1].Label != "interactive" || res.Calls[2].Label != "background" {
		t.Fatalf("calls = %+v, want holder, interactive, background", res.Calls)
	}
	if ws.atOnce != 1 {
		t.Errorf("%d requests were in flight at once on a one-slot pool", ws.atOnce)
	}
	// The server saw the holder first, then the interactive call, then the
	// background one, although the background call asked first.
	var order []string
	for _, a := range ws.arrived {
		_, said, _ := strings.Cut(a, "Say ")
		order = append(order, said)
	}
	if strings.Join(order, " ") != "holder. interactive. background." {
		t.Errorf("requests arrived as %q", order)
	}
	if res.Calls[1].QueueSeconds == nil || res.Calls[2].QueueSeconds == nil || *res.Calls[1].QueueSeconds >= *res.Calls[2].QueueSeconds {
		t.Errorf("queue waits = %v and %v, want the interactive call to have waited less", res.Calls[1].QueueSeconds, res.Calls[2].QueueSeconds)
	}
}

func TestSharedAdmissionRunsTwoLargeSessionsOneAfterTheOther(t *testing.T) {
	ws := newCountingServer(t, 30*time.Millisecond)
	res := live.SharedAdmission("fixed").Execute(context.Background(), connect(t, ws.URL))
	if !res.Passed {
		t.Fatalf("failed: %s", res.Error)
	}
	if ws.atOnce != 1 {
		t.Errorf("%d requests were in flight at once; the two prompts pass the cache and must be taken in turn", ws.atOnce)
	}
	if len(res.Calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(res.Calls))
	}
	for _, c := range res.Calls {
		if c.PromptTokens == nil || *c.PromptTokens < 60_000 || *c.PromptTokens > 70_000 {
			t.Errorf("%s has a prompt of %v tokens, want about 66000", c.Label, c.PromptTokens)
		}
	}
	waited := 0.0
	for _, c := range res.Calls {
		waited = max(waited, *c.QueueSeconds)
	}
	if waited < 0.02 {
		t.Errorf("longest queue wait = %.3f s, want the second session to have waited for the first", waited)
	}
}
