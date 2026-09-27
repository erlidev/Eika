//go:build docker

package server_test

import (
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider/providertest"
)

// workspaceState reads a workspace's recorded state.
func (a *api) workspaceState(t *testing.T, id string) string {
	t.Helper()
	return decodeBody[workspaceWire](t, request(t, a.Server, "GET", "/api/workspaces/"+id, nil), 200).State
}

func TestIdleWorkspacesStop(t *testing.T) {
	t.Run("after the default timeout without a run", func(t *testing.T) {
		a := newAPI(t)
		project, _ := a.newProject(t, "idle")
		ws := a.newWorkspace(t, project.ID)
		start := time.Now()

		a.SweepIdle(t.Context(), start)
		a.SweepIdle(t.Context(), start.Add(14*time.Minute))
		if got := a.workspaceState(t, ws.ID); got != "running" {
			t.Fatalf("state after 14 idle minutes = %q, want running", got)
		}
		a.SweepIdle(t.Context(), start.Add(15*time.Minute))
		if got := a.workspaceState(t, ws.ID); got != "stopped" {
			t.Errorf("state after 15 idle minutes = %q, want stopped", got)
		}
	})

	t.Run("counting from the end of the last run", func(t *testing.T) {
		a := newAPI(t)
		project, _ := a.newProject(t, "busy")
		ws := a.newWorkspace(t, project.ID)
		sess := a.newSession(t, ws.ID)
		// First seen ten minutes ago, so only the run keeps it up below.
		a.SweepIdle(t.Context(), time.Now().Add(-10*time.Minute))

		a.script(providertest.Text("done"))
		a.postMessage(t, sess.ID, "hello", "", 202)
		a.waitIdle(t, sess.ID)
		finished := time.Now()

		a.SweepIdle(t.Context(), finished.Add(14*time.Minute))
		if got := a.workspaceState(t, ws.ID); got != "running" {
			t.Fatalf("state 14 minutes after a run = %q, want running", got)
		}
		a.SweepIdle(t.Context(), finished.Add(15*time.Minute))
		if got := a.workspaceState(t, ws.ID); got != "stopped" {
			t.Errorf("state 15 minutes after a run = %q, want stopped", got)
		}
	})

	t.Run("after the timeout the settings name", func(t *testing.T) {
		a := newAPI(t)
		project, _ := a.newProject(t, "short")
		ws := a.newWorkspace(t, project.ID)
		decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings",
			map[string]any{"workspace_idle_minutes": 5}), 200)
		start := time.Now()

		a.SweepIdle(t.Context(), start)
		a.SweepIdle(t.Context(), start.Add(5*time.Minute))
		if got := a.workspaceState(t, ws.ID); got != "stopped" {
			t.Errorf("state after 5 idle minutes = %q, want stopped", got)
		}
	})

	t.Run("never when the timeout is zero", func(t *testing.T) {
		a := newAPI(t)
		project, _ := a.newProject(t, "forever")
		ws := a.newWorkspace(t, project.ID)
		decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings",
			map[string]any{"workspace_idle_minutes": 0}), 200)
		start := time.Now()

		a.SweepIdle(t.Context(), start)
		a.SweepIdle(t.Context(), start.Add(24*time.Hour))
		if got := a.workspaceState(t, ws.ID); got != "running" {
			t.Errorf("state with no timeout = %q, want running", got)
		}
	})

	t.Run("not while a run is going in it", func(t *testing.T) {
		a := newAPI(t)
		project, _ := a.newProject(t, "asking")
		ws := a.newWorkspace(t, project.ID)
		sess := a.newSession(t, ws.ID)
		start := time.Now()
		a.SweepIdle(t.Context(), start)

		a.script(askStep("c1", "still there?"), providertest.Text("done"))
		a.postMessage(t, sess.ID, "hello", "", 202)
		a.waitQuestion(t, sess.ID)
		a.SweepIdle(t.Context(), start.Add(time.Hour))
		if got := a.workspaceState(t, ws.ID); got != "running" {
			t.Errorf("state with a run going = %q, want running", got)
		}
	})
}

func TestIdleTimeoutSettingIsBounded(t *testing.T) {
	a := newAPI(t)
	for _, value := range []any{-1, 10081, 1.5, "15"} {
		rec := request(t, a.Server, "PUT", "/api/settings", map[string]any{"workspace_idle_minutes": value})
		if rec.Code != 400 {
			t.Errorf("workspace_idle_minutes = %v: status %d, want 400", value, rec.Code)
		}
	}
	got := decodeBody[settingsWire](t, request(t, a.Server, "GET", "/api/settings", nil), 200)
	if got.Defaults.WorkspaceIdleMinutes != 15 {
		t.Errorf("default idle minutes = %d, want 15", got.Defaults.WorkspaceIdleMinutes)
	}
}
