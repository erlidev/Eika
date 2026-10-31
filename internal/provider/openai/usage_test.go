package openai_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

// recordedServer replays a stream recorded from llama.cpp (see the README in
// internal/provider/testdata/usage) byte for byte.
func recordedServer(t *testing.T, name string) *httptest.Server {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "usage", "llamacpp", name))
	if err != nil {
		t.Fatalf("read recording: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// lastUsage returns the usage of the last usage event of a stream, and how
// many there were.
func lastUsage(events []provider.Event) (provider.Usage, int) {
	var usage provider.Usage
	n := 0
	for _, e := range events {
		if e.Kind == provider.KindUsage {
			usage, n = e.Usage, n+1
		}
	}
	return usage, n
}

func TestStreamReadsTheUsageLlamaCppRecorded(t *testing.T) {
	rep := provider.Reported
	cases := []struct {
		name string
		file string
		want provider.Usage
	}{
		{"cold cache", "chat_cold.sse", provider.Usage{
			InputTokens: rep(90), OutputTokens: rep(100), TotalTokens: rep(190), CachedTokens: rep(0), PrefilledTokens: rep(90)}},
		{"warm cache", "chat_warm.sse", provider.Usage{
			InputTokens: rep(90), OutputTokens: rep(100), TotalTokens: rep(190), CachedTokens: rep(86), PrefilledTokens: rep(4)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newProvider(t, recordedServer(t, c.file).URL)
			events := collect(t, p, provider.Request{Messages: []provider.Message{provider.UserMessage("hi")}})
			got, n := lastUsage(events)
			if got != c.want {
				t.Errorf("usage = %+v, want %+v", got, c.want)
			}
			if n != 1 {
				t.Errorf("usage events = %d, want one", n)
			}
			if last := events[len(events)-1]; last.Kind != provider.KindDone || last.StopReason != "stop" {
				t.Errorf("last event = %+v, want done/stop", last)
			}
		})
	}
}

func TestStreamLeavesWhatTheEndpointDoesNotReportUnreported(t *testing.T) {
	// A server that states only the three headline counts says nothing about
	// the cache or the reasoning, and neither is turned into a zero.
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7,"prompt_tokens_details":null}}`,
	)
	p := newProvider(t, s.URL)
	got, _ := lastUsage(collect(t, p, provider.Request{Messages: []provider.Message{provider.UserMessage("hi")}}))
	if got.CachedTokens.Reported() || got.PrefilledTokens.Reported() || got.ReasoningTokens.Reported() {
		t.Errorf("usage = %+v, want cached, prefilled and reasoning not reported", got)
	}
}

func TestStreamReportsAStatedZeroCachedCount(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`,
	)
	p := newProvider(t, s.URL)
	got, _ := lastUsage(collect(t, p, provider.Request{Messages: []provider.Message{provider.UserMessage("hi")}}))
	if got.CachedTokens != provider.Reported(0) || got.ReasoningTokens != provider.Reported(0) {
		t.Errorf("usage = %+v, want cached and reasoning reported as 0", got)
	}
}

func TestStreamIgnoresPlaceholderUsageInEarlierChunks(t *testing.T) {
	// Gateways that send a usage block of zeros on every chunk before the last
	// state nothing in it: no usage event is relayed for those chunks.
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"}}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
	)
	p := newProvider(t, s.URL)
	got, n := lastUsage(collect(t, p, provider.Request{Messages: []provider.Message{provider.UserMessage("hi")}}))
	if n != 1 || got.InputTokens != provider.Reported(5) {
		t.Errorf("got %d usage events, last %+v, want one stating 5 input tokens", n, got)
	}
}

func TestStreamWithoutAnyUsageSendsNoUsageEvent(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`,
	)
	p := newProvider(t, s.URL)
	if got, n := lastUsage(collect(t, p, provider.Request{Messages: []provider.Message{provider.UserMessage("hi")}})); n != 0 {
		t.Errorf("got %d usage events (%+v), want none", n, got)
	}
}
