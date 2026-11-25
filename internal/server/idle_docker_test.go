//go:build docker

package server_test

import (
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
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

	t.Run("counting from the end of the last worker run", func(t *testing.T) {
		d := newDelegating(t)
		d.SweepIdle(t.Context(), time.Now().Add(-10*time.Minute))
		eika := providertest.New(call("start", "subagent_start", map[string]any{"agent": "worker", "name": "worker", "brief": "work"}), providertest.Text("started"))
		worker := providertest.New(call("report", "report", map[string]any{"summary": "done"}), providertest.Text("done"))
		d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a worker": worker})
		d.postMessage(t, d.home.ID, "start", "", 202)
		waitFor(t, "worker to finish", func() bool {
			child, err := d.store.Subagent(t.Context(), d.home.ID, "worker")
			return err == nil && child.State == store.SessionIdle
		})
		finished := time.Now()
		d.SweepIdle(t.Context(), finished.Add(14*time.Minute))
		if got := d.computerState(t, d.computer); got != "running" {
			t.Fatalf("state after 14 minutes = %q", got)
		}
		d.SweepIdle(t.Context(), finished.Add(15*time.Minute))
		if got := d.computerState(t, d.computer); got != "stopped" {
			t.Errorf("state after 15 minutes = %q", got)
		}
	})
	t.Run("not while a worker run is going", func(t *testing.T) {
		d := newDelegating(t)
		start := time.Now()
		d.SweepIdle(t.Context(), start)
		eika := providertest.New(call("start", "subagent_start", map[string]any{"agent": "worker", "name": "worker", "brief": "work"}), providertest.Text("started"))
		worker := newHeldScript(call("report", "report", map[string]any{"summary": "done"}), providertest.Text("done"))
		d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a worker": worker})
		d.postMessage(t, d.home.ID, "start", "", 202)
		<-worker.started
		d.SweepIdle(t.Context(), start.Add(time.Hour))
		if got := d.computerState(t, d.computer); got != "running" {
			t.Errorf("state with worker running = %q", got)
		}
		worker.open()
		waitFor(t, "worker to finish", func() bool {
			child, err := d.store.Subagent(t.Context(), d.home.ID, "worker")
			return err == nil && child.State == store.SessionIdle
		})
	})
}
