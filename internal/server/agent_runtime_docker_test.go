//go:build docker

package server_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/session"
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
	if err := a.store.SetInstructions(ctx, store.GlobalInstructions{AboutMe: "first snapshot"}); err != nil {
		t.Fatal(err)
	}
	scripted := a.script(providertest.Text("reply"))
	a.postMessage(t, home.ID, "hello", "", 202)
	a.waitIdle(t, home.ID)
	reqs := scripted.Requests()
	if len(reqs) != 1 || !strings.HasPrefix(reqs[0].System, "agent rules\n\n") || len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "web_search" {
		t.Fatalf("requests = %+v", reqs)
	}

	if !strings.Contains(reqs[0].System, "first snapshot") {
		t.Fatalf("missing snapshot: %q", reqs[0].System)
	}
	path, err := a.store.SessionPath(ctx, home.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := session.Preamble(path)
	if err != nil || !strings.Contains(snapshot, "first snapshot") {
		t.Fatalf("stored snapshot = %q, %v", snapshot, err)
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
	decodeBody[map[string]any](t, request(t, a.Server, "PUT", "/api/agents/eika", map[string]any{"prompt": ""}), 200)
	scripted = a.script(providertest.Text("empty prompt reply"))
	a.postMessage(t, home.ID, "again", "", 202)
	a.waitIdle(t, home.ID)
	if got := scripted.Requests()[0].System; !strings.HasPrefix(got, "About me\nfirst snapshot") {
		t.Fatalf("empty override = %q", got)
	}
	if rec := request(t, a.Server, "DELETE", "/api/agents/eika", nil); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	scripted = a.script(providertest.Text("builtin reply"))
	a.postMessage(t, home.ID, "reset", "", 202)
	a.waitIdle(t, home.ID)
	builtin, _ := agentdef.Lookup("eika")
	if got := scripted.Requests()[0].System; !strings.HasPrefix(got, strings.TrimSpace(builtin.Prompt)+"\n\n") {
		t.Fatalf("reset prompt = %q", got)
	}
}

func TestSessionsWithAnUnknownAgentStayListedAndRefuseRuns(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	cx := a.home(t)
	ghost, err := a.store.CreateSession(ctx, store.Session{ComputerID: cx.ID, Agent: "ghost", Tools: []string{"bash"}})
	if err != nil {
		t.Fatal(err)
	}
	ghostChat, err := a.store.CreateSession(ctx, store.Session{Agent: "ghost", Title: "ghost chat"})
	if err != nil {
		t.Fatal(err)
	}
	ordinary := a.newSession(t, cx.ID)

	for _, route := range []string{"/api/sessions", "/api/sessions?chats=true"} {
		list := decodeBody[sessionsWire](t, request(t, a.Server, "GET", route, nil), 200)
		var ids []string
		for _, s := range list.Sessions {
			ids = append(ids, s.ID)
		}
		want := ghost.ID
		if strings.Contains(route, "chats") {
			want = ghostChat.ID
		} else if !slices.Contains(ids, ordinary.ID) {
			t.Errorf("%s = %v, lacks the ordinary session %s", route, ids, ordinary.ID)
		}
		if !slices.Contains(ids, want) {
			t.Errorf("%s = %v, lacks the session %s with an unknown agent", route, ids, want)
		}
	}
	got := decodeBody[struct {
		Session struct {
			Agent string   `json:"agent"`
			Tools []string `json:"tools"`
		} `json:"session"`
	}](t, request(t, a.Server, "GET", "/api/sessions/"+ghost.ID, nil), 200)
	if got.Session.Agent != "ghost" || len(got.Session.Tools) != 0 {
		t.Errorf("session = %+v, want agent ghost with no tools", got.Session)
	}
	a.postMessage(t, ghost.ID, "hello", "", 400)
}

// TestAgentRunsHoldAnExecutorOnlyWhenTheirToolsNeedOne starts a run in a
// stopped computer. Only a run that holds an executor starts the computer.
func TestAgentRunsHoldAnExecutorOnlyWhenTheirToolsNeedOne(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent string
		want  string
	}{
		{"worker", "worker", "running"},
		{"eika", "eika", "stopped"},
		{"researcher", "researcher", "stopped"},
		{"profile session", "", "running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newAPI(t)
			cx := a.seedComputer(t)
			a.stopIdle(t)
			if got := a.computerState(t, cx.ID); got != "stopped" {
				t.Fatalf("computer before the run = %q, want stopped", got)
			}
			sess, err := a.store.CreateSession(t.Context(), store.Session{ComputerID: cx.ID, Agent: tc.agent})
			if err != nil {
				t.Fatal(err)
			}
			a.assignClasses(t, map[string]any{"main": "test-model", "fast": "test-model"})
			a.script(providertest.Text("done"))
			a.postMessage(t, sess.ID, "hello", "", 202)
			a.waitIdle(t, sess.ID)
			if got := a.computerState(t, cx.ID); got != tc.want {
				t.Errorf("computer after the run = %q, want %q", got, tc.want)
			}
		})
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
	a.Server = server.New(testConfig(), testLogger(), a.deps, server.Options{})
	t.Cleanup(a.Close)
	second := a.script(providertest.Text("second response"))
	a.postMessage(t, home.ID, "after restart", "", 202)
	a.waitIdle(t, home.ID)
	if got := second.Requests()[0].System; got != before {
		t.Fatalf("prefix after restart = %q, want %q", got, before)
	}
}
