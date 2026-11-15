//go:build docker

package server_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
)

func TestAgentSessionRunsAndPreviewsUseTheDefinition(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	// The default profile must not supply an agent's prompt or tools.
	profiles, err := a.store.Profiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := profiles[0]
	p.ChatPrompt, p.Instructions = new("legacy prompt"), new("legacy rules")
	p.Tools = []string{"ask_user"}
	if _, err := a.store.UpdateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	home, err := a.store.CreateSession(ctx, store.Session{Kind: store.SessionHome, Name: "home", Agent: "eika", ProfileID: p.ID, Tools: []string{"ask_user"}})
	if err != nil {
		t.Fatal(err)
	}
	decodeBody[map[string]any](t, request(t, a.Server, "PUT", "/api/agents/eika", map[string]any{"class": "deep", "prompt": "agent rules"}), 200)
	a.assignClasses(t, map[string]any{"main": "test-model"})
	configPath := "/api/sessions/" + home.ID + "/configuration"
	cfg := decodeBody[struct {
		Resolved struct {
			ProfileID    string   `json:"profile_id"`
			Model        string   `json:"model"`
			ChatPrompt   string   `json:"chat_prompt"`
			Instructions string   `json:"instructions"`
			Tools        []string `json:"tools"`
		} `json:"resolved"`
	}](t, request(t, a.Server, "GET", configPath, nil), 200)
	if cfg.Resolved.ProfileID != "" || cfg.Resolved.Model != "test-model" || cfg.Resolved.ChatPrompt != "agent rules" || cfg.Resolved.Instructions != "" || !slices.Equal(cfg.Resolved.Tools, []string{"web_search"}) {
		t.Fatalf("configuration = %+v", cfg)
	}
	preview := decodeBody[struct {
		Sections []struct {
			Text string `json:"text"`
		} `json:"sections"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}](t, request(t, a.Server, "GET", "/api/sessions/"+home.ID+"/context", nil), 200)
	if len(preview.Tools) != 1 || preview.Tools[0].Name != "web_search" {
		t.Fatalf("preview tools = %+v", preview.Tools)
	}
	scripted := a.script(providertest.Text("reply"))
	a.postMessage(t, home.ID, "hello", "", 202)
	a.waitIdle(t, home.ID)
	reqs := scripted.Requests()
	if len(reqs) != 1 || reqs[0].System != "agent rules" || len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "web_search" {
		t.Fatalf("requests = %+v", reqs)
	}
	calls, err := a.store.ModelCalls(ctx, home.ID)
	if err != nil || len(calls) != 1 {
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
	decodeBody[map[string]any](t, request(t, a.Server, "PUT", "/api/agents/eika", map[string]any{"prompt": ""}), 200)
	scripted = a.script(providertest.Text("empty prompt reply"))
	a.postMessage(t, home.ID, "again", "", 202)
	a.waitIdle(t, home.ID)
	if got := scripted.Requests()[0].System; got != "" {
		t.Fatalf("empty override = %q", got)
	}
	if rec := request(t, a.Server, "DELETE", "/api/agents/eika", nil); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	scripted = a.script(providertest.Text("builtin reply"))
	a.postMessage(t, home.ID, "reset", "", 202)
	a.waitIdle(t, home.ID)
	builtin, _ := agentdef.Lookup("eika")
	if got := scripted.Requests()[0].System; got != strings.TrimSpace(builtin.Prompt) {
		t.Fatalf("reset prompt = %q", got)
	}
}
