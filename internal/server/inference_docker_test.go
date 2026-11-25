//go:build docker

package server_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
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
	d := newDelegating(t)
	if _, err := d.store.OverrideProvider(t.Context(), d.testProvider.ID, store.ProviderOverride{Slots: new(1)}); err != nil {
		t.Fatal(err)
	}
	worker := newGate()
	eika := providertest.New(call("start", "subagent_start", map[string]any{"agent": "researcher", "name": "research", "brief": "work"}), providertest.Text("started"), providertest.Text("second answer"))
	d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a researcher": worker})
	d.mu.Lock()
	d.delay = 500 * time.Millisecond
	d.mu.Unlock()
	d.postMessage(t, d.home.ID, "start research", "", 202)
	d.waitIdle(t, d.home.ID)
	<-worker.started
	child := d.child(t, "research")
	d.postMessage(t, d.home.ID, "another question", "", 202)
	waitFor(t, "home to wait for the worker", func() bool { pools := d.inference(t).Pools; return len(pools) == 1 && len(pools[0].Waiting) == 1 })
	pool := d.inference(t).Pools[0]
	if len(pool.Running) != 1 || pool.Running[0].SessionID != child.ID || pool.Waiting[0].SessionID != d.home.ID {
		t.Fatalf("pool = %+v", pool)
	}
	time.Sleep(30 * time.Millisecond)
	close(worker.release)
	d.waitIdle(t, d.home.ID)
	calls := d.homeCalls(t)
	if len(calls) < 3 || calls[2].QueueMS == nil || *calls[2].QueueMS < 30 {
		t.Fatalf("home calls = %+v", calls)
	}
}
