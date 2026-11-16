package openai_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
)

// slotServer stands in for a llama.cpp server on both of its routes and keeps
// the body of every request. With refuse set it answers any request that names
// id_slot the way a server that does not know the field would, with a 400.
type slotServer struct {
	*httptest.Server
	refuse string

	mu     sync.Mutex
	bodies [][]byte
}

func newSlotServer(t *testing.T, refuse string) *slotServer {
	t.Helper()
	s := &slotServer{refuse: refuse}
	answer := fixture(t, "llamacpp/answer.sse")
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, raw)
		s.mu.Unlock()
		if s.refuse != "" && strings.Contains(string(raw), "id_slot") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":`+s.refuse+`,"type":"invalid_request_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.HasSuffix(r.URL.Path, "/responses") {
			_, _ = w.Write(answer)
			return
		}
		_, _ = io.WriteString(w, `data: {"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(s.Close)
	return s
}

// requests decodes the bodies the server received.
func (s *slotServer) requests(t *testing.T) []map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, raw := range s.bodies {
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode the request: %v\n%s", err, raw)
		}
		out = append(out, body)
	}
	return out
}

// protocols builds the provider of each protocol on an endpoint.
var protocols = []struct {
	name  string
	build func(provider.Endpoint) (provider.Provider, error)
}{
	{"chat", openai.New},
	{"responses", openai.NewResponses},
}

func slotRequest(slot int) provider.Request {
	return provider.Request{
		Model:    "qwen3.8-27b",
		Messages: []provider.Message{provider.UserMessage("hi")},
		SlotHint: &slot,
	}
}

func TestAServerThatRefusesTheSlotFieldIsAskedAgainWithoutIt(t *testing.T) {
	for _, proto := range protocols {
		t.Run(proto.name, func(t *testing.T) {
			srv := newSlotServer(t, `"Unknown field: id_slot"`)
			p, err := proto.build(provider.Endpoint{BaseURL: srv.URL, SlotHints: true})
			if err != nil {
				t.Fatal(err)
			}
			r := read(t, p, slotRequest(1))
			if r.err != nil || r.stop == "" {
				t.Fatalf("err %v, stop %q: the call failed for its slot hint", r.err, r.stop)
			}
			got := srv.requests(t)
			if len(got) != 2 {
				t.Fatalf("requests = %d, want the refused one and the one without the slot", len(got))
			}
			if _, ok := got[0]["id_slot"]; !ok {
				t.Error("the first request did not name the slot")
			}
			if _, ok := got[1]["id_slot"]; ok {
				t.Error("the second request still named the slot")
			}
		})
	}
}
