package openai_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
)

// bodyRecorder keeps the raw bodies a server received, in order. It answers a
// chat request with a one-word stream and a Responses request with a completed
// response, which is all a provider needs to end its call.
type bodyRecorder struct {
	*httptest.Server
	mu     sync.Mutex
	bodies [][]byte
}

func newBodyRecorder(t *testing.T) *bodyRecorder {
	t.Helper()
	r := &bodyRecorder{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, raw)
		r.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if req.URL.Path == "/responses" {
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
			return
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *bodyRecorder) sent() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.bodies...)
}

// everyField is a request that sets each field a provider writes from a map:
// the sampling extras, the thinking switch, the preserved reasoning and the
// slot. They are the fields an encoder is tempted to write in the order a map
// gives them.
func everyField() provider.Request {
	effort := provider.EffortNone
	topK, minP, topP, maxOut := 20, 0.05, 0.8, 512
	temperature, penalty := 0.7, 0.3
	seed := int64(7)
	slot := 1
	return provider.Request{
		Model: "qwen", System: "be brief", ThinkingSwitch: provider.SwitchTemplate, PreserveThinking: true,
		SlotHint: &slot,
		Sampling: provider.Sampling{
			ReasoningEffort: &effort, MaxOutput: &maxOut, TopK: &topK, MinP: &minP, TopP: &topP,
			Temperature: &temperature, FrequencyPenalty: &penalty, PresencePenalty: &penalty, Seed: &seed,
			Stop: []string{"STOP"},
		},
		Tools: []provider.ToolDef{{Name: "ls", Description: "list", Schema: []byte(`{"type":"object","properties":{"b":{"type":"string"},"a":{"type":"integer"}}}`)}},
		Messages: []provider.Message{
			provider.UserMessage("hi"),
			provider.AssistantMessageWithReasoning("ok", "mulling", []provider.ToolCall{{ID: "c1", Name: "ls", Arguments: `{"p": 1,"a":2}`}}),
			provider.ToolResultMessage("c1", "a b", false),
		},
	}
}

// The wire is what is sent, and the prompt cache reads it: a request sent twice
// is sent as the same bytes. The SDK's own encoder writes the fields it takes
// from a map in whatever order the map is walked, which differs from one
// encoding to the next, so the provider writes them itself.
func TestTheSameRequestIsSentAsTheSameBytesEveryTime(t *testing.T) {
	req := everyField()
	for _, c := range []struct {
		name string
		make func(provider.Endpoint) (provider.Provider, error)
	}{
		{"chat", openai.New},
		{"responses", openai.NewResponses},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := newBodyRecorder(t)
			p, err := c.make(provider.Endpoint{BaseURL: srv.URL, APIKey: "k", SlotHints: true})
			if err != nil {
				t.Fatal(err)
			}
			const sends = 60
			for range sends {
				collect(t, p, req)
			}
			bodies := srv.sent()
			if len(bodies) != sends {
				t.Fatalf("the server received %d requests, want %d", len(bodies), sends)
			}
			for i, body := range bodies[1:] {
				if string(body) != string(bodies[0]) {
					t.Fatalf("request %d differs from request 1:\n%s\n%s", i+2, body, bodies[0])
				}
			}
		})
	}
}

func TestTheChatBodyIsTheSameBytesEveryTime(t *testing.T) {
	encoder := newProvider(t, "http://model.invalid").(provider.ChatEncoder)
	req := everyField()
	first, err := encoder.ChatBody(req)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		again, err := encoder.ChatBody(req)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("encoding %d differs from the first:\n%s\n%s", i+2, again, first)
		}
	}
}
