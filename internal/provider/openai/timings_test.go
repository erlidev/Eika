package openai_test

import (
	"math"
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

// usageOf returns the one usage event of a stream, which is where an endpoint
// that measures its own speed reports it.
func usageOf(t *testing.T, events []provider.Event) provider.Event {
	t.Helper()
	var found []provider.Event
	for _, e := range events {
		if e.Kind == provider.KindUsage {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d usage events, want 1", len(found))
	}
	return found[0]
}

// closeEnough compares two millisecond figures, since several endpoints state
// a rate that this code turns back into a duration.
func closeEnough(got, want float64) bool { return math.Abs(got-want) < 0.5 }

// TestStreamReadsEndpointTimings covers the shapes the compatible endpoints
// use to report how fast they ran. Each case is one real final chunk.
func TestStreamReadsEndpointTimings(t *testing.T) {
	cases := []struct {
		name  string
		chunk string
		want  provider.Timings
	}{
		{
			name: "llama.cpp states both phases",
			chunk: `{"id":"c","object":"chat.completion.chunk","choices":[],` +
				`"usage":{"prompt_tokens":1200,"completion_tokens":256,"total_tokens":1456},` +
				`"timings":{"cache_n":0,"prompt_n":1200,"prompt_ms":300.0,"prompt_per_token_ms":0.25,` +
				`"prompt_per_second":4000.0,"predicted_n":256,"predicted_ms":3200.0,` +
				`"predicted_per_token_ms":12.5,"predicted_per_second":80.0}}`,
			want: provider.Timings{
				PromptTokens: 1200, PromptMS: 300,
				DecodeTokens: 256, DecodeMS: 3200,
				Source: provider.TimedByEndpoint,
			},
		},
		{
			name: "vLLM per-request metrics",
			chunk: `{"id":"c","object":"chat.completion.chunk","choices":[],` +
				`"usage":{"prompt_tokens":900,"completion_tokens":137,"total_tokens":1037},` +
				`"metrics":{"time_to_first_token_ms":85.2,"generation_time_ms":1240.5,` +
				`"queue_time_ms":12.3,"mean_itl_ms":9.1,"tokens_per_second":103.2}}`,
			want: provider.Timings{
				PromptTokens: 900, PromptMS: 72.9,
				// The decode window opens at the first output token, so it
				// holds the 136 tokens that followed it.
				DecodeTokens: 136, DecodeMS: 1240.5,
				Source: provider.TimedByEndpoint,
			},
		},
		{
			name: "Ollama durations in nanoseconds",
			chunk: `{"id":"c","object":"chat.completion.chunk","choices":[],` +
				`"usage":{"prompt_tokens":400,"completion_tokens":80,"total_tokens":480},` +
				`"prompt_eval_count":400,"prompt_eval_duration":120000000,` +
				`"eval_count":80,"eval_duration":2000000000}`,
			want: provider.Timings{
				PromptTokens: 400, PromptMS: 120,
				DecodeTokens: 80, DecodeMS: 2000,
				Source: provider.TimedByEndpoint,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSSEServer(t, 200,
				`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
				`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				tc.chunk,
			)
			got := usageOf(t, collect(t, newProvider(t, s.URL), provider.Request{})).Timings
			if got.PromptTokens != tc.want.PromptTokens || got.DecodeTokens != tc.want.DecodeTokens {
				t.Errorf("tokens = %d prompt, %d decode; want %d and %d",
					got.PromptTokens, got.DecodeTokens, tc.want.PromptTokens, tc.want.DecodeTokens)
			}
			if !closeEnough(got.PromptMS, tc.want.PromptMS) || !closeEnough(got.DecodeMS, tc.want.DecodeMS) {
				t.Errorf("times = %.2fms prompt, %.2fms decode; want %.2f and %.2f",
					got.PromptMS, got.DecodeMS, tc.want.PromptMS, tc.want.DecodeMS)
			}
			if got.Source != tc.want.Source {
				t.Errorf("source = %q, want %q", got.Source, tc.want.Source)
			}
		})
	}
}

// TestStreamIgnoresAnImpossibleMeasurement covers an endpoint whose numbers
// cannot make a rate: a phase with no tokens or no time is left unmeasured
// rather than reported as infinitely fast.
func TestStreamIgnoresAnImpossibleMeasurement(t *testing.T) {
	s := newSSEServer(t, 200,
		`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`,
		`{"id":"c","object":"chat.completion.chunk","choices":[],`+
			`"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9},`+
			`"timings":{"prompt_n":8,"prompt_ms":0.0,"predicted_n":1,"predicted_ms":40.0}}`,
	)
	got := usageOf(t, collect(t, newProvider(t, s.URL), provider.Request{})).Timings
	if got.HasPrompt() {
		t.Errorf("prompt phase = %d tokens in %.2fms, want it left unmeasured", got.PromptTokens, got.PromptMS)
	}
	if !got.HasDecode() {
		t.Errorf("decode phase = %+v, want the measurement the endpoint did make", got)
	}
}
