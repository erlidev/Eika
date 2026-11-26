//go:build docker

package server_test

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
)

func TestRoutineToolAndApprovalAPI(t *testing.T) {
	a := newAPI(t)
	home := a.home(t)
	if err := a.BindAgentToolsForTest(a.host); err != nil {
		t.Fatal(err)
	}
	sess, err := a.store.CreateSession(t.Context(), store.Session{Kind: store.SessionHome, Name: "home", Agent: "eika", ComputerID: home.ID})
	if err != nil {
		t.Fatal(err)
	}
	a.assignClasses(t, map[string]any{"main": "test-model"})
	a.script(providertest.Calls("", providertest.Call("create", "routine_create", map[string]any{"name": "Brief", "schedule_kind": "cron", "schedule_expr": "0 8 * * mon-fri", "timezone": "UTC", "agent": "eika", "prompt": "Prepare a brief"})), providertest.Text("done"))
	a.postMessage(t, sess.ID, "schedule it", "", 202)
	a.waitIdle(t, sess.ID)
	type routine struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		Enabled       bool     `json:"enabled"`
		ApprovedAt    *string  `json:"approved_at"`
		ScheduleWords string   `json:"schedule_words"`
		NextFive      []string `json:"next_five"`
	}
	listed := decodeBody[struct {
		Routines []routine `json:"routines"`
	}](t, request(t, a.Server, "GET", "/api/routines", nil), 200)
	var got routine
	for _, r := range listed.Routines {
		if r.Name == "Brief" {
			got = r
		}
	}
	if got.ID == "" || got.Enabled || got.ApprovedAt != nil || got.ScheduleWords == "" || len(got.NextFive) != 5 {
		t.Fatalf("created = %+v", got)
	}
	approved := decodeBody[routine](t, request(t, a.Server, "POST", "/api/routines/"+got.ID+"/approve", nil), 200)
	if !approved.Enabled || approved.ApprovedAt == nil {
		t.Fatalf("approved = %+v", approved)
	}
	a.script(providertest.Calls("", providertest.Call("update", "routine_update", map[string]any{"id": got.ID, "prompt": "New brief"})), providertest.Calls("", providertest.Call("wake", "wake_me", map[string]any{"in": "1h", "note": "check"})), providertest.Text("done"))
	a.postMessage(t, sess.ID, "update and wake", "", 202)
	a.waitIdle(t, sess.ID)
	edited := decodeBody[routine](t, request(t, a.Server, "GET", "/api/routines/"+got.ID, nil), 200)
	if edited.Enabled || edited.ApprovedAt != nil {
		t.Fatalf("agent edit = %+v", edited)
	}
	wakes := decodeBody[struct {
		Wakeups []struct {
			ID string `json:"id"`
		} `json:"wakeups"`
	}](t, request(t, a.Server, "GET", "/api/wakeups", nil), 200)
	if len(wakes.Wakeups) != 1 {
		t.Fatalf("wakeups = %+v", wakes)
	}
	if rec := request(t, a.Server, "DELETE", "/api/wakeups/"+wakes.Wakeups[0].ID, nil); rec.Code != 204 {
		t.Fatalf("cancel wakeup: %d", rec.Code)
	}
	updated := decodeBody[routine](t, request(t, a.Server, "PATCH", "/api/routines/"+got.ID, map[string]any{"name": "Updated"}), 200)
	if updated.Name != "Updated" {
		t.Fatalf("updated = %+v", updated)
	}
	if rec := request(t, a.Server, "DELETE", "/api/routines/"+got.ID, nil); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRoutineSessionCannotSchedule(t *testing.T) {
	a := newAPI(t)
	if err := a.BindAgentToolsForTest(a.host); err != nil {
		t.Fatal(err)
	}
	sess, err := a.store.CreateSession(t.Context(), store.Session{Kind: store.SessionRoutine, Name: "test", Agent: "eika"})
	if err != nil {
		t.Fatal(err)
	}
	a.assignClasses(t, map[string]any{"main": "test-model"})
	p := a.script(providertest.Calls("", providertest.Call("create", "routine_create", map[string]any{"name": "Brief", "schedule_kind": "cron", "schedule_expr": "0 8 * * *", "timezone": "UTC", "agent": "eika", "prompt": "Brief"}), providertest.Call("wake", "wake_me", map[string]any{"in": "1h", "note": "check"})), providertest.Text("done"))
	a.postMessage(t, sess.ID, "schedule", "", 202)
	a.waitIdle(t, sess.ID)
	requests := p.Requests()
	if len(requests) < 2 {
		t.Fatal("tool calls did not complete")
	}
	count := 0
	for _, m := range requests[len(requests)-1].Messages {
		if m.Role == provider.RoleTool && strings.Contains(m.Content, "routine sessions cannot") {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("guard returned %d errors", count)
	}
}
