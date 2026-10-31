package probe_test

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// leaseWatcher notes how many leases the pool holds each time the check asks
// the model something.
type leaseWatcher struct {
	provider.Provider
	q *inference.Queue

	mu   sync.Mutex
	held [][]string
}

func (w *leaseWatcher) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	var priorities []string
	for _, l := range w.q.Snapshot()[0].Running {
		priorities = append(priorities, l.Priority.String())
	}
	w.mu.Lock()
	w.held = append(w.held, priorities)
	w.mu.Unlock()
	return w.Provider.Stream(ctx, req)
}

func newQueue() *inference.Queue {
	q := inference.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.SetPools([]inference.Pool{{Name: "gpu", Slots: 1}})
	return q
}

func TestCheckResponsesTakesALeaseForEachRequestAtTheCallersPriority(t *testing.T) {
	q := newQueue()
	w := &leaseWatcher{Provider: providertest.New(toolCall(providertest.Usage(40, 20, 60), true), answer(stated(70, 3, 60))), q: q}
	got := probe.CheckResponses(context.Background(), probe.ResponsesCheck{
		Provider: w, Model: "m", Queue: q,
		Need: inference.Need{Pool: "gpu", Priority: inference.Maintenance, Endpoint: "llama"},
	})
	if got.Verdict != probe.Passed {
		t.Fatalf("conformance = %+v, want passed", got)
	}
	if want := [][]string{{"maintenance"}, {"maintenance"}}; !reflect.DeepEqual(w.held, want) {
		t.Errorf("leases held at each request = %v, want %v", w.held, want)
	}
	if n := len(q.Snapshot()[0].Running); n != 0 {
		t.Errorf("%d leases left after the check", n)
	}
}

func TestACheckThatNeverGetsItsTurnIsSkippedNotFailed(t *testing.T) {
	q := newQueue()
	user, err := q.Acquire(context.Background(), inference.Need{Pool: "gpu", Priority: inference.Interactive})
	if err != nil {
		t.Fatal(err)
	}
	defer user.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	p := providertest.New()
	got := probe.CheckResponses(ctx, probe.ResponsesCheck{
		Provider: p, Model: "m", Build: "b1", Queue: q,
		Need: inference.Need{Pool: "gpu", Priority: inference.Maintenance},
	})
	if got.Verdict != probe.Skipped || !got.Busy {
		t.Fatalf("conformance = %+v, want skipped as busy, not failed: the endpoint did nothing wrong", got)
	}
	if p.Calls() != 0 {
		t.Error("the model was asked without a lease")
	}
	if got.Current("b1", "m") {
		t.Error("a check that never ran is current; it would never run again")
	}
}
