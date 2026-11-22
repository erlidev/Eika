//go:build docker

package server_test

import (
	"testing"

	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/server"
)

type haltWire struct {
	Halted bool   `json:"halted"`
	Reason string `json:"reason"`
}

func TestHaltAbortsRunsRefusesNewOnesAndSurvivesRestart(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	p := a.hold(askStep("c1", "waiting"), providertest.Text("never reached"))
	run := a.postMessage(t, sess.ID, "start", "", 202)
	<-p.started

	got := decodeBody[haltWire](t, request(t, a.Server, "POST", "/api/halt", map[string]any{"reason": "test"}), 200)
	if !got.Halted || got.Reason != "test" {
		t.Fatalf("halt = %+v", got)
	}
	if a.runState(t, sess.ID).Active {
		t.Error("the run is still going after halt")
	}
	if row, err := a.store.Run(t.Context(), run.ID); err != nil || row.State != "aborted" {
		t.Errorf("run = %+v, %v; want aborted", row, err)
	}
	a.postMessage(t, sess.ID, "again", "", 409)

	// A restart keeps the halt.
	a.Close()
	a.Server = server.New(testConfig(), testLogger(), a.deps, server.Options{})
	t.Cleanup(a.Close)
	if got := decodeBody[haltWire](t, request(t, a.Server, "GET", "/api/halt", nil), 200); !got.Halted {
		t.Fatal("halt lost across restart")
	}
	a.postMessage(t, sess.ID, "still halted", "", 409)

	if got := decodeBody[haltWire](t, request(t, a.Server, "POST", "/api/halt/resume", nil), 200); got.Halted {
		t.Fatal("still halted after resume")
	}
	a.script(providertest.Text("back"))
	a.postMessage(t, sess.ID, "after resume", "", 202)
	a.waitIdle(t, sess.ID)
}
