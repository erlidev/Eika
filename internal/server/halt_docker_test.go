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
	a.Server = server.New(a.cfg, testLogger(), a.deps, server.Options{})
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

func TestCanaryInABashCommandHaltsAndAbortsTheRun(t *testing.T) {
	a := newAPI(t)
	a.home(t)
	if err := a.BindAgentToolsForTest(a.host); err != nil {
		t.Fatal(err)
	}
	canary := a.CanaryForTest()
	if canary == "" {
		t.Fatal("the server made no canary")
	}
	home, err := a.store.EnsureHomeSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	eika := providertest.New(call("start", "subagent_start", map[string]any{"agent": "worker", "name": "shell", "brief": "Run the command"}), providertest.Text("started"))
	worker := providertest.New(call("cmd", "bash", map[string]any{"command": "echo " + canary}), providertest.Text("never reached"))
	a.use(byAgent{"You are Eika": answeringNotices(eika), "You are a worker": worker})
	a.postMessage(t, home.ID, "run the command", "", 202)

	waitFor(t, "the canary to halt Eika", func() bool {
		return decodeBody[haltWire](t, request(t, a.Server, "GET", "/api/halt", nil), 200).Halted
	})
	child, err := a.store.Subagent(t.Context(), home.ID, "shell")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the worker's run to abort", func() bool {
		st := a.runState(t, child.ID)
		return !st.Active && st.Run != nil && st.Run.State == "aborted"
	})
}
