//go:build docker

package server_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
)

// probeWindow stores a probe of the test provider that found the endpoint
// serves window tokens to a request, as the real probe does at start.
func (a *api) probeWindow(t *testing.T, window int) {
	t.Helper()
	capabilities, err := json.Marshal(probe.Capabilities{
		Window: window, Counting: probe.CountNone, CacheMetrics: probe.CacheUnknown,
	})
	if err != nil {
		t.Fatalf("encode capabilities: %v", err)
	}
	if _, err := a.store.RecordProbe(t.Context(), a.testProvider.ID, store.ProbeResult{
		Backend: "other", Slots: 1, KV: "unknown", Capabilities: capabilities, At: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordProbe: %v", err)
	}
}

func TestARunIsJudgedAgainstWhatTheEndpointHoldsPerRequest(t *testing.T) {
	a := newAPI(t)
	model := decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200).Models[0]
	if model.ContextWindow != 32768 {
		t.Fatalf("the test model's window = %d, want 32768", model.ContextWindow)
	}

	// Before a probe states a window, the configured one stands.
	if model.ProbedWindow != 0 || model.EffectiveWindow != 32768 {
		t.Errorf("unprobed model = probed %d, effective %d; want none and the configured 32768", model.ProbedWindow, model.EffectiveWindow)
	}

	// An endpoint that serves less per request than the row allows.
	a.probeWindow(t, 20000)
	model = decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200).Models[0]
	if model.ContextWindow != 32768 || model.ProbedWindow != 20000 || model.EffectiveWindow != 20000 {
		t.Errorf("model = window %d, probed %d, effective %d; want the smaller 20000 in effect",
			model.ContextWindow, model.ProbedWindow, model.EffectiveWindow)
	}
	updated := decodeBody[modelWire](t, request(t, a.Server, "PATCH", "/api/models/"+model.ID,
		map[string]any{"max_output": 512}), 200)
	if updated.EffectiveWindow != 20000 {
		t.Errorf("updated model effective window = %d, want 20000", updated.EffectiveWindow)
	}

	chat := a.newSession(t, "")
	preview := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/context", nil), 200)
	if preview.ContextWindow != 20000 {
		t.Errorf("preview window = %d, want the effective 20000", preview.ContextWindow)
	}

	a.script(providertest.Text("done"))
	a.postMessage(t, chat.ID, "hello", "", 202)
	a.waitIdle(t, chat.ID)
	records := decodeBody[callsWire](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/calls", nil), 200)
	if len(records.Calls) != 1 || records.Calls[0].ContextWindow == nil || *records.Calls[0].ContextWindow != 20000 {
		t.Fatalf("calls = %+v, want one judged against 20000", records.Calls)
	}
	recorded := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/calls/"+records.Calls[0].ID, nil), 200)
	if recorded.ContextWindow != 20000 {
		t.Errorf("recorded window = %d, want the 20000 the call was judged against", recorded.ContextWindow)
	}

	// An endpoint that holds more than the row allows leaves the row's window.
	a.probeWindow(t, 131072)
	model = decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200).Models[0]
	if model.EffectiveWindow != 32768 {
		t.Errorf("effective window with a larger probed one = %d, want the configured 32768", model.EffectiveWindow)
	}
}
