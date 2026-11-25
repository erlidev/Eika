package openai_test

import (
	"net/http"
	"net/http/httptest"
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
