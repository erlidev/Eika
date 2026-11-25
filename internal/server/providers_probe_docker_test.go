//go:build docker

package server_test

import (
	"errors"
	"reflect"
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
