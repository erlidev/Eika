//go:build docker

package server_test

import (
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
)

// homeTurn is the length of a message that makes a turn of about 4,000
// tokens: home keeps 15% of the 32,768-token window, so a compaction keeps
// the last of two such turns.
const homeTurn = 16000

// lastContent is the content of the last message a request sent.
func lastContent(req provider.Request) string {
	return req.Messages[len(req.Messages)-1].Content
}

func TestHomeCompactionWritesTheBoardAndRollsTheConversation(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	home, err := a.store.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.assignClasses(t, map[string]any{"main": "test-model"})
	kitchen, err := a.store.CreateProject(ctx, "kitchen", store.ProjectFields{Name: "Kitchen", Status: "active", Directory: "/home/eika/projects/kitchen"})
	if err != nil {
		t.Fatal(err)
	}
	child := func(name, agent, projectID string) store.Session {
		t.Helper()
		c, err := a.store.CreateSession(ctx, store.Session{Kind: store.SessionSubagent, Name: name, Agent: agent,
			ProjectID: projectID, ParentSessionID: home.ID, State: store.SessionIdle})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	plumber, quiet := child("plumber", "worker", kitchen.ID), child("quiet", "researcher", "")
	if _, err := a.store.SetSubagentResult(ctx, quiet.ID, "Found it.\nDetails."); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.SetSubagentState(ctx, plumber.ID, store.SessionWaiting, "Question ID: q1\nWhich quote?"); err != nil {
		t.Fatal(err)
	}
	eika, _ := agentdef.Lookup("eika")
	p := a.script(providertest.Text("first answer"), providertest.Text("second answer"), providertest.Text("CONVERSATION-1"),
		providertest.Text("third answer"), providertest.Text("CONVERSATION-2"))
	for _, text := range []string{"one", "two"} {
		a.postMessage(t, home.ID, text+" "+strings.Repeat("x", homeTurn), "", 202)
		a.waitIdle(t, home.ID)
	}
	compact := func() string {
		t.Helper()
		decodeBody[runWire](t, request(t, a.Server, "POST", "/api/sessions/"+home.ID+"/compact", map[string]any{}), 202)
		if state := a.waitIdle(t, home.ID); state.Run == nil || state.Run.State != "done" {
			t.Fatalf("compaction run = %+v", state.Run)
		}
		path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+home.ID+"/path", nil), 200)
		if len(path.Messages) == 0 || !path.Messages[0].Summary {
			t.Fatalf("path messages = %+v, want a summary first", path.Messages)
		}
		return path.Messages[0].Content
	}

	first := compact()
	if got := lastContent(p.Requests()[2]); got != eika.Compaction.Summary {
		t.Errorf("summary prompt = %.80q, want Eika's", got)
	}
	for _, want := range []string{
		"<summary>\n[Subagents] as of ",
		"\n- plumber (worker, project kitchen): waiting, last active ",
		"\n  waiting on: Question ID: q1\n    Which quote?\n",
		"\n- quiet (researcher): idle, last active ",
		"\n  last report: Found it.\n\n[Conversation]\nCONVERSATION-1\n</summary>",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("first summary = %q, want it to contain %q", first, want)
		}
	}

	// After the first compaction the plumber reports; quiet does nothing, so
	// the next board only counts it.
	if _, err := a.store.SetSubagentState(ctx, plumber.ID, store.SessionIdle, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.SetSubagentResult(ctx, plumber.ID, "Booked the second quote."); err != nil {
		t.Fatal(err)
	}
	a.postMessage(t, home.ID, "three "+strings.Repeat("x", homeTurn), "", 202)
	a.waitIdle(t, home.ID)
	second := compact()
	update := p.Requests()[4]
	if got := lastContent(update); got != eika.Compaction.Update {
		t.Errorf("update prompt = %.80q, want Eika's", got)
	}
	if got := update.Messages[0].Content; got != first {
		t.Errorf("update request begins with %q, want the first summary", got)
	}
	for _, want := range []string{
		"\n- plumber (worker, project kitchen): idle, last active ",
		"\n  last report: Booked the second quote.\n1 other subagent has had no activity since the last compaction; subagent_status lists it.\n\n[Conversation]\nCONVERSATION-2\n</summary>",
	} {
		if !strings.Contains(second, want) {
			t.Errorf("second summary = %q, want it to contain %q", second, want)
		}
	}
	if strings.Contains(second, "quiet") || strings.Contains(second, "waiting on") {
		t.Errorf("second summary = %q, want quiet only counted and no stale wait", second)
	}
}

func TestSteeringDuringIdleCompactionStartsAUserTurn(t *testing.T) {
	a := newAPI(t)
	home, err := a.store.EnsureHomeSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	a.assignClasses(t, map[string]any{"main": "test-model"})
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{"maintenance_idle_seconds": 0}), 200)
	a.script(providertest.Text("first"), providertest.Text("second"))
	for _, text := range []string{"one", "two"} {
		a.postMessage(t, home.ID, text+" "+strings.Repeat("x", homeTurn), "", 202)
		a.waitIdle(t, home.ID)
	}
	held := newHeldCall(1, providertest.New(providertest.Text("user answer")))
	a.use(held)
	if err := a.StartIdleCompactionForTest(t.Context(), home.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-held.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("idle summary did not start")
	}
	a.postMessage(t, home.ID, "continue", "steer", 202)
	state := a.waitIdle(t, home.ID)
	if state.Run == nil || state.Run.State != "done" {
		t.Fatalf("user turn: %+v", state.Run)
	}
	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+home.ID+"/path", nil), 200)
	if len(path.Messages) < 2 || path.Messages[len(path.Messages)-2].Content != "continue" || path.Messages[len(path.Messages)-1].Content != "user answer" {
		t.Fatalf("user turn did not get its answer: %+v", path.Messages)
	}
	entries, err := a.store.Entries(t.Context(), home.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Kind == store.KindCompaction {
			t.Fatal("cancelled idle summary changed stored context")
		}
	}
}
