//go:build docker

package server_test

import (
	"testing"

	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/sentry"
)

type tripwireWire struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Running bool   `json:"running"`
	Host    string `json:"host"`
	Summary string `json:"summary"`
}

type tripwireCountsWire struct {
	Counts []struct {
		Tripwire string `json:"tripwire"`
		Fired    int    `json:"fired"`
		Fine     int    `json:"fine"`
	} `json:"counts"`
}

func inboxCount(t *testing.T, a *api) int {
	t.Helper()
	return decodeBody[struct {
		Pending int `json:"pending"`
	}](t, request(t, a.Server, "GET", "/api/inbox/count", nil), 200).Pending
}

func openEntries(t *testing.T, a *api) []tripwireWire {
	t.Helper()
	return decodeBody[struct {
		Entries []tripwireWire `json:"entries"`
	}](t, request(t, a.Server, "GET", "/api/inbox/tripwires", nil), 200).Entries
}

func TestStopTheRunAbortsItAndFineMakesTheHostKnown(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	if err := a.RecoverMemory(t.Context()); err != nil {
		t.Fatal(err)
	}
	sess := a.session(t)
	p := a.hold(askStep("c1", "waiting"), providertest.Text("never reached"))
	run := a.postMessage(t, sess.ID, "start", "", 202)
	<-p.started

	a.TrippedForTest(t.Context(), sentry.Tripped{Tripwire: "followed", Held: true, Host: "collect.example.net",
		Principal: sentry.Principal{Session: sess.ID, Run: run.ID}})
	a.TrippedForTest(t.Context(), sentry.Tripped{Tripwire: "drained", Host: "upload.example.net",
		Principal: sentry.Principal{Session: sess.ID, Run: "ended-run"}})
	entries := openEntries(t, a)
	if len(entries) != 2 || inboxCount(t, a) != 2 {
		t.Fatalf("entries = %+v, count %d; want two open", entries, inboxCount(t, a))
	}
	byHost := map[string]tripwireWire{entries[0].Host: entries[0], entries[1].Host: entries[1]}
	held, ended := byHost["collect.example.net"], byHost["upload.example.net"]
	if !held.Running || ended.Running || held.Summary == "" {
		t.Fatalf("held = %+v, ended = %+v", held, ended)
	}

	// Stop the run: it aborts, the entry closes, and a second answer is refused.
	if got := decodeBody[tripwireWire](t, request(t, a.Server, "POST", "/api/inbox/tripwires/"+held.ID+"/stop", nil), 200); got.Status != "stopped" {
		t.Errorf("stop answered %+v", got)
	}
	if row, err := a.store.Run(t.Context(), run.ID); err != nil || row.State != "aborted" {
		t.Errorf("run = %+v, %v; want aborted", row, err)
	}
	if r := request(t, a.Server, "POST", "/api/inbox/tripwires/"+held.ID+"/fine", nil); r.Code != 409 {
		t.Errorf("answering twice = %d, want 409", r.Code)
	}

	// "This was fine" on an ended run still teaches the host.
	if got := decodeBody[tripwireWire](t, request(t, a.Server, "POST", "/api/inbox/tripwires/"+ended.ID+"/fine", nil), 200); got.Status != "fine" {
		t.Errorf("fine answered %+v", got)
	}
	if known, err := a.store.KnownHost(t.Context(), "any-computer", "upload.example.net"); err != nil || !known {
		t.Errorf("answered host known = %v, %v", known, err)
	}
	if known, _ := a.store.KnownHost(t.Context(), "any-computer", "collect.example.net"); known {
		t.Error("a stopped run's host became known")
	}
	// "Stop" on an ended run has nothing to abort: it closes the entry as
	// stopped and teaches no host.
	a.TrippedForTest(t.Context(), sentry.Tripped{Tripwire: "lifted", Host: "third.example.net",
		Principal: sentry.Principal{Session: sess.ID, Run: "ended-run"}})
	third := openEntries(t, a)[0]
	if got := decodeBody[tripwireWire](t, request(t, a.Server, "POST", "/api/inbox/tripwires/"+third.ID+"/stop", nil), 200); got.Status != "stopped" {
		t.Errorf("stopping an ended run answered %+v", got)
	}
	if known, _ := a.store.KnownHost(t.Context(), "any-computer", "third.example.net"); known {
		t.Error("a stopped entry's host became known")
	}
	if n := inboxCount(t, a); n != 0 {
		t.Errorf("count = %d after both answers, want 0", n)
	}
	counts := decodeBody[tripwireCountsWire](t, request(t, a.Server, "GET", "/api/tripwires/counts", nil), 200).Counts
	if len(counts) != 3 || counts[0].Tripwire != "drained" || counts[0].Fired != 1 || counts[0].Fine != 1 || counts[1].Tripwire != "followed" || counts[1].Fine != 0 || counts[2].Tripwire != "lifted" || counts[2].Fine != 0 {
		t.Errorf("counts = %+v", counts)
	}
}
