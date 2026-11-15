//go:build docker

package server_test

import (
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

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
	if rec := request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"compaction": map[string]any{"keep_recent_tokens": 1024},
	}); rec.Code != 200 {
		t.Fatalf("set compaction = %d: %s", rec.Code, rec.Body.String())
	}
	eika, _ := agentdef.Lookup("eika")
	p := a.script(providertest.Text("first answer"), providertest.Text("second answer"), providertest.Text("CONVERSATION-1"),
		providertest.Text("third answer"), providertest.Text("CONVERSATION-2"))
	for _, text := range []string{"one", "two"} {
		a.postMessage(t, home.ID, text+" "+strings.Repeat("x", 8000), "", 202)
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
	a.postMessage(t, home.ID, "three "+strings.Repeat("x", 8000), "", 202)
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

// A child can report after a compaction has read the board and before it
// stores its entry. The next board counts from the read, not from the entry,
// so the report that no board showed is listed.
func TestHomeBoardListsAReportMadeWhileTheLastCompactionWasStored(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	home, err := a.store.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.assignClasses(t, map[string]any{"main": "test-model"})
	plumber, err := a.store.CreateSession(ctx, store.Session{Kind: store.SessionSubagent, Name: "plumber", Agent: "worker",
		ParentSessionID: home.ID, State: store.SessionRunning})
	if err != nil {
		t.Fatal(err)
	}
	read := time.Now().UTC()
	if _, err := a.store.SetSubagentState(ctx, plumber.ID, store.SessionIdle, ""); err != nil {
		t.Fatal(err)
	}
	reported, err := a.store.SetSubagentResult(ctx, plumber.ID, "Booked the second quote.")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := session.CompactionEntry(agent.Compaction{Summary: "[Subagents] as of then\n- plumber (worker): running\n\n[Conversation]\nOLD", BoardAsOf: &read})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := a.store.AppendEntry(ctx, home.ID, entry)
	if err != nil {
		t.Fatal(err)
	}
	if !reported.LastActiveAt.Before(stored.CreatedAt) || !reported.LastActiveAt.After(read) {
		t.Fatalf("report at %v, want between the board read %v and the entry %v", reported.LastActiveAt, read, stored.CreatedAt)
	}
	if rec := request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"compaction": map[string]any{"keep_recent_tokens": 1024},
	}); rec.Code != 200 {
		t.Fatalf("set compaction = %d: %s", rec.Code, rec.Body.String())
	}
	a.script(providertest.Text("first answer"), providertest.Text("second answer"), providertest.Text("CONVERSATION"))
	for _, text := range []string{"one", "two"} {
		a.postMessage(t, home.ID, text+" "+strings.Repeat("x", 8000), "", 202)
		a.waitIdle(t, home.ID)
	}
	decodeBody[runWire](t, request(t, a.Server, "POST", "/api/sessions/"+home.ID+"/compact", map[string]any{}), 202)
	if state := a.waitIdle(t, home.ID); state.Run == nil || state.Run.State != "done" {
		t.Fatalf("compaction run = %+v", state.Run)
	}
	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+home.ID+"/path", nil), 200)
	if want := "\n- plumber (worker): idle, last active "; len(path.Messages) == 0 || !strings.Contains(path.Messages[0].Content, want) ||
		!strings.Contains(path.Messages[0].Content, "last report: Booked the second quote.") {
		t.Fatalf("summary = %q, want the plumber's report on the board", path.Messages[0].Content)
	}
	entries, err := a.store.SessionPath(ctx, home.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := entries[len(entries)-1]
	c, err := session.CompactionOf(last)
	if last.Kind != store.KindCompaction || err != nil || c.BoardAsOf == nil || c.BoardAsOf.Before(stored.CreatedAt) {
		t.Errorf("new compaction = %+v, %v, want the time its board was read", c, err)
	}
}
