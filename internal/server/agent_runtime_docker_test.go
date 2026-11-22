//go:build docker

package server_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

func TestAgentSessionRunsAndPreviewsUseTheDefinition(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	home, err := a.store.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	decodeBody[map[string]any](t, request(t, a.Server, "PUT", "/api/agents/eika", map[string]any{"class": "deep", "prompt": "agent rules"}), 200)
	a.assignClasses(t, map[string]any{"main": "test-model"})
	wantTools := []string{"history_read", "history_search", "memory_forget", "memory_read", "memory_search", "memory_write", "project_create", "project_list", "project_open", "project_update",
		"subagent_cancel", "subagent_message", "subagent_start", "subagent_status", "web_search"}
	preview := decodeBody[struct {
		Sections []struct {
			Text string `json:"text"`
		} `json:"sections"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}](t, request(t, a.Server, "GET", "/api/sessions/"+home.ID+"/context", nil), 200)
	previewNames := make([]string, 0, len(preview.Tools))
	for _, t := range preview.Tools {
		previewNames = append(previewNames, t.Name)
	}
	if !slices.Equal(previewNames, wantTools) {
		t.Fatalf("preview tools = %+v", preview.Tools)
	}
	if err := a.store.SetInstructions(ctx, store.GlobalInstructions{AboutMe: "first snapshot"}); err != nil {
		t.Fatal(err)
	}
	scripted := a.script(providertest.Text("reply"))
	a.postMessage(t, home.ID, "hello", "", 202)
	a.waitIdle(t, home.ID)
	reqs := scripted.Requests()
	if len(reqs) != 1 || !strings.HasPrefix(reqs[0].System, "agent rules\n\n") {
		t.Fatalf("requests = %+v", reqs)
	}

	var requestNames []string
	for _, t := range reqs[0].Tools {
		requestNames = append(requestNames, t.Name)
	}
	if !slices.Equal(requestNames, wantTools) {
		t.Fatalf("request tools = %v, want %v", requestNames, wantTools)
	}

	if !strings.Contains(reqs[0].System, "first snapshot") {
		t.Fatalf("missing snapshot: %q", reqs[0].System)
	}
	path, err := a.store.SessionPath(ctx, home.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := session.SnapshotOf(path)
	if err != nil || !strings.Contains(snapshot.Text, "first snapshot") {
		t.Fatalf("stored snapshot = %q, %v", snapshot.Text, err)
	}
	if err := a.store.SetInstructions(ctx, store.GlobalInstructions{AboutMe: "changed snapshot"}); err != nil {
		t.Fatal(err)
	}
	scripted = a.script(providertest.Text("again"))
	a.postMessage(t, home.ID, "again", "", 202)
	a.waitIdle(t, home.ID)
	if got := scripted.Requests()[0].System; got != reqs[0].System {
		t.Fatalf("snapshot changed: %q", got)
	}
	calls, err := a.store.ModelCalls(ctx, home.ID)
	if err != nil || len(calls) != 2 {
		t.Fatalf("recorded calls = %+v, %v", calls, err)
	}
	if calls[0].Agent != "eika" || calls[0].Class != "deep" {
		t.Fatalf("attribution = %+v", calls[0])
	}
	a.postMessage(t, home.ID, "override model", "test-model", 400)
	for _, route := range []string{"context", "prediction"} {
		rec := request(t, a.Server, "GET", "/api/sessions/"+home.ID+"/"+route+"?model=test-model", nil)
		if rec.Code != 400 {
			t.Fatalf("%s = %d", route, rec.Code)
		}
	}
}

// compactHome compacts a session through the API and waits for it.
func compactHome(t *testing.T, a *api, id string) {
	t.Helper()
	decodeBody[runWire](t, request(t, a.Server, "POST", "/api/sessions/"+id+"/compact", map[string]any{}), 202)
	if state := a.waitIdle(t, id); state.Run == nil || state.Run.State != "done" {
		t.Fatalf("compaction run = %+v", state.Run)
	}
}

// requestTools names the tools a request offered.
func requestTools(req provider.Request) []string {
	var names []string
	for _, def := range req.Tools {
		names = append(names, def.Name)
	}
	return names
}

// A snapshot from before a tool joined Eika's definition keeps the list it
// pinned, request after request, until a compaction
// rebuilds it with the tool (runtime.md 4).
func TestHomeToolListIsRebuiltOnlyAtCompaction(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	home, err := a.store.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.assignClasses(t, map[string]any{"main": "test-model"})
	eika, _ := agentdef.Lookup("eika")
	// Pinned before the memory tools joined the definition.
	older := []string{"project_list", "web_search"}
	entry, err := session.PreambleEntry("Time zone\nUTC\n\nLocale\nunspecified", &eika.Prompt, older)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AppendEntry(ctx, home.ID, entry); err != nil {
		t.Fatal(err)
	}
	scripted := a.script(providertest.Text("big reply"), providertest.Text("small reply"), providertest.Text("SUMMARY"),
		providertest.Text("next reply"), providertest.Text("after reply"))
	a.postMessage(t, home.ID, "big "+strings.Repeat("x", 24000), "", 202)
	a.waitIdle(t, home.ID)
	a.postMessage(t, home.ID, "small", "", 202)
	a.waitIdle(t, home.ID)
	for i, req := range scripted.Requests() {
		if got := requestTools(req); !slices.Equal(got, older) {
			t.Fatalf("request %d tools = %v, want the pinned %v", i, got, older)
		}
	}
	compactHome(t, a, home.ID)
	if got := requestTools(scripted.Requests()[2]); !slices.Equal(got, older) {
		t.Errorf("summary request tools = %v, want the prefix it shares", got)
	}

	// Each run is built from the store, as after a restart: both read the
	// replacement snapshot, which lists the definition's tools now.
	for _, text := range []string{"next", "after"} {
		a.postMessage(t, home.ID, text, "", 202)
		a.waitIdle(t, home.ID)
	}
	reqs := scripted.Requests()
	for _, req := range reqs[3:] {
		if got := requestTools(req); !slices.Equal(got, eika.Tools) {
			t.Errorf("tools after compaction = %v, want %v", got, eika.Tools)
		}
	}
	if reqs[3].System != reqs[4].System || !reflect.DeepEqual(reqs[3].Tools, reqs[4].Tools) {
		t.Error("the replacement snapshot changed between runs")
	}
	path, err := a.store.SessionPath(ctx, home.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := session.SnapshotOf(path)
	if err != nil || snap.Prompt == nil || *snap.Prompt != eika.Prompt || !slices.Equal(snap.Tools, eika.Tools) {
		t.Fatalf("stored snapshot = %+v, %v", snap, err)
	}
}

func TestAgentPreambleSurvivesServerRestart(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	home, err := a.store.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.assignClasses(t, map[string]any{"main": "test-model"})
	if err := a.store.SetInstructions(ctx, store.GlobalInstructions{AboutMe: "original snapshot\nexact bytes"}); err != nil {
		t.Fatal(err)
	}
	first := a.script(providertest.Text("first response"))
	a.postMessage(t, home.ID, "hello", "", 202)
	a.waitIdle(t, home.ID)
	before := first.Requests()[0].System
	if !strings.Contains(before, "original snapshot\nexact bytes") {
		t.Fatalf("missing snapshot: %q", before)
	}
	if err := a.store.SetInstructions(ctx, store.GlobalInstructions{AboutMe: "changed after snapshot"}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a.Server = server.New(a.cfg, testLogger(), a.deps, server.Options{})
	t.Cleanup(a.Close)
	second := a.script(providertest.Text("second response"))
	a.postMessage(t, home.ID, "after restart", "", 202)
	a.waitIdle(t, home.ID)
	if got := second.Requests()[0].System; got != before {
		t.Fatalf("prefix after restart = %q, want %q", got, before)
	}
}
