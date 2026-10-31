//go:build docker

package server_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// setProviderServer tells the server what the test provider is, as a user and
// a probe would: its backend and how many slots it has, and what the slot
// affinity check found (none for a check that never ran).
func (a *api) setProviderServer(t *testing.T, backend string, slots int, affinity probe.Verdict) {
	t.Helper()
	a.recordAffinity(t, a.testProvider.ID, affinity)
	// The change of the user's settings is what tells the queue to describe
	// the pools again.
	decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+a.testProvider.ID, map[string]any{
		"backend": backend, "slots": slots,
	}), 200)
}

// recordAffinity stores a probe of the provider whose slot affinity check came
// out as verdict (none for a check that never ran).
func (a *api) recordAffinity(t *testing.T, id string, verdict probe.Verdict) {
	t.Helper()
	caps := probe.Capabilities{Counting: probe.CountNone, CacheMetrics: probe.CacheUnknown, SlotHints: true}
	if verdict != "" {
		caps.SlotAffinity = &probe.SlotAffinity{Verdict: verdict, Model: "test-model", CheckedAt: time.Now().UTC()}
	}
	raw, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("encode capabilities: %v", err)
	}
	row, err := a.store.Provider(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.RecordProbe(t.Context(), id, storeProbe(row, raw)); err != nil {
		t.Fatalf("RecordProbe: %v", err)
	}
}

// slotsOf reads the slot each request named, -1 for a request that named none.
func slotsOf(reqs []provider.Request) []int {
	out := make([]int, len(reqs))
	for i, r := range reqs {
		out[i] = -1
		if r.SlotHint != nil {
			out[i] = *r.SlotHint
		}
	}
	return out
}

func TestSessionsOnALlamaCppServerWhoseSlotsKeepAPrefixKeepTheirSlot(t *testing.T) {
	a := newAPI(t)
	a.setProviderServer(t, "llamacpp", 2, probe.Passed)
	p := a.script(providertest.Text("1"), providertest.Text("2"), providertest.Text("3"), providertest.Text("4"))
	one, two := a.session(t), a.session(t)

	for _, s := range []sessionWire{one, two, one, two} {
		a.postMessage(t, s.ID, "hello", "", 202)
		a.waitIdle(t, s.ID)
	}
	if !a.lastEndpoint().SlotHints {
		t.Error("the client was built without slot hints for a llama.cpp server with two slots that passed the check")
	}
	slots := slotsOf(p.Requests())
	if len(slots) != 4 || slots[0] < 0 || slots[1] < 0 || slots[0] == slots[1] || slots[2] != slots[0] || slots[3] != slots[1] {
		t.Fatalf("slots named = %v, want each session on one slot of its own, the same both times", slots)
	}

	// What the record says is what the request named.
	for i, s := range []sessionWire{one, two} {
		calls := decodeBody[callsWire](t, request(t, a.Server, "GET", "/api/sessions/"+s.ID+"/calls", nil), 200).Calls
		if len(calls) != 2 {
			t.Fatalf("calls of session %d = %d, want 2", i+1, len(calls))
		}
		for _, c := range calls {
			if c.Slot == nil || *c.Slot != slots[i] {
				t.Errorf("session %d recorded slot %v, want %d", i+1, deref(c.Slot), slots[i])
			}
		}
	}
	if pools := a.inference(t).Pools; len(pools) != 1 || len(pools[0].Running) != 0 {
		t.Errorf("pool after the runs = %+v, want it empty", pools)
	}
}

func TestOnlyAServerThatPassedTheCheckIsSentASlot(t *testing.T) {
	for _, tt := range []struct {
		name     string
		backend  string
		slots    int
		affinity probe.Verdict
	}{
		{"vllm that passed", "vllm", 8, probe.Passed},
		{"a hosted api that passed", "openai", 8, probe.Passed},
		{"an unidentified server that passed", "other", 4, probe.Passed},
		{"llama.cpp with one slot that passed", "llamacpp", 1, probe.Passed},
		{"llama.cpp that failed the check", "llamacpp", 2, probe.Failed},
		{"llama.cpp whose check was skipped", "llamacpp", 2, probe.Skipped},
		{"llama.cpp the check never ran on", "llamacpp", 2, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newAPI(t)
			a.setProviderServer(t, tt.backend, tt.slots, tt.affinity)
			p := a.script(providertest.Text("1"), providertest.Text("2"))
			s := a.session(t)
			for range 2 {
				a.postMessage(t, s.ID, "hello", "", 202)
				a.waitIdle(t, s.ID)
			}
			if a.lastEndpoint().SlotHints {
				t.Error("the client was built to send slot hints")
			}
			for i, slot := range slotsOf(p.Requests()) {
				if slot != -1 {
					t.Errorf("request %d named slot %d, want none", i+1, slot)
				}
			}
			for _, c := range decodeBody[callsWire](t, request(t, a.Server, "GET", "/api/sessions/"+s.ID+"/calls", nil), 200).Calls {
				if c.Slot != nil {
					t.Errorf("a call recorded slot %d, want none: no request named one", *c.Slot)
				}
			}
		})
	}
}

func TestATitleNamesNoSlotAndMovesNoSession(t *testing.T) {
	a := newAPI(t)
	a.setProviderServer(t, "llamacpp", 2, probe.Passed)
	titler := providertest.New(providertest.Text("A title"))
	a.assignTitleModel(t, a.addTitleModelOn(t, titler, "llamacpp"))
	for _, p := range decodeBody[providersWire](t, request(t, a.Server, "GET", "/api/providers", nil), 200).Providers {
		if p.Name == "utility" {
			a.recordAffinity(t, p.ID, probe.Passed)
			decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+p.ID, map[string]any{"slots": 2, "pool": "test", "backend": "llamacpp"}), 200)
		}
	}
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"maintenance_idle_seconds": 0,
	}), 200)
	p := a.script(providertest.Text("done"))
	chat := decodeBody[sessionWire](t, request(t, a.Server, "POST", "/api/sessions", map[string]any{"chat": true}), 201)
	a.postMessage(t, chat.ID, "the login page loops forever", "", 202)
	a.waitIdle(t, chat.ID)
	waitFor(t, "the title", func() bool { return a.sessionTitle(t, chat.ID) == "A title" })

	if got := slotsOf(titler.Requests()); len(got) != 1 || got[0] != -1 {
		t.Errorf("the title request named slots %v, want none: its prompt shares nothing with the session's", got)
	}
	if got := slotsOf(p.Requests()); len(got) != 1 || got[0] < 0 {
		t.Errorf("the run named slots %v, want one", got)
	}
}

// slotCheckSteps scripts the three responses the slot affinity check gets: the
// last states how many of the first prompt's tokens it found cached.
func slotCheckSteps(cached int) []providertest.Step {
	reply := func(input, cached int) providertest.Step {
		u := providertest.Usage(input, 3, input+3)
		u.CachedTokens = provider.Reported(cached)
		return providertest.Stream(provider.TextDelta("ok"), provider.Event{Kind: provider.KindUsage, Usage: u}, provider.Done("stop"))
	}
	return []providertest.Step{reply(800, 0), reply(30, 0), reply(830, cached)}
}

func TestAnExplicitProbeRunsTheSlotCheckAndItsVerdictDecidesWhetherSlotsAreNamed(t *testing.T) {
	for _, tt := range []struct {
		name   string
		cached int
		hints  bool
	}{
		{"a server that keeps the prefix", 790, true},
		{"a server that clears the slot", 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newAPI(t)
			p, served := slottedBench(t, a, slices.Concat(conforming(), slotCheckSteps(tt.cached))...)
			probeProvider(t, a, p.ID)

			got := providerNamed(t, a, p.ID)
			c := got.Capabilities.SlotAffinity
			if c == nil || c.Model != "qwen3.8-27b" || c.Build != "b11942-338fce651" || c.PromptTokens != 800 || c.CachedTokens != tt.cached {
				t.Fatalf("slot affinity = %+v, want a result on the model and build with 800 prompt tokens and %d cached", c, tt.cached)
			}
			if tt.hints != (c.Verdict == "passed") || (!tt.hints && !strings.Contains(c.Reason, "--no-cache-idle-slots")) {
				t.Errorf("verdict %q, reason %q; want passed only for the server that kept the prefix, and a reason that names the flag for the other", c.Verdict, c.Reason)
			}
			// The Responses check and the slot check's three requests, the
			// slot check's on the protocol the first chose.
			if served.Calls() != 5 {
				t.Errorf("the probe sent %d requests, want 2 and 3", served.Calls())
			}
			if slots := slotsOf(served.Requests()); !slices.Equal(slots, []int{-1, -1, 0, 1, 0}) {
				t.Errorf("slots named = %v, want none by the Responses check and 0, 1, 0 by the slot check", slots)
			}
			if e := a.lastEndpoint(); e.API != provider.APIResponses {
				t.Errorf("the check's endpoint = %+v, want the Responses API", e)
			}

			// What the verdict decides: a run on this server names its slot
			// or it does not.
			after := providertest.New(providertest.Text("ok"))
			a.serveAt(got.BaseURL, listingProvider{Provider: after, models: []provider.ModelInfo{{ID: "qwen3.8-27b"}}})
			s := a.session(t)
			request(t, a.Server, "POST", "/api/sessions/"+s.ID+"/messages", map[string]any{"text": "hello", "model": "qwen"})
			a.waitIdle(t, s.ID)
			if reqs := after.Requests(); len(reqs) != 1 || (reqs[0].SlotHint != nil) != tt.hints || a.lastEndpoint().SlotHints != tt.hints {
				t.Errorf("the run named slots %v (client built to send them: %v), want a slot named: %v", slotsOf(reqs), a.lastEndpoint().SlotHints, tt.hints)
			}
		})
	}
}

func TestStartAsksTheSlotCheckOnlyWhenItIsStale(t *testing.T) {
	a := newAPI(t)
	p, served := slottedBench(t, a, slices.Concat(conforming(), slotCheckSteps(790), conforming(), slotCheckSteps(790))...)
	a.ProbeProviders(t.Context())
	if served.Calls() != 5 {
		t.Fatalf("first start sent %d requests, want 2 and 3", served.Calls())
	}
	a.ProbeProviders(t.Context())
	if served.Calls() != 5 {
		t.Errorf("a later start sent %d requests, want none new: the build and the model are the ones the checks ran on", served.Calls())
	}

	// The server was upgraded: the checks were made against another build.
	row, err := a.store.Provider(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var caps map[string]any
	if err := json.Unmarshal(row.Capabilities, &caps); err != nil {
		t.Fatal(err)
	}
	caps["slot_affinity"].(map[string]any)["build"] = "b11000"
	changed, _ := json.Marshal(caps)
	if _, err := a.store.RecordProbe(t.Context(), p.ID, storeProbe(row, changed)); err != nil {
		t.Fatal(err)
	}
	a.ProbeProviders(t.Context())
	if served.Calls() != 8 {
		t.Errorf("a start after an upgrade sent %d requests in all, want the 3 of the slot check again", served.Calls())
	}
}

func TestAServerWithOneSlotIsNotAskedTheSlotQuestion(t *testing.T) {
	a := newAPI(t)
	p, served := responsesBench(t, a, conforming()...)
	probeProvider(t, a, p.ID)
	if got := providerNamed(t, a, p.ID); got.Capabilities.SlotAffinity != nil || served.Calls() != 2 {
		t.Errorf("slot affinity %+v after %d requests, want none and the Responses check's 2", got.Capabilities.SlotAffinity, served.Calls())
	}
}

func TestAProviderWithNoModelIsNotAskedTheSlotQuestion(t *testing.T) {
	a := newAPI(t)
	p := benchProvider(t, a, "bench")
	probeProvider(t, a, p.ID)
	c := providerNamed(t, a, p.ID).Capabilities.SlotAffinity
	if c == nil || c.Verdict != "skipped" || c.Reason == "" {
		t.Errorf("slot affinity = %+v, want a skipped check with its reason", c)
	}
}
