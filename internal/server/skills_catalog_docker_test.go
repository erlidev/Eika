//go:build docker

package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/skill"
	"github.com/erlidev/eika/internal/store"
)

// countedModel puts the main class on a model whose endpoint tokenises text
// (one token per word), so the catalog can be counted exactly.
func countedModel(t *testing.T, a *api) {
	t.Helper()
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		tokens := make([]int, len(strings.Fields(body.Content)))
		_ = json.NewEncoder(w).Encode(map[string]any{"tokens": tokens})
	}))
	t.Cleanup(tok.Close)
	ctx := t.Context()
	prov, err := a.store.CreateProvider(ctx, store.Provider{Name: "counted", Kind: "openai", BaseURL: tok.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.RecordProbe(ctx, prov.ID, store.ProbeResult{Backend: string(probe.LlamaCpp), Slots: 1, KV: "unknown", Capabilities: json.RawMessage(`{"counting":"text"}`), At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.CreateModel(ctx, store.Model{ProviderID: prov.ID, Name: "counted", Model: "counted", ContextWindow: 32768, MaxOutput: 1024}); err != nil {
		t.Fatal(err)
	}
	a.assignClasses(t, map[string]any{"main": "counted"})
}

func enableSkill(t *testing.T, a *api, name, description string, enabled bool) {
	t.Helper()
	ctx := t.Context()
	rec := skill.Record{Skill: skill.Skill{Name: name, Description: description, Body: "body"}, SHA: "sha-" + name}
	if err := a.store.UpsertSkill(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetSkillEnabled(ctx, name, enabled); err != nil {
		t.Fatal(err)
	}
}

// The catalog is part of the frozen prefix: a skill enabled after the
// snapshot, or disabled, reaches the session at its next compaction, and a
// restart keeps the stored bytes.
func TestSkillCatalogIsFrozenUntilCompaction(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	home, err := a.store.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	countedModel(t, a)
	enableSkill(t, a, "trip-planner", "Plan trips.\nUse for travel.", true)
	enableSkill(t, a, "pdf-forms", "Fill PDF forms.", true)
	enableSkill(t, a, "off-skill", "Not enabled.", false)
	first := a.script(providertest.Text("one"))
	a.postMessage(t, home.ID, "hello", "", 202)
	a.waitIdle(t, home.ID)
	before := first.Requests()[0].System
	want := "## Skills (skill_use <name> to load one)\n- pdf-forms: Fill PDF forms.\n- trip-planner: Plan trips. Use for travel."
	if !strings.Contains(before, want) || strings.Contains(before, "off-skill") {
		t.Fatalf("system = %q, want the catalog sorted, enabled only", before)
	}
	enableSkill(t, a, "late-skill", "Installed late.", true)
	if err := a.store.SetSkillEnabled(ctx, "pdf-forms", false); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a.Server = server.New(a.cfg, testLogger(), a.deps, server.Options{})
	t.Cleanup(a.Close)
	second := a.script(providertest.Text("two"), providertest.Text("big reply"), providertest.Text("SUMMARY"), providertest.Text("three"))
	a.postMessage(t, home.ID, "after restart", "", 202)
	a.waitIdle(t, home.ID)
	if got := second.Requests()[0].System; got != before {
		t.Fatalf("prefix after restart = %q, want %q", got, before)
	}
	a.postMessage(t, home.ID, "big "+strings.Repeat("x", 24000), "", 202)
	a.waitIdle(t, home.ID)
	compactHome(t, a, home.ID)
	a.postMessage(t, home.ID, "after compaction", "", 202)
	a.waitIdle(t, home.ID)
	reqs := second.Requests()
	after := reqs[len(reqs)-1].System
	if !strings.Contains(after, "- late-skill: Installed late.\n- trip-planner:") || strings.Contains(after, "pdf-forms") {
		t.Fatalf("system after compaction = %q", after)
	}
}
