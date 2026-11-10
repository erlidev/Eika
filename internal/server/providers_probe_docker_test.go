//go:build docker

package server_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/probe/probetest"
)

// benchProvider stores a provider on a fake llama.cpp server with the
// bench's description (2 slots, 131072 tokens each) and makes its model list
// answer.
func benchProvider(t *testing.T, a *api, name string) providerWire {
	t.Helper()
	baseURL := probetest.LlamaCpp(t, "").URL + "/v1"
	a.serveAt(baseURL, listingProvider{models: []provider.ModelInfo{{ID: "qwen3.8-27b"}}})
	return decodeBody[providerWire](t, request(t, a.Server, "POST", "/api/providers", map[string]any{
		"name": name, "base_url": baseURL,
	}), 201)
}

func probeProvider(t *testing.T, a *api, id string) probeWire {
	t.Helper()
	return decodeBody[probeWire](t, request(t, a.Server, "POST", "/api/providers/probe", map[string]any{"provider_id": id}), 200)
}

func providerNamed(t *testing.T, a *api, id string) providerWire {
	t.Helper()
	for _, p := range decodeBody[providersWire](t, request(t, a.Server, "GET", "/api/providers", nil), 200).Providers {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("provider %s is not listed", id)
	return providerWire{}
}

func TestANewProviderIsUnprobed(t *testing.T) {
	a := newAPI(t)
	p := benchProvider(t, a, "bench")
	if p.Backend != "other" || p.Slots != 1 || p.KV != "unknown" || p.Pool != "bench" || p.ProbedAt != "" ||
		len(p.Overridden) != 0 || p.Capabilities.Counting != "none" {
		t.Errorf("provider = %+v, want the defaults of an endpoint nobody probed", p)
	}
}

func TestProbingAStoredProviderRecordsWhatTheEndpointSays(t *testing.T) {
	a := newAPI(t)
	p := benchProvider(t, a, "bench")

	got := probeProvider(t, a, p.ID)
	if got.Probe.Backend != "llamacpp" || got.Probe.Slots != 2 || got.Probe.KV != "unknown" || len(got.Models) != 1 {
		t.Errorf("probe = %+v", got)
	}
	stored := providerNamed(t, a, p.ID)
	if got.Provider == nil || got.Provider.Backend != "llamacpp" {
		t.Errorf("the response carries provider %+v, want the stored one", got.Provider)
	}
	if stored.Backend != "llamacpp" || stored.Slots != 2 || stored.Capabilities.Window != 131072 ||
		stored.Capabilities.Counting != "chat" || stored.Capabilities.Build != "b11942-338fce651" ||
		!stored.Capabilities.SlotHints || stored.ProbedAt == "" || stored.ProbeError != "" {
		t.Errorf("stored provider = %+v", stored)
	}
}

func TestAFormProbeWritesNothing(t *testing.T) {
	a := newAPI(t)
	p := benchProvider(t, a, "bench")
	other := probetest.LlamaCpp(t, "").URL + "/v1"
	a.serveAt(other, listingProvider{models: []provider.ModelInfo{{ID: "m"}}})

	// An address nothing stores, a stored provider tried at another address,
	// and one tried with a key typed into the form: what comes back is about
	// the thing probed, so none of them is recorded on the stored provider.
	decodeBody[probeWire](t, request(t, a.Server, "POST", "/api/providers/probe", map[string]any{"base_url": other}), 200)
	got := decodeBody[probeWire](t, request(t, a.Server, "POST", "/api/providers/probe", map[string]any{
		"provider_id": p.ID, "base_url": other,
	}), 200)
	if got.Provider != nil {
		t.Errorf("a probe at another address carries the stored provider %+v", got.Provider)
	}
	decodeBody[probeWire](t, request(t, a.Server, "POST", "/api/providers/probe", map[string]any{
		"provider_id": p.ID, "api_key": "typed",
	}), 200)
	if stored := providerNamed(t, a, p.ID); stored.ProbedAt != "" || stored.Backend != "other" {
		t.Errorf("stored provider = %+v, want it untouched", stored)
	}
}

func TestAnOverrideSurvivesTheNextProbe(t *testing.T) {
	a := newAPI(t)
	p := benchProvider(t, a, "bench")
	probeProvider(t, a, p.ID)

	// The probe cannot tell whether the cache of this 2-slot server is
	// shared, so the user says it, and which GPU it runs on.
	set := decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+p.ID, map[string]any{
		"kv": "shared", "kv_tokens": 131072, "pool": "rtx5090", "slots": 3,
	}), 200)
	if set.KV != "shared" || set.KVTokens != 131072 || set.Pool != "rtx5090" || set.Slots != 3 ||
		!slices.Equal(set.Overridden, []string{"slots", "kv", "pool"}) {
		t.Fatalf("after the override = %+v", set)
	}

	probeProvider(t, a, p.ID)
	kept := providerNamed(t, a, p.ID)
	if kept.KV != "shared" || kept.KVTokens != 131072 || kept.Pool != "rtx5090" || kept.Slots != 3 || kept.Backend != "llamacpp" {
		t.Errorf("after the next probe = %+v, want the user's values kept and the backend probed", kept)
	}

	// Handing a field back lets the next probe write it.
	decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+p.ID, map[string]any{"reset": []string{"kv", "slots", "pool"}}), 200)
	probeProvider(t, a, p.ID)
	back := providerNamed(t, a, p.ID)
	if back.KV != "unknown" || back.KVTokens != 0 || back.Slots != 2 || back.Pool != "bench" || len(back.Overridden) != 0 {
		t.Errorf("after the reset and a probe = %+v", back)
	}
}

func TestProviderOverridesAreValidated(t *testing.T) {
	a := newAPI(t)
	p := benchProvider(t, a, "bench")
	for name, body := range map[string]map[string]any{
		"an unknown backend":        {"backend": "lmstudio"},
		"no slots":                  {"slots": 0},
		"too many slots":            {"slots": 100000},
		"an unknown kv":             {"kv": "pooled"},
		"a negative cache":          {"kv": "shared", "kv_tokens": -1},
		"a size on a partitioned":   {"kv": "partitioned", "kv_tokens": 1000},
		"a size on an unknown kv":   {"kv_tokens": 1000},
		"a pool with a control":     {"pool": "gpu\n0"},
		"a reset of something else": {"reset": []string{"base_url"}},
	} {
		t.Run(name, func(t *testing.T) {
			if rec := request(t, a.Server, "PATCH", "/api/providers/"+p.ID, body); rec.Code != 400 {
				t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
	if got := providerNamed(t, a, p.ID); len(got.Overridden) != 0 {
		t.Errorf("a refused override was stored: %+v", got)
	}
}

func TestAFailedProbeKeepsTheEarlierResult(t *testing.T) {
	a := newAPI(t)
	p := benchProvider(t, a, "bench")
	probeProvider(t, a, p.ID)
	first := providerNamed(t, a, p.ID)

	// The endpoint stops answering its model list.
	row, err := a.store.Provider(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.serveAt(row.BaseURL, listingProvider{err: &provider.Error{Op: "list models", Err: errors.New("connection refused")}})
	if rec := request(t, a.Server, "POST", "/api/providers/probe", map[string]any{"provider_id": p.ID}); rec.Code != 400 {
		t.Fatalf("probe of a silent endpoint = %d", rec.Code)
	}
	failed := providerNamed(t, a, p.ID)
	if failed.ProbeError == "" || failed.Backend != "llamacpp" || failed.Slots != 2 || failed.ProbedAt != first.ProbedAt ||
		failed.Capabilities.Window != 131072 {
		t.Errorf("after a failed probe = %+v, want the error recorded and nothing lost", failed)
	}

	a.serveAt(row.BaseURL, listingProvider{models: []provider.ModelInfo{{ID: "qwen3.8-27b"}}})
	probeProvider(t, a, p.ID)
	if healed := providerNamed(t, a, p.ID); healed.ProbeError != "" {
		t.Errorf("probe error after a success = %q", healed.ProbeError)
	}
}

func TestAProbeThatIdentifiesNothingIsNotAnOverride(t *testing.T) {
	a := newAPI(t)
	// The default test provider's address does not resolve, so no backend
	// claims it; a public-looking name is taken for a hosted API.
	a.use(listingProvider{models: []provider.ModelInfo{{ID: "alpha"}}})
	got := probeProvider(t, a, a.testProvider.ID)
	if got.Probe.Backend != "openai" || got.Probe.Slots != 8 || got.Probe.KV != "unknown" {
		t.Errorf("probe = %+v, want a hosted API with 8 slots", got.Probe)
	}
	if p := providerNamed(t, a, a.testProvider.ID); len(p.Overridden) != 0 || p.Backend != "openai" {
		t.Errorf("stored provider = %+v, want the probed backend and nothing overridden", p)
	}
}

// Core probes every provider at start, and a provider that cannot be reached
// neither stops that nor loses what an earlier probe found.
func TestStartProbesEveryProviderAndSurvivesFailures(t *testing.T) {
	a := newAPI(t)
	good := benchProvider(t, a, "good")
	bad := benchProvider(t, a, "bad")
	a.ProbeProviders(t.Context())
	if p := providerNamed(t, a, good.ID); p.Backend != "llamacpp" || p.ProbedAt == "" {
		t.Fatalf("good provider after the first start = %+v", p)
	}
	earlier := providerNamed(t, a, bad.ID)

	row, err := a.store.Provider(t.Context(), bad.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.serveAt(row.BaseURL, listingProvider{err: &provider.Error{Op: "list models", Err: errors.New("no route to host")}})
	a.ProbeProviders(t.Context())

	if p := providerNamed(t, a, good.ID); p.ProbeError != "" || p.Backend != "llamacpp" {
		t.Errorf("good provider after the second start = %+v", p)
	}
	after := providerNamed(t, a, bad.ID)
	if after.ProbeError == "" || after.Backend != earlier.Backend || !reflect.DeepEqual(after.Capabilities, earlier.Capabilities) || after.ProbedAt != earlier.ProbedAt {
		t.Errorf("unreachable provider after the second start = %+v, want its earlier result and the error", after)
	}
}
