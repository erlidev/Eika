//go:build docker

package server_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/probe/probetest"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
)

// conforming scripts the two responses a Responses-capable endpoint gives the
// conformance check: a tool call with a reasoning item, and an answer that
// states a cached count.
func conforming() []providertest.Step {
	item := json.RawMessage(`{"id":"rs_1","type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"echo it"}],"encrypted_content":""}`)
	first := providertest.Stream(
		provider.Event{Kind: provider.KindReasoningItem, ReasoningItem: item},
		provider.Event{Kind: provider.KindToolCall, ToolCall: providertest.Call("call_1", "echo", map[string]string{"word": "ping"})},
		provider.Event{Kind: provider.KindUsage, Usage: providertest.Usage(40, 20, 60)},
		provider.Done("tool_calls"))
	usage := providertest.Usage(70, 3, 73)
	usage.CachedTokens = provider.Reported(60)
	second := providertest.Stream(provider.TextDelta("ping"), provider.Event{Kind: provider.KindUsage, Usage: usage}, provider.Done("stop"))
	return []providertest.Step{first, second}
}

// responsesBench stores a provider on a fake llama.cpp server whose scripted
// provider answers the conformance check as the steps say, and gives it one
// model, which the check asks. The user sets its slots to one, so that the
// slot affinity check, which only a server with several slots gets, asks the
// model nothing and the steps are the Responses check's alone.
func responsesBench(t *testing.T, a *api, steps ...providertest.Step) (providerWire, listingProvider) {
	t.Helper()
	p, served := slottedBench(t, a, steps...)
	decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+p.ID, map[string]any{"slots": 1}), 200)
	return p, served
}

// slottedBench is responsesBench on the two slots the recorded server has.
func slottedBench(t *testing.T, a *api, steps ...providertest.Step) (providerWire, listingProvider) {
	t.Helper()
	baseURL := probetest.LlamaCpp(t, "").URL + "/v1"
	served := listingProvider{Provider: providertest.New(steps...), models: []provider.ModelInfo{{ID: "qwen3.8-27b"}}}
	a.serveAt(baseURL, served)
	p := decodeBody[providerWire](t, request(t, a.Server, "POST", "/api/providers", map[string]any{"name": "bench", "base_url": baseURL}), 201)
	decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", map[string]any{
		"provider_id": p.ID, "name": "qwen", "model": "qwen3.8-27b", "context_window": 32768, "max_output": 4096,
	}), 201)
	return p, served
}

// storeProbe is a probe result that writes capabilities over a stored
// provider and changes nothing else.
func storeProbe(row store.Provider, capabilities []byte) store.ProbeResult {
	return store.ProbeResult{
		Backend: row.Backend, Slots: row.Slots, KV: row.KV, KVTokens: row.KVTokens,
		Capabilities: capabilities, At: time.Now().UTC(),
	}
}

func TestAnExplicitProbeChoosesResponsesWhenTheCheckPasses(t *testing.T) {
	a := newAPI(t)
	p, served := responsesBench(t, a, conforming()...)
	if p.API != "chat" {
		t.Fatalf("a new provider speaks %q, want chat", p.API)
	}
	got := probeProvider(t, a, p.ID)
	if got.Probe.API != "responses" || got.Provider == nil || got.Provider.API != "responses" {
		t.Fatalf("probe = %+v, want the Responses API chosen and stored", got)
	}
	c := providerNamed(t, a, p.ID).Capabilities.Responses
	if c == nil || c.Verdict != "passed" || c.Model != "qwen3.8-27b" || c.Build != "b11942-338fce651" || !c.Reasoning || !c.CacheMetrics || c.Reason != "" {
		t.Errorf("conformance = %+v, want passed on the model and build, with reasoning and cache metrics", c)
	}
	if served.Calls() != 2 {
		t.Errorf("the check sent %d requests, want 2", served.Calls())
	}
	if e := a.lastEndpoint(); e.API != provider.APIResponses || e.Hosted {
		t.Errorf("the check's endpoint = %+v, want the Responses API on a local server", e)
	}
}

func TestACheckThatFailsLeavesTheProviderOnChat(t *testing.T) {
	a := newAPI(t)
	p, _ := responsesBench(t, a, providertest.Fail(&provider.Error{Op: "stream response", StatusCode: 404, Err: errors.New("404 page not found")}))
	got := probeProvider(t, a, p.ID)
	stored := providerNamed(t, a, p.ID)
	c := stored.Capabilities.Responses
	if got.Probe.API != "chat" || stored.API != "chat" || c == nil || c.Verdict != "failed" || !strings.Contains(c.Reason, "404") {
		t.Errorf("probe %q, stored %q, conformance %+v; want chat and a failure that says why", got.Probe.API, stored.API, c)
	}
}

func TestAProviderWithNoModelIsNotAsked(t *testing.T) {
	a := newAPI(t)
	p := benchProvider(t, a, "bench")
	got := probeProvider(t, a, p.ID)
	c := providerNamed(t, a, p.ID).Capabilities.Responses
	if got.Probe.API != "chat" || c == nil || c.Verdict != "skipped" || c.Reason == "" {
		t.Errorf("probe %q, conformance %+v; want chat and a skipped check with its reason", got.Probe.API, c)
	}
}

func TestAFormProbeOfAnUnsavedEndpointAsksNoModel(t *testing.T) {
	a := newAPI(t)
	_, served := responsesBench(t, a, conforming()...)
	other := probetest.LlamaCpp(t, "").URL + "/v1"
	a.serveAt(other, served)
	got := decodeBody[probeWire](t, request(t, a.Server, "POST", "/api/providers/probe", map[string]any{"base_url": other}), 200)
	if got.Probe.API != "chat" || served.Calls() != 0 {
		t.Errorf("probe %q after %d model requests, want chat and none", got.Probe.API, served.Calls())
	}
}

func TestStartAsksTheModelOnlyWhenTheCheckIsStale(t *testing.T) {
	a := newAPI(t)
	p, served := responsesBench(t, a, slices.Concat(conforming(), conforming(), conforming())...)
	a.ProbeProviders(t.Context())
	if got := providerNamed(t, a, p.ID); got.API != "responses" || served.Calls() != 2 {
		t.Fatalf("first start: api %q after %d requests, want responses after 2", got.API, served.Calls())
	}

	// Core restarts: the build and the model are the ones the check ran on.
	a.ProbeProviders(t.Context())
	a.ProbeProviders(t.Context())
	if got := providerNamed(t, a, p.ID); got.API != "responses" || served.Calls() != 2 {
		t.Errorf("later starts: api %q after %d requests, want responses and no new request", got.API, served.Calls())
	}

	// The server was upgraded: the check was made against another build.
	row, err := a.store.Provider(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var caps map[string]any
	if err := json.Unmarshal(row.Capabilities, &caps); err != nil {
		t.Fatal(err)
	}
	caps["responses"].(map[string]any)["build"] = "b11000"
	changed, _ := json.Marshal(caps)
	if _, err := a.store.RecordProbe(t.Context(), p.ID, storeProbe(row, changed)); err != nil {
		t.Fatal(err)
	}
	a.ProbeProviders(t.Context())
	if got := providerNamed(t, a, p.ID); served.Calls() != 4 || got.Capabilities.Responses.Build != "b11942-338fce651" {
		t.Errorf("after an upgrade: %d requests, build %q; want the check again, on the new build", served.Calls(), got.Capabilities.Responses.Build)
	}

	// The user changed the model the check would ask.
	models := decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200).Models
	for _, m := range models {
		if m.ProviderID == p.ID {
			if rec := request(t, a.Server, "DELETE", "/api/models/"+m.ID, nil); rec.Code != 204 {
				t.Fatalf("delete model = %d", rec.Code)
			}
		}
	}
	decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", map[string]any{
		"provider_id": p.ID, "name": "other", "model": "another-model", "context_window": 32768, "max_output": 4096,
	}), 201)
	a.ProbeProviders(t.Context())
	if got := providerNamed(t, a, p.ID); served.Calls() != 6 || got.Capabilities.Responses.Model != "another-model" {
		t.Errorf("after a model change: %d requests, model %q; want the check again, on the new model", served.Calls(), got.Capabilities.Responses.Model)
	}
}

func TestTheUsersApiSurvivesAProbeUntilTheyHandItBack(t *testing.T) {
	a := newAPI(t)
	// The recorded server has two slots, so the slot check asks too and finds nothing
	// scripted; the user's only override is the api.
	p, _ := slottedBench(t, a, providertest.Fail(errors.New("no route")), providertest.Fail(errors.New("no route")))

	set := decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+p.ID, map[string]any{"api": "responses"}), 200)
	if set.API != "responses" || !slices.Equal(set.Overridden, []string{"api"}) {
		t.Fatalf("after the override = %+v", set)
	}
	probeProvider(t, a, p.ID)
	kept := providerNamed(t, a, p.ID)
	if kept.API != "responses" || kept.Capabilities.Responses.Verdict != "failed" {
		t.Errorf("after a probe whose check failed = api %q, conformance %+v; want the user's api and the check's own finding", kept.API, kept.Capabilities.Responses)
	}

	decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+p.ID, map[string]any{"reset": []string{"api"}}), 200)
	a.ProbeProviders(t.Context())
	if back := providerNamed(t, a, p.ID); back.API != "chat" || len(back.Overridden) != 0 {
		t.Errorf("after the reset and a start = %+v, want the probe's chat, set from the check on record", back)
	}

	for _, body := range []map[string]any{{"api": "soap"}, {"api": ""}, {"reset": []string{"apii"}}} {
		if rec := request(t, a.Server, "PATCH", "/api/providers/"+p.ID, body); rec.Code != 400 {
			t.Errorf("PATCH %v = %d, want 400", body, rec.Code)
		}
	}
}

func TestOnlyAHostedAPIIsBuiltAsHosted(t *testing.T) {
	a := newAPI(t)
	// The test provider's address does not resolve to a local one, so the
	// probe takes it for a hosted API; the bench's llama.cpp is local.
	a.use(listingProvider{models: []provider.ModelInfo{{ID: "alpha"}}})
	probeProvider(t, a, a.testProvider.ID)
	request(t, a.Server, "POST", "/api/models/test", map[string]any{"provider_id": a.testProvider.ID, "model": "m"})
	if e := a.lastEndpoint(); !e.Hosted {
		t.Errorf("endpoint of a hosted API = %+v, want Hosted", e)
	}
	p, _ := responsesBench(t, a)
	request(t, a.Server, "POST", "/api/models/test", map[string]any{"provider_id": p.ID, "model": "qwen3.8-27b"})
	probeProvider(t, a, p.ID)
	request(t, a.Server, "POST", "/api/models/test", map[string]any{"provider_id": p.ID, "model": "qwen3.8-27b"})
	if e := a.lastEndpoint(); e.Hosted {
		t.Errorf("endpoint of llama.cpp = %+v, want not Hosted", e)
	}
}

func TestARunSpeaksTheProtocolTheProviderRowNames(t *testing.T) {
	a := newAPI(t)
	p, _ := responsesBench(t, a)
	for _, api := range []string{"responses", "chat"} {
		decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+p.ID, map[string]any{"api": api}), 200)
		request(t, a.Server, "POST", "/api/models/test", map[string]any{"provider_id": p.ID, "model": "qwen3.8-27b"})
		if e := a.lastEndpoint(); e.API != provider.API(api) {
			t.Errorf("api %s: the endpoint was built for %q", api, e.API)
		}
	}
}
