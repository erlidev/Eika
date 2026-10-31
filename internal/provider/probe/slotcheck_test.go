package probe_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// reply is a response that states its usage.
func reply(usage provider.Usage) providertest.Step {
	return providertest.Stream(
		provider.TextDelta("ok"),
		provider.Event{Kind: provider.KindUsage, Usage: usage},
		provider.Done("stop"))
}

func slotsNamed(p *providertest.Provider) []int {
	var out []int
	for _, r := range p.Requests() {
		if r.SlotHint == nil {
			out = append(out, -1)
			continue
		}
		out = append(out, *r.SlotHint)
	}
	return out
}

func TestCheckSlotAffinityPassesWhenANamedSlotKeepsItsPrefixAcrossARequestOnTheOther(t *testing.T) {
	p := providertest.New(
		reply(stated(800, 3, 0)),
		reply(stated(30, 3, 0)),
		reply(stated(830, 3, 790)),
	)
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	got := probe.CheckSlotAffinity(context.Background(), probe.SlotCheck{
		Provider: p, Model: "qwen", Build: "b11942", Now: func() time.Time { return now },
	})
	want := probe.SlotAffinity{Verdict: probe.Passed, Model: "qwen", Build: "b11942", CheckedAt: now, Prompt: 800, Cached: 790}
	if got != want {
		t.Errorf("slot affinity = %+v, want %+v", got, want)
	}
	if slots := slotsNamed(p); len(slots) != 3 || slots[0] != 0 || slots[1] != 1 || slots[2] != 0 {
		t.Errorf("slots named = %v, want 0, 1, 0", slots)
	}
	reqs := p.Requests()
	// The last request continues the first's conversation, as a session's next
	// turn does, so what is found cached is what a session would find.
	if reqs[2].System != reqs[0].System || len(reqs[2].Messages) != 3 || reqs[2].Messages[0].Content != reqs[0].Messages[0].Content {
		t.Errorf("the last request does not continue the first: %+v", reqs[2])
	}
	if reqs[1].System == reqs[0].System {
		t.Error("the request on the other slot shares the first's prompt")
	}
	if !strings.HasPrefix(reqs[0].System, "Slot check ") || len(reqs[0].System) < 2000 {
		t.Errorf("the first prompt is %d bytes starting %q, want a nonce and a prompt long enough to tell a cache hit by", len(reqs[0].System), reqs[0].System[:min(len(reqs[0].System), 20)])
	}
}

func TestCheckSlotAffinityFailsWhenANamedSlotLosesItsPrefix(t *testing.T) {
	// llama.cpp with a unified cache clears the idle slot when the other starts
	// a task: the named slot is empty when the last request reaches it.
	p := providertest.New(
		reply(stated(800, 3, 0)),
		reply(stated(30, 3, 0)),
		reply(stated(830, 3, 0)),
	)
	got := probe.CheckSlotAffinity(context.Background(), probe.SlotCheck{Provider: p, Model: "qwen", Build: "b11942"})
	if got.Verdict != probe.Failed {
		t.Fatalf("slot affinity = %+v, want failed", got)
	}
	for _, want := range []string{"0 of the 800", "--no-cache-idle-slots"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not say %q", got.Reason, want)
		}
	}
	if got.Prompt != 800 || got.Cached != 0 {
		t.Errorf("prompt %d, cached %d, want 800 and 0", got.Prompt, got.Cached)
	}
}

func TestCheckSlotAffinityFailsBelowTheBar(t *testing.T) {
	p := providertest.New(reply(stated(1000, 3, 0)), reply(stated(30, 3, 0)), reply(stated(1040, 3, 799)))
	if got := probe.CheckSlotAffinity(context.Background(), probe.SlotCheck{Provider: p, Model: "m"}); got.Verdict != probe.Failed {
		t.Errorf("799 of 1000 cached = %+v, want failed: under 80%%", got)
	}
	p = providertest.New(reply(stated(1000, 3, 0)), reply(stated(30, 3, 0)), reply(stated(1040, 3, 800)))
	if got := probe.CheckSlotAffinity(context.Background(), probe.SlotCheck{Provider: p, Model: "m"}); got.Verdict != probe.Passed {
		t.Errorf("800 of 1000 cached = %+v, want passed", got)
	}
}

func TestCheckSlotAffinityFailsWhenTheServerStatesNoCachedCount(t *testing.T) {
	p := providertest.New(
		reply(providertest.Usage(800, 3, 803)),
		reply(providertest.Usage(30, 3, 33)),
		reply(providertest.Usage(830, 3, 833)),
	)
	got := probe.CheckSlotAffinity(context.Background(), probe.SlotCheck{Provider: p, Model: "m"})
	if got.Verdict != probe.Failed || !strings.Contains(got.Reason, "cached") {
		t.Errorf("slot affinity = %+v, want failed for want of a cached count", got)
	}
}

func TestCheckSlotAffinityFailsWhenARequestFails(t *testing.T) {
	for name, steps := range map[string][]providertest.Step{
		"the first":  {providertest.Fail(errors.New("boom"))},
		"the second": {reply(stated(800, 3, 0)), providertest.Fail(errors.New("boom"))},
		"the last":   {reply(stated(800, 3, 0)), reply(stated(30, 3, 0)), providertest.Fail(errors.New("boom"))},
	} {
		t.Run(name, func(t *testing.T) {
			got := probe.CheckSlotAffinity(context.Background(), probe.SlotCheck{Provider: providertest.New(steps...), Model: "m"})
			if got.Verdict != probe.Failed || !strings.Contains(got.Reason, "boom") {
				t.Errorf("slot affinity = %+v, want failed with the cause", got)
			}
		})
	}
}

func TestCheckSlotAffinityTakesALeaseForEachRequest(t *testing.T) {
	q := newQueue()
	w := &leaseWatcher{Provider: providertest.New(reply(stated(800, 3, 0)), reply(stated(30, 3, 0)), reply(stated(830, 3, 790))), q: q}
	got := probe.CheckSlotAffinity(context.Background(), probe.SlotCheck{
		Provider: w, Model: "m", Queue: q,
		Need: inference.Need{Pool: "gpu", Priority: inference.Maintenance, Endpoint: "llama"},
	})
	if got.Verdict != probe.Passed {
		t.Fatalf("slot affinity = %+v, want passed", got)
	}
	if len(w.held) != 3 {
		t.Errorf("leases held at each request = %v, want one for each of three", w.held)
	}
	if n := len(q.Snapshot()[0].Running); n != 0 {
		t.Errorf("%d leases left after the check", n)
	}
}

func TestASlotCheckThatNeverGetsItsTurnIsSkippedNotFailed(t *testing.T) {
	q := newQueue()
	user, err := q.Acquire(context.Background(), inference.Need{Pool: "gpu", Priority: inference.Interactive})
	if err != nil {
		t.Fatal(err)
	}
	defer user.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	p := providertest.New()
	got := probe.CheckSlotAffinity(ctx, probe.SlotCheck{
		Provider: p, Model: "m", Build: "b1", Queue: q,
		Need: inference.Need{Pool: "gpu", Priority: inference.Maintenance},
	})
	if got.Verdict != probe.Skipped || !got.Busy {
		t.Fatalf("slot affinity = %+v, want skipped as busy, not failed", got)
	}
	if p.Calls() != 0 {
		t.Error("the model was asked without a lease")
	}
	if got.Current("b1", "m") {
		t.Error("a check that never ran is current; it would never run again")
	}
}

func TestSlotAffinityIsCurrentForTheBuildAndModelItRanAgainst(t *testing.T) {
	c := probe.SlotAffinity{Verdict: probe.Passed, Build: "b1", Model: "m"}
	if !c.Current("b1", "m") || c.Current("b2", "m") || c.Current("b1", "other") {
		t.Errorf("Current: same %v, new build %v, new model %v; want true, false, false",
			c.Current("b1", "m"), c.Current("b2", "m"), c.Current("b1", "other"))
	}
	if (probe.SlotAffinity{}).Current("", "") {
		t.Error("a check that never ran is current")
	}
}
