//go:build docker

package server_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
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
	if q1 == nil || q2 == nil || *q2 < 30 || *q1 >= *q2 || s1 != nil || s2 != nil || p1 != "interactive" || p2 != "interactive" {
		t.Errorf("recorded waits = %v and %v ms on slots %v and %v, want the second to have waited for the first", deref(q1), deref(q2), deref(s1), deref(s2))
	}
	if st := a.inference(t).Pools[0]; len(st.Running) != 0 || len(st.Waiting) != 0 {
		t.Errorf("pool after the runs = %+v, want it empty", st)
	}
}
