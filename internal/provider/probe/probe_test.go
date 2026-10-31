package probe_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/provider/probe/probetest"
)

const testKey = "test-probe-key"

// target lists the models of a fake server through the harness's own
// provider, as the server does before it probes.
func target(t *testing.T, srv *httptest.Server, key string) probe.Target {
	t.Helper()
	p, err := openai.New(provider.Endpoint{BaseURL: srv.URL + "/v1", APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	models, err := p.(provider.Lister).Models(context.Background())
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	return probe.Target{BaseURL: srv.URL + "/v1", APIKey: key, Models: models}
}

func TestRunReadsWhatEachBackendStates(t *testing.T) {
	tests := []struct {
		name string
		srv  func(testing.TB, string) *httptest.Server
		want probe.Result
	}{
		{
			// Recorded from the bench. The server states two slots and 131072
			// tokens per slot; it does not say whether the cache is unified.
			name: "llama.cpp, recorded",
			srv:  probetest.LlamaCpp,
			want: probe.Result{
				Backend: probe.LlamaCpp, Slots: 2, KV: probe.Unknown,
				Capabilities: probe.Capabilities{
					Build: "b11942-338fce651", Window: 131072, Counting: probe.CountChat,
					CacheMetrics: probe.CacheReported, SlotHints: true,
				},
				Details: probe.Details{
					ModelFile:       "Qwen3.8-27B-UD-Q4_K_XL.gguf",
					ModelRevision:   "4ca720788d1e01f1bff70c033e0d0028fd02e502",
					Quantisation:    "Q4_K - Medium",
					Parameters:      27320697856,
					SizeBytes:       17548181504,
					TrainContext:    262144,
					Vision:          true,
					SlotsEndpoint:   true,
					MetricsEndpoint: true,
				},
			},
		},
		{
			// 16 tokens a block and 8421 blocks. Unverified fixture.
			name: "vLLM, unverified fixture",
			srv:  probetest.VLLM,
			want: probe.Result{
				Backend: probe.VLLM, Slots: 1, KV: probe.Shared, KVTokens: 134736,
				Capabilities: probe.Capabilities{
					Build: "0.11.0", Window: 40960, Counting: probe.CountChat, CacheMetrics: probe.CacheUnknown,
				},
			},
		},
		{
			// The window comes from the model list: context_length is null.
			name: "SGLang, unverified fixture",
			srv:  probetest.SGLang,
			want: probe.Result{
				Backend: probe.SGLang, Slots: 1, KV: probe.Shared, KVTokens: 151290,
				Capabilities: probe.Capabilities{
					Build: "0.5.2", Window: 40960, Counting: probe.CountText, CacheMetrics: probe.CacheUnknown,
				},
			},
		},
		{
			name: "TabbyAPI, unverified fixture",
			srv:  probetest.Tabby,
			want: probe.Result{
				Backend: probe.Tabby, Slots: 4, KV: probe.Shared, KVTokens: 131072,
				Capabilities: probe.Capabilities{
					Window: 131072, Counting: probe.CountChat, CacheMetrics: probe.CacheUnknown,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := tt.srv(t, testKey)
			got := probe.Run(context.Background(), target(t, srv, testKey))
			if got != tt.want {
				t.Errorf("result = %+v\nwant     %+v", got, tt.want)
			}
		})
	}
}

func TestRunWithoutTheKeyIdentifiesNothing(t *testing.T) {
	srv := probetest.LlamaCpp(t, testKey)
	// The server refuses every route without its key, so none of its
	// descriptions can be read; the probe falls back rather than failing.
	got := probe.Run(context.Background(), probe.Target{BaseURL: srv.URL + "/v1"})
	if got.Backend != probe.Other || got.KV != probe.Unknown || got.Capabilities.Counting != probe.CountNone {
		t.Errorf("result = %+v, want an unidentified local server", got)
	}
}

func TestRunOnAnEndpointThatNeverAnswersAsksOnce(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close() // no answer at all
		}
	}))
	t.Cleanup(srv.Close)

	got := probe.Run(context.Background(), probe.Target{BaseURL: srv.URL + "/v1"})
	if got.Backend != probe.Other || requests.Load() != 1 {
		t.Errorf("result = %+v after %d requests, want an unidentified server after 1", got, requests.Load())
	}
}

func TestRunOnAHostedAPIDefaultsToEightSlots(t *testing.T) {
	srv := probetest.Cloud(t, "")
	tgt := target(t, srv, "")
	// httptest listens on loopback, which is local. A hosted API is judged by
	// its address, so the same model list behind a public name is a cloud one.
	tgt.BaseURL = "https://api.example.com/v1"
	got := probe.Run(context.Background(), tgt)
	want := probe.Result{
		Backend: probe.OpenAI, Slots: 8, KV: probe.Unknown,
		Capabilities: probe.Capabilities{Counting: probe.CountNone, CacheMetrics: probe.CacheUnknown},
	}
	if got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}

	local := probe.Run(context.Background(), target(t, srv, ""))
	if local.Backend != probe.Other || local.Slots != 1 {
		t.Errorf("a model list on the local network = %+v, want backend other with 1 slot", local)
	}
}

func TestRunOnASingleSlotLlamaCppIsShared(t *testing.T) {
	// One slot owns the whole cache whether or not the cache is unified, so
	// the answer is known and the size is the window.
	props := `{"build_info":"b1","total_slots":1,"default_generation_settings":{"n_ctx":32768}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/props" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(props))
	}))
	t.Cleanup(srv.Close)

	got := probe.Run(context.Background(), probe.Target{BaseURL: srv.URL + "/v1"})
	if got.Backend != probe.LlamaCpp || got.KV != probe.Shared || got.KVTokens != 32768 || got.Slots != 1 {
		t.Errorf("result = %+v, want llama.cpp, shared, 32768 tokens, 1 slot", got)
	}
	if got.Capabilities.Counting != probe.CountNone {
		t.Errorf("counting = %q: the tokenizer routes are missing, so none", got.Capabilities.Counting)
	}
}

func TestRunCountsTextWhenOnlyTheTokenizerRouteExists(t *testing.T) {
	props := `{"build_info":"b1","total_slots":2,"default_generation_settings":{"n_ctx":8192}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			_, _ = w.Write([]byte(props))
		case "/tokenize":
			_, _ = w.Write([]byte(`{"tokens":[1,2]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	got := probe.Run(context.Background(), probe.Target{BaseURL: srv.URL})
	if got.Capabilities.Counting != probe.CountText {
		t.Errorf("counting = %q, want text", got.Capabilities.Counting)
	}
}

func TestRunNeverSendsAPromptToAModel(t *testing.T) {
	var paths []string
	inner := probetest.LlamaCpp(t, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		inner.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	probe.Run(context.Background(), probe.Target{BaseURL: srv.URL + "/v1"})
	for _, p := range paths {
		if strings.Contains(p, "completions") || strings.Contains(p, "responses") || strings.Contains(p, "/completion") {
			t.Errorf("the probe requested %s, which would run the model", p)
		}
	}
}

func TestBackendHelpers(t *testing.T) {
	if !probe.LlamaCpp.Valid() || probe.Backend("lmstudio").Valid() {
		t.Error("Valid does not tell the known backends from an unknown one")
	}
	if probe.DefaultSlots(probe.OpenAI) != 8 || probe.DefaultSlots(probe.VLLM) != 1 {
		t.Error("default slots are not 1 for a local backend and 8 for a hosted one")
	}
	if !probe.Shared.Valid() || !probe.Partitioned.Valid() || !probe.Unknown.Valid() || probe.KV("x").Valid() {
		t.Error("KV.Valid does not accept exactly shared, partitioned and unknown")
	}
}
