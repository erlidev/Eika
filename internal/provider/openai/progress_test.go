package openai_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
)

// keepAliveServer answers with a stream that sends three comments before its
// one chunk, each only after the client has reported the bytes of the one
// before. A client that reports nothing for a comment leaves the server
// waiting, and the stream ends in a failure the test reads.
func keepAliveServer(t *testing.T, progress *atomic.Int64, chunk string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		flusher.Flush()
		for range 3 {
			seen := progress.Load()
			_, _ = io.WriteString(w, ": keep-alive\n\n")
			flusher.Flush()
			deadline := time.Now().Add(2 * time.Second)
			for progress.Load() == seen {
				if time.Now().After(deadline) {
					http.Error(w, "no progress reported for a comment", http.StatusInternalServerError)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}
		_, _ = io.WriteString(w, chunk)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStreamReportsEverySSECommentAsProgress(t *testing.T) {
	// A server reading a long prompt may send nothing but comments, which no
	// provider turns into an event. The client reports their bytes, so a
	// caller watching for a stalled stream sees the server is alive.
	chat := `data: {"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
	var progress atomic.Int64
	srv := keepAliveServer(t, &progress, chat)
	ctx := provider.WithProgress(context.Background(), func() { progress.Add(1) })
	ch, err := newProvider(t, srv.URL).Stream(ctx, provider.Request{Model: "m", Messages: []provider.Message{provider.UserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var last provider.Event
	for e := range ch {
		last = e
	}
	if last.Kind != provider.KindDone {
		t.Fatalf("the stream ended with %+v: a comment's bytes were not reported", last)
	}
	if got := progress.Load(); got < 4 {
		t.Errorf("progress reported %d times, want at least 4 (the response and three comments)", got)
	}
}
