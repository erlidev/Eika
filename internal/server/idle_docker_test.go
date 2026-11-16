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
