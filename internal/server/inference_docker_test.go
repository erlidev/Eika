//go:build docker

package server_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// inferenceWire is GET /api/inference, as far as the tests read it.
type inferenceWire struct {
	Policy struct {
		MaintenanceIdleSeconds int  `json:"maintenance_idle_seconds"`
		AgingSeconds           int  `json:"aging_seconds"`
		PreemptRunning         bool `json:"preempt_running"`
	} `json:"policy"`
	Pools []struct {
		Name        string   `json:"name"`
		Endpoints   []string `json:"endpoints"`
		Slots       int      `json:"slots"`
		KV          string   `json:"kv"`
		CacheTokens *int     `json:"cache_tokens"`
		Running     []struct {
			Slot      int    `json:"slot"`
			SessionID string `json:"session_id"`
			Priority  string `json:"priority"`
		} `json:"running"`
		Waiting []struct {
			SessionID string `json:"session_id"`
			Priority  string `json:"priority"`
			Gated     bool   `json:"gated"`
		} `json:"waiting"`
	} `json:"pools"`
}

// inference reads the queue.
func (a *api) inference(t *testing.T) inferenceWire {
	t.Helper()
	return decodeBody[inferenceWire](t, request(t, a.Server, "GET", "/api/inference", nil), 200)
}

// gate is a provider whose responses wait to be let go: each request says it
// has started, and answers once release is closed.
type gate struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func newGate() *gate {
	return &gate{started: make(chan struct{}, 16), release: make(chan struct{})}
}

func (g *gate) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	g.calls.Add(1)
	g.started <- struct{}{}
	out := make(chan provider.Event, 2)
	go func() {
		defer close(out)
		select {
		case <-g.release:
		case <-ctx.Done():
			return
		}
		out <- provider.TextDelta("ok")
		out <- provider.Done("stop")
	}()
	return out, nil
}

func TestTwoRunsOnOnePoolTakeTurnsAndTheSecondRecordsItsWait(t *testing.T) {
	a := newAPI(t)
	g := newGate()
	a.use(g)
	cx := a.home(t)
	first, second := a.newSession(t, cx.ID), a.newSession(t, cx.ID)

	a.postMessage(t, first.ID, "one", "", 202)
	<-g.started
	a.postMessage(t, second.ID, "two", "", 202)
	waitFor(t, "the second run to wait in the queue", func() bool {
		pools := a.inference(t).Pools
		return len(pools) == 1 && len(pools[0].Waiting) == 1
	})
	st := a.inference(t).Pools[0]
	if st.Name != "test" || st.Slots != 1 || len(st.Running) != 1 || st.Running[0].SessionID != first.ID ||
		st.Running[0].Priority != "interactive" || st.Waiting[0].SessionID != second.ID {
		t.Fatalf("pool = %+v, want the first run holding the one slot and the second waiting", st)
	}
	if g.calls.Load() != 1 {
		t.Fatalf("model calls = %d while the pool is full, want 1", g.calls.Load())
	}
	time.Sleep(30 * time.Millisecond)
	close(g.release)
	a.waitIdle(t, first.ID)
	a.waitIdle(t, second.ID)

	wait := func(sessionID string) (queueMS, slot *int, priority string) {
		calls := decodeBody[callsWire](t, request(t, a.Server, "GET", "/api/sessions/"+sessionID+"/calls", nil), 200).Calls
		if len(calls) != 1 {
			t.Fatalf("calls of %s = %d, want 1", sessionID, len(calls))
		}
		return calls[0].QueueMS, calls[0].Slot, calls[0].Priority
	}
	q1, s1, p1 := wait(first.ID)
	q2, s2, p2 := wait(second.ID)
	if q1 == nil || q2 == nil || *q2 < 30 || *q1 >= *q2 || s1 == nil || s2 == nil || *s1 != 0 || *s2 != 0 || p1 != "interactive" || p2 != "interactive" {
		t.Errorf("recorded waits = %v and %v ms on slots %v and %v, want the second to have waited for the first", deref(q1), deref(q2), deref(s1), deref(s2))
	}
	if st := a.inference(t).Pools[0]; len(st.Running) != 0 || len(st.Waiting) != 0 {
		t.Errorf("pool after the runs = %+v, want it empty", st)
	}
}

func TestAProvidersPoolSettingsReachTheQueueWithoutARestart(t *testing.T) {
	a := newAPI(t)
	id := a.testProvider.ID
	patch := func(body map[string]any) {
		t.Helper()
		decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+id, body), 200)
	}
	pool := func() (slots int, kv string, cache *int, endpoints []string) {
		for _, p := range a.inference(t).Pools {
			if p.Name == "test" {
				return p.Slots, p.KV, p.CacheTokens, p.Endpoints
			}
		}
		t.Fatal("no pool named test")
		return
	}

	patch(map[string]any{"slots": 3, "kv": "shared", "kv_tokens": 131072})
	if slots, kv, cache, _ := pool(); slots != 3 || kv != "shared" || cache == nil || *cache != 131072 {
		t.Errorf("pool = %d slots, %s, cache %v; want 3, shared, 131072", slots, kv, deref(cache))
	}
	patch(map[string]any{"kv": "partitioned"})
	if slots, kv, cache, _ := pool(); slots != 3 || kv != "partitioned" || cache != nil {
		t.Errorf("pool = %d slots, %s, cache %v; want 3, partitioned, none", slots, kv, deref(cache))
	}

	// A second provider joins the pool: the pool is one machine, so the
	// stricter of the two rows rules.
	other := decodeBody[providerWire](t, request(t, a.Server, "POST", "/api/providers", map[string]any{
		"name": "second", "base_url": "https://second.test/v1",
	}), 201)
	decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+other.ID, map[string]any{
		"pool": "test", "slots": 2,
	}), 200)
	if slots, _, _, endpoints := pool(); slots != 2 || len(endpoints) != 2 {
		t.Errorf("pool = %d slots on %v; want the fewer slots of the two rows, and both providers", slots, endpoints)
	}
	decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+other.ID, map[string]any{
		"pool": "elsewhere",
	}), 200)
	if slots, _, _, endpoints := pool(); slots != 3 || len(endpoints) != 1 {
		t.Errorf("pool = %d slots on %v after the provider left; want 3 on one provider", slots, endpoints)
	}
}

func TestTheQueuesSettingsAreCheckedAndApplied(t *testing.T) {
	a := newAPI(t)
	if p := a.inference(t).Policy; p.MaintenanceIdleSeconds != 120 || p.AgingSeconds != 300 || p.PreemptRunning {
		t.Errorf("default policy = %+v, want 120, 300, off", p)
	}
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"maintenance_idle_seconds": 5, "aging_seconds": 60, "preempt_running": true,
	}), 200)
	if p := a.inference(t).Policy; p.MaintenanceIdleSeconds != 5 || p.AgingSeconds != 60 || !p.PreemptRunning {
		t.Errorf("policy = %+v, want 5, 60, on", p)
	}
	for _, bad := range []map[string]any{
		{"maintenance_idle_seconds": -1},
		{"aging_seconds": 0},
		{"preempt_running": "yes"},
	} {
		if rec := request(t, a.Server, "PUT", "/api/settings", bad); rec.Code != 400 {
			t.Errorf("PUT %v = %d, want 400", bad, rec.Code)
		}
	}
	// null means the default again.
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"maintenance_idle_seconds": nil, "aging_seconds": nil, "preempt_running": nil,
	}), 200)
	if p := a.inference(t).Policy; p.MaintenanceIdleSeconds != 120 || p.AgingSeconds != 300 || p.PreemptRunning {
		t.Errorf("policy after null = %+v, want the defaults", p)
	}
}

func TestASessionTitleWaitsForAnIdlePoolAndOutlivesItsRun(t *testing.T) {
	a := newAPI(t)
	titler := providertest.New(providertest.Text("Fix the login loop"))
	a.assignTitleModel(t, a.addTitleModel(t, titler))
	// The titler shares the runs' pool, and the pool must be quiet for two
	// seconds before a title is made.
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"maintenance_idle_seconds": 2,
	}), 200)
	var utility string
	for _, p := range decodeBody[providersWire](t, request(t, a.Server, "GET", "/api/providers", nil), 200).Providers {
		if p.Name == "utility" {
			utility = p.ID
		}
	}
	decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+utility, map[string]any{"pool": "test"}), 200)

	// One run leaves the pool active just now; an untitled chat then runs
	// and ends well inside the idle time.
	busy := a.session(t)
	a.script(providertest.Text("done"), providertest.Text("answered"))
	a.postMessage(t, busy.ID, "hello", "", 202)
	a.waitIdle(t, busy.ID)
	chat := decodeBody[sessionWire](t, request(t, a.Server, "POST", "/api/sessions", map[string]any{"chat": true}), 201)
	a.postMessage(t, chat.ID, "the login page loops forever", "", 202)
	a.waitIdle(t, chat.ID)

	if titler.Calls() != 0 {
		t.Fatalf("title requests = %d right after the run, want the title to wait for an idle pool", titler.Calls())
	}
	waiting := a.inference(t).Pools
	if len(waiting) == 0 || len(waiting[0].Waiting) != 1 || waiting[0].Waiting[0].Priority != "maintenance" || !waiting[0].Waiting[0].Gated {
		t.Errorf("queue = %+v, want one gated maintenance call", waiting)
	}
	waitFor(t, "the title, made after the run ended", func() bool { return a.sessionTitle(t, chat.ID) == "Fix the login loop" })
}

func TestTheModelTestWaitsInTheQueueAsAnInteractiveCall(t *testing.T) {
	a := newAPI(t)
	g := newGate()
	a.use(g)
	sess := a.session(t)
	a.postMessage(t, sess.ID, "hold the pool", "", 202)
	<-g.started

	done := make(chan int, 1)
	go func() {
		rec := request(t, a.Server, "POST", "/api/models/test", map[string]any{
			"provider_id": a.testProvider.ID, "model": "test-model",
		})
		done <- rec.Code
	}()
	waitFor(t, "the test to wait in the queue", func() bool {
		pools := a.inference(t).Pools
		return len(pools) == 1 && len(pools[0].Waiting) == 1
	})
	if w := a.inference(t).Pools[0].Waiting[0]; w.Priority != "interactive" {
		t.Errorf("model test waits as %q, want interactive: the user is waiting on it", w.Priority)
	}
	close(g.release)
	select {
	case code := <-done:
		if code != 200 {
			t.Errorf("model test = %d, want 200", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the model test never finished")
	}
	a.waitIdle(t, sess.ID)
}
