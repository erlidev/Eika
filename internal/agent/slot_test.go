package agent_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// newHintedQueue returns a queue whose one pool is a llama.cpp endpoint named
// llama with the given number of slots.
func newHintedQueue(slots int) *inference.Queue {
	q := inference.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.SetPools([]inference.Pool{{
		Name: "gpu", Slots: slots, Hinted: []inference.SlotEndpoint{{Name: "llama", Slots: slots}},
	}})
	return q
}

// slots reads the slot each request named, -1 for a request that named none.
func slots(reqs []provider.Request) []int {
	out := make([]int, len(reqs))
	for i, r := range reqs {
		out[i] = -1
		if r.SlotHint != nil {
			out[i] = *r.SlotHint
		}
	}
	return out
}

func TestEachSessionsCallsNameTheSlotTheirLeaseHolds(t *testing.T) {
	q := newHintedQueue(2)
	p := providertest.New(providertest.Text("1"), providertest.Text("2"), providertest.Text("3"), providertest.Text("4"))
	one := newQueuedRun(t, q, nil, p, 0)
	two := newQueuedRun(t, q, nil, p, 0)
	two.session = agent.NewSession("session-2", "computer-1")
	for _, r := range []*queuedRun{one, two, one, two} {
		if err := within(t, r.start(context.Background(), "hello")); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	got := slots(p.Requests())
	if len(got) != 4 || got[0] < 0 || got[1] < 0 || got[0] == got[1] || got[2] != got[0] || got[3] != got[1] {
		t.Fatalf("slots named = %v, want each session on one slot of its own, the same both times", got)
	}
	// What a call records is the slot its request named.
	for i, r := range []*queuedRun{one, two} {
		calls, _ := r.calls.recorded()
		if len(calls) != 2 {
			t.Fatalf("session %d recorded %d calls, want 2", i+1, len(calls))
		}
		for _, c := range calls {
			if c.Queue.Slot == nil || *c.Queue.Slot != got[i] {
				t.Errorf("session %d recorded slot %v, want %d", i+1, c.Queue.Slot, got[i])
			}
		}
	}
}

func TestAnEndpointWithoutSlotHintsIsNamedNoSlot(t *testing.T) {
	q := newQueue(2)
	p := providertest.New(providertest.Text("1"))
	run := newQueuedRun(t, q, nil, p, 0)
	if err := within(t, run.start(context.Background(), "hello")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := slots(p.Requests()); !reflect.DeepEqual(got, []int{-1}) {
		t.Errorf("slots named = %v, want none", got)
	}
	if calls, _ := run.calls.recorded(); len(calls) != 1 || calls[0].Queue.Slot != nil {
		t.Errorf("recorded calls = %+v, want one that records no slot", calls)
	}
}

func TestACompactionSummaryRunsOnTheSlotOfItsSession(t *testing.T) {
	q := newHintedQueue(2)
	var steps []providertest.Step
	for i := range 4 {
		steps = append(steps, providertest.Text(filler(fmt.Sprintf("a%d", 2*i+1), 1000)))
	}
	steps = append(steps, providertest.Text("SUMMARY-1"))
	p := providertest.New(steps...)
	run := newQueuedRun(t, q, nil, p, 0)
	for i := range 4 {
		if err := run.agent.Run(context.Background(), run.session, provider.UserMessage(filler(fmt.Sprintf("u%d", 2*i), 1000))); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
	if err := run.agent.Compact(context.Background(), run.session, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	got := slots(p.Requests())
	if len(got) != 5 || got[0] < 0 {
		t.Fatalf("slots named = %v, want five requests on a slot", got)
	}
	for i, slot := range got {
		if slot != got[0] {
			t.Errorf("request %d named slot %d, want %d: the summary reads the session's prefix", i+1, slot, got[0])
		}
	}
}
