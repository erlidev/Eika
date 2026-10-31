package openai_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

// dropConnection closes the connection under w without answering.
func dropConnection(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

func TestStreamCallsAClosedConnectionRetryable(t *testing.T) {
	// A server that drops the connection without answering is a failure of
	// the transport, not of the request: the agent loop must retry it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dropConnection(w)
	}))
	t.Cleanup(srv.Close)

	events := collect(t, newProvider(t, srv.URL), provider.Request{
		Messages: []provider.Message{provider.UserMessage("hi")},
	})
	if len(events) != 1 || events[0].Kind != provider.KindError {
		t.Fatalf("events = %+v, want one error event", events)
	}
	if !provider.Retryable(events[0].Err) {
		t.Errorf("Retryable(%v) = false, want true for a connection closed without a response", events[0].Err)
	}
}

func TestStreamSurvivesAServerThatClosesTheIdleConnection(t *testing.T) {
	// llama.cpp closes the connection when a streamed response ends, and the
	// next request can go out on it before the client has seen the close.
	// Every second request here arrives on such a connection and is dropped
	// unanswered; the provider must still deliver the answer.
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		drop := requests%2 == 0
		mu.Unlock()
		if drop {
			dropConnection(w)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	p := newProvider(t, srv.URL)
	for turn := 1; turn <= 4; turn++ {
		events := collect(t, p, provider.Request{Messages: []provider.Message{provider.UserMessage("hi")}})
		if last := events[len(events)-1]; last.Kind != provider.KindDone {
			t.Fatalf("turn %d ended with %+v, want done", turn, last)
		}
	}
}
