package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
)

// endpointOn returns the endpoint a run on a llama.cpp server at url would
// use, with the capabilities a probe leaves.
func endpointOn(t *testing.T, url, key string, counting probe.Counting) endpoint {
	t.Helper()
	caps, err := json.Marshal(probe.Capabilities{Counting: counting, CacheMetrics: probe.CacheUnknown})
	if err != nil {
		t.Fatalf("encode capabilities: %v", err)
	}
	client, err := openai.New(provider.Endpoint{BaseURL: url, APIKey: key})
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	return endpoint{
		row:    store.Provider{Name: "local", BaseURL: url, Backend: string(probe.LlamaCpp), Capabilities: caps},
		key:    key,
		client: client,
	}
}

func TestASessionKeepsItsCounterAcrossRuns(t *testing.T) {
	counters := newSessionCounters()
	model := store.Model{Model: "qwen"}
	ep := endpointOn(t, "http://127.0.0.1:1/v1", "k", probe.CountChat)

	first := counters.forRun("s1", ep, model)
	if again := counters.forRun("s1", endpointOn(t, "http://127.0.0.1:1/v1", "k", probe.CountChat), model); again != first {
		t.Error("the same session on the same endpoint got a new counter, so nothing it learned survives a turn")
	}
	if other := counters.forRun("s2", ep, model); other == first {
		t.Error("two sessions share a counter")
	}
}

func TestACounterIsRebuiltWhenTheEndpointChanges(t *testing.T) {
	counters := newSessionCounters()
	model := store.Model{Model: "qwen"}
	base := endpointOn(t, "http://127.0.0.1:1/v1", "k", probe.CountChat)
	first := counters.forRun("s1", base, model)

	changes := map[string]func() (endpoint, store.Model){
		"another model": func() (endpoint, store.Model) { return base, store.Model{Model: "llama"} },
		"another address": func() (endpoint, store.Model) {
			return endpointOn(t, "http://127.0.0.1:2/v1", "k", probe.CountChat), model
		},
		"another key": func() (endpoint, store.Model) {
			return endpointOn(t, "http://127.0.0.1:1/v1", "j", probe.CountChat), model
		},
		"another counting kind": func() (endpoint, store.Model) {
			return endpointOn(t, "http://127.0.0.1:1/v1", "k", probe.CountText), model
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			ep, m := change()
			if got := counters.forRun("s1", ep, m); got == first {
				t.Errorf("the counter of %s was kept", name)
			}
		})
	}
}

// A provider that cannot state its chat body still gets a counter that
// predicts, by the estimated method.
func TestAProviderThatCannotEncodeChatStillGetsACounter(t *testing.T) {
	counters := newSessionCounters()
	ep := endpointOn(t, "http://127.0.0.1:1/v1", "", probe.CountChat)
	ep.client = providertest.New()
	c := counters.forRun("s1", ep, store.Model{Model: "m"})
	pred, err := c.Predict(context.Background(), provider.Request{Model: "m", Messages: []provider.Message{provider.UserMessage("hello there")}})
	if err != nil || pred.Method != provider.MethodEstimated || pred.Tokens <= 0 {
		t.Errorf("Predict = %+v, %v; want an estimate", pred, err)
	}
}

func TestAnUnprobedProviderIsCountedByEstimate(t *testing.T) {
	counters := newSessionCounters()
	ep := endpointOn(t, "http://127.0.0.1:1/v1", "", probe.CountNone)
	ep.row.Capabilities = nil
	c := counters.forRun("s1", ep, store.Model{Model: "m"})
	pred, err := c.Predict(context.Background(), provider.Request{Model: "m", Messages: []provider.Message{provider.UserMessage("hello there")}})
	if err != nil || pred.Method != provider.MethodEstimated {
		t.Errorf("Predict = %+v, %v; want an estimate", pred, err)
	}
}

func TestCountersAreBoundedAndForgotten(t *testing.T) {
	counters := newSessionCounters()
	model := store.Model{Model: "qwen"}
	ep := endpointOn(t, "http://127.0.0.1:1/v1", "k", probe.CountChat)
	first := counters.forRun("s0", ep, model)
	for i := 1; i <= maxSessionCounters; i++ {
		counters.forRun(fmt.Sprintf("s%d", i), ep, model)
	}
	if len(counters.byID) != maxSessionCounters {
		t.Errorf("%d counters held, want at most %d", len(counters.byID), maxSessionCounters)
	}
	if again := counters.forRun("s0", ep, model); again == first {
		t.Error("the least recently used session kept its counter past the bound")
	}
	counters.forget("s0")
	if _, held := counters.byID["s0"]; held {
		t.Error("a forgotten session still holds a counter")
	}
}
