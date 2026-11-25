//go:build docker && live

package server_test

import (
	"net/http"
	"testing"

	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/store"
)

func TestLiveBackgroundRunsRequireRoutineSessions(t *testing.T) {
	a := newAPI(t)
	s := server.New(a.cfg, testLogger(), a.deps, server.Options{LiveTest: true})
	t.Cleanup(s.Close)
	home, err := a.store.EnsureHomeSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	child, err := a.store.CreateSession(t.Context(), store.Session{Kind: store.SessionSubagent, ParentSessionID: home.ID, Agent: "worker", Name: "child", State: store.SessionIdle})
	if err != nil {
		t.Fatal(err)
	}
	routine, err := a.store.CreateSession(t.Context(), store.Session{Kind: store.SessionRoutine, Agent: "eika", Name: "routine"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{home.ID, child.ID} {
		rec := request(t, s, http.MethodPost, "/api/_live/sessions/"+id+"/background", map[string]string{"text": "work"})
		if rec.Code != http.StatusConflict {
			t.Errorf("session %s: status %d", id, rec.Code)
		}
		runs, err := a.store.Runs(t.Context(), id)
		if err != nil || len(runs) != 0 {
			t.Fatalf("session %s: runs %+v, error %v", id, runs, err)
		}
	}
	a.script(providertest.Text("done"))
	rec := request(t, s, http.MethodPost, "/api/_live/sessions/"+routine.ID+"/background", map[string]string{"text": "work"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("routine: status %d: %s", rec.Code, rec.Body.String())
	}
	waitFor(t, "routine background call", func() bool {
		calls, err := a.store.ModelCalls(t.Context(), routine.ID)
		return err == nil && len(calls) > 0 && calls[0].Priority == "background"
	})
}
