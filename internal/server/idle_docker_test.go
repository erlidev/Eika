//go:build docker

package server_test

import (
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider/providertest"
)

// computerState reads a computer's recorded state from the computer list.
func (a *api) computerState(t *testing.T, id string) string {
	t.Helper()
	for _, cx := range decodeBody[computersWire](t, request(t, a.Server, "GET", "/api/computers", nil), 200).Computers {
		if cx.ID == id {
			return cx.State
		}
	}
	t.Fatalf("computer %s is not listed", id)
	return ""
}

func TestIdleComputersStop(t *testing.T) {
	t.Run("after the default timeout without a run", func(t *testing.T) {
		a := newAPI(t)
		cx := a.home(t)
		start := time.Now()

		a.SweepIdle(t.Context(), start)
		a.SweepIdle(t.Context(), start.Add(14*time.Minute))
		if got := a.computerState(t, cx.ID); got != "running" {
			t.Fatalf("state after 14 idle minutes = %q, want running", got)
		}
		a.SweepIdle(t.Context(), start.Add(15*time.Minute))
		if got := a.computerState(t, cx.ID); got != "stopped" {
			t.Errorf("state after 15 idle minutes = %q, want stopped", got)
		}
	})

	t.Run("counting from the end of the last run", func(t *testing.T) {
		a := newAPI(t)
		cx := a.home(t)
		sess := a.newSession(t, cx.ID)
		// First seen ten minutes ago, so only the run keeps it up below.
		a.SweepIdle(t.Context(), time.Now().Add(-10*time.Minute))

		a.script(providertest.Text("done"))
		a.postMessage(t, sess.ID, "hello", "", 202)
		a.waitIdle(t, sess.ID)
		finished := time.Now()

		a.SweepIdle(t.Context(), finished.Add(14*time.Minute))
		if got := a.computerState(t, cx.ID); got != "running" {
			t.Fatalf("state 14 minutes after a run = %q, want running", got)
		}
		a.SweepIdle(t.Context(), finished.Add(15*time.Minute))
		if got := a.computerState(t, cx.ID); got != "stopped" {
			t.Errorf("state 15 minutes after a run = %q, want stopped", got)
		}
	})

	t.Run("after the timeout the settings name", func(t *testing.T) {
		a := newAPI(t)
		cx := a.home(t)
		decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings",
			map[string]any{"computer_idle_minutes": 5}), 200)
		start := time.Now()

		a.SweepIdle(t.Context(), start)
		a.SweepIdle(t.Context(), start.Add(5*time.Minute))
		if got := a.computerState(t, cx.ID); got != "stopped" {
			t.Errorf("state after 5 idle minutes = %q, want stopped", got)
		}
	})

	t.Run("never when the timeout is zero", func(t *testing.T) {
		a := newAPI(t)
		cx := a.home(t)
		decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings",
			map[string]any{"computer_idle_minutes": 0}), 200)
		start := time.Now()

		a.SweepIdle(t.Context(), start)
		a.SweepIdle(t.Context(), start.Add(24*time.Hour))
		if got := a.computerState(t, cx.ID); got != "running" {
			t.Errorf("state with no timeout = %q, want running", got)
		}
	})

	t.Run("not while a run is going in it", func(t *testing.T) {
		a := newAPI(t)
		cx := a.home(t)
		sess := a.newSession(t, cx.ID)
		start := time.Now()
		a.SweepIdle(t.Context(), start)

		a.script(askStep("c1", "still there?"), providertest.Text("done"))
		a.postMessage(t, sess.ID, "hello", "", 202)
		a.waitQuestion(t, sess.ID)
		a.SweepIdle(t.Context(), start.Add(time.Hour))
		if got := a.computerState(t, cx.ID); got != "running" {
			t.Errorf("state with a run going = %q, want running", got)
		}
	})
}

func TestIdleTimeoutSettingIsBounded(t *testing.T) {
	a := newAPI(t)
	for _, value := range []any{-1, 10081, 1.5, "15"} {
		rec := request(t, a.Server, "PUT", "/api/settings", map[string]any{"computer_idle_minutes": value})
		if rec.Code != 400 {
			t.Errorf("computer_idle_minutes = %v: status %d, want 400", value, rec.Code)
		}
	}
	got := decodeBody[settingsWire](t, request(t, a.Server, "GET", "/api/settings", nil), 200)
	if got.Defaults.ComputerIdleMinutes != 15 {
		t.Errorf("default idle minutes = %d, want 15", got.Defaults.ComputerIdleMinutes)
	}
}
