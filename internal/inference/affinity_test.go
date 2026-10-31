package inference

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

// llamaPool is a pool of one llama.cpp endpoint with the given number of slots,
// admitting that many calls at once.
func llamaPool(slots int) Pool {
	return Pool{Name: "gpu", Slots: slots, Hinted: []SlotEndpoint{{Name: "llama", Slots: slots}}}
}

// turn is a call that continues the session's conversation.
func turn(session string, prompt int) Need {
	return Need{Pool: "gpu", Endpoint: "llama", SessionID: session, Affinity: true, Prompt: prompt, Tokens: prompt + 100}
}

// slotOf takes a lease for the session, reads the slot it names, and releases
// it, as a call that runs and ends does.
func slotOf(t *testing.T, q *Queue, session string) int {
	t.Helper()
	l := acquire(t, q, turn(session, 1000))
	slot, ok := l.SlotHint()
	if !ok {
		t.Fatalf("the call of %s names no slot", session)
	}
	l.Release()
	return slot
}

func TestASessionKeepsTheSlotItLastUsed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		a := acquire(t, q, turn("a", 1000))
		b := acquire(t, q, turn("b", 1000))
		slotA, _ := a.SlotHint()
		slotB, _ := b.SlotHint()
		if slotA == slotB {
			t.Fatalf("two sessions share slot %d while both run", slotA)
		}
		a.Release()
		b.Release()
		for round := 1; round <= 3; round++ {
			// The other order each round, so the lowest free slot is not
			// what keeps the answer.
			first, second := "b", "a"
			if round%2 == 0 {
				first, second = "a", "b"
			}
			slots := map[string]int{first: slotOf(t, q, first), second: slotOf(t, q, second)}
			if slots["a"] != slotA || slots["b"] != slotB {
				t.Fatalf("round %d: a on %d and b on %d, want %d and %d", round, slots["a"], slots["b"], slotA, slotB)
			}
		}
	})
}

func TestASessionKeepsItsSlotWhileTheOtherRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		slotA := slotOf(t, q, "a")
		slotB := slotOf(t, q, "b")
		// b runs on its slot for a long time; a returns to its own.
		b := acquire(t, q, turn("b", 1000))
		if got, _ := b.SlotHint(); got != slotB {
			t.Fatalf("b on %d, want %d", got, slotB)
		}
		if got := slotOf(t, q, "a"); got != slotA {
			t.Errorf("a on %d while b runs, want %d", got, slotA)
		}
		b.Release()
	})
}

func TestANewSessionTakesTheSlotOfTheOwnerIdleLongest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		slotA := slotOf(t, q, "a")
		time.Sleep(time.Second)
		slotB := slotOf(t, q, "b")
		time.Sleep(time.Second)
		if got := slotOf(t, q, "c"); got != slotA {
			t.Fatalf("c on %d, want %d, where a went longest without a call", got, slotA)
		}
		time.Sleep(time.Second)
		// a lost its prefix to c, so it is a new session again: the owner idle
		// longest is now b.
		if got := slotOf(t, q, "a"); got != slotB {
			t.Errorf("a on %d after c evicted it, want %d", got, slotB)
		}
		// And b, whose slot a took, is the one without a slot now.
		if got := slotOf(t, q, "b"); got != slotA {
			t.Errorf("b on %d after a evicted it, want %d", got, slotA)
		}
	})
}

func TestAnEmptySlotIsTakenBeforeOneThatHoldsAPrefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(3))
		a := slotOf(t, q, "a")
		b := slotOf(t, q, "b")
		c := slotOf(t, q, "c")
		if a == b || b == c || a == c {
			t.Fatalf("three sessions on slots %d, %d and %d, want three slots", a, b, c)
		}
	})
}

func TestACallThatSharesNothingWithASessionNamesNoSlotAndMovesNone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		slotA := slotOf(t, q, "a")
		slotB := slotOf(t, q, "b")

		time.Sleep(DefaultMaintenanceIdle) // a title waits for an idle pool
		title := acquire(t, q, Need{Pool: "gpu", Endpoint: "llama", SessionID: "a", Priority: Maintenance})
		if slot, ok := title.SlotHint(); ok {
			t.Errorf("a title named slot %d, want none: its prompt shares nothing with the session", slot)
		}
		title.Release()
		model := acquire(t, q, Need{Pool: "gpu", Endpoint: "llama"})
		if _, ok := model.SlotHint(); ok {
			t.Error("a call without a session named a slot")
		}
		model.Release()

		if got := slotOf(t, q, "a"); got != slotA {
			t.Errorf("a on %d after a title, want %d", got, slotA)
		}
		if got := slotOf(t, q, "b"); got != slotB {
			t.Errorf("b on %d after a title, want %d", got, slotB)
		}
	})
}

func TestACallOnAnEndpointWithoutHintsNamesNoSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 2, Hinted: []SlotEndpoint{{Name: "llama", Slots: 2}}})
		for _, endpoint := range []string{"vllm", ""} {
			n := turn("a", 1000)
			n.Endpoint = endpoint
			l := acquire(t, q, n)
			if slot, ok := l.SlotHint(); ok {
				t.Errorf("a call on endpoint %q named slot %d, want none", endpoint, slot)
			}
			l.Release()
		}
		q.SetPools([]Pool{{Name: "gpu", Slots: 2}})
		l := acquire(t, q, turn("a", 1000))
		if _, ok := l.SlotHint(); ok {
			t.Error("a pool that lists no hinted endpoint hinted a slot")
		}
		l.Release()
	})
}

func TestTheLeaseOfANilQueueNamesNoSlot(t *testing.T) {
	var q *Queue
	l, err := q.Acquire(context.Background(), turn("a", 1000))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.SlotHint(); ok {
		t.Error("the lease of a nil queue named a slot")
	}
}

func TestEndpointsOfOnePoolKeepSlotNumbersOfTheirOwn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 2, Hinted: []SlotEndpoint{{Name: "one", Slots: 2}, {Name: "two", Slots: 2}}})
		callOn := func(endpoint, session string) int {
			n := turn(session, 1000)
			n.Endpoint = endpoint
			l := acquire(t, q, n)
			slot, ok := l.SlotHint()
			if !ok {
				t.Fatalf("%s on %s named no slot", session, endpoint)
			}
			l.Release()
			return slot
		}
		// a is on slot 0 of one, then b takes one's other slot.
		if got := callOn("one", "a"); got != 0 {
			t.Fatalf("a on one = %d, want 0", got)
		}
		callOn("one", "b")
		// On two, a is a new session: nothing says a's prefix is there, and
		// slot 0 of one does not mean slot 0 of two. It gets whatever slot two
		// has free, and keeps it there.
		onTwo := callOn("two", "a")
		if again := callOn("two", "a"); again != onTwo {
			t.Errorf("a on two = %d then %d, want the same", onTwo, again)
		}
		if got := callOn("one", "a"); got != 0 {
			t.Errorf("a on one = %d after a call on two, want 0 still", got)
		}
	})
}

func TestAPoolMayMixAnEndpointWithHintsAndOneWithout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 2, Hinted: []SlotEndpoint{{Name: "llama", Slots: 2}}})
		llama := acquire(t, q, turn("a", 1000))
		other := turn("a", 1000)
		other.Endpoint = "tabby"
		tabby := acquire(t, q, other)
		if _, ok := llama.SlotHint(); !ok {
			t.Error("the llama.cpp call named no slot")
		}
		if slot, ok := tabby.SlotHint(); ok {
			t.Errorf("the call on the endpoint without hints named slot %d", slot)
		}
		llama.Release()
		tabby.Release()
	})
}

func TestAHintedEndpointMayHaveMoreSlotsThanThePoolRunsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Four sessions take turns, one at a time: each keeps its own slot,
		// because the server has the room to keep four prefixes.
		q := newQueue(Pool{Name: "gpu", Slots: 1, Hinted: []SlotEndpoint{{Name: "llama", Slots: 4}}})
		first := map[string]int{}
		for _, s := range []string{"a", "b", "c", "d"} {
			first[s] = slotOf(t, q, s)
		}
		seen := map[int]bool{}
		for s, slot := range first {
			if seen[slot] {
				t.Fatalf("session %s shares slot %d", s, slot)
			}
			seen[slot] = true
		}
		for _, s := range []string{"d", "b", "a", "c", "a"} {
			if got := slotOf(t, q, s); got != first[s] {
				t.Errorf("%s on %d, want %d", s, got, first[s])
			}
		}
	})
}

func TestAnAffineCallWaitsForTheBusySlotOfItsSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		// a's prefix is 4000 tokens, which the default rate reads in 2 s.
		running := acquire(t, q, turn("a", 4000))
		slotA, _ := running.SlotHint()
		start := time.Now()

		next := begin(context.Background(), q, turn("a", 4000))
		if next.admitted() {
			t.Fatal("a second call of a session was admitted beside the first while its slot is busy and a free slot would cost a full prefill")
		}
		time.Sleep(time.Second)
		running.Release()
		l := next.get(t)
		if got, _ := l.SlotHint(); got != slotA {
			t.Errorf("the second call named slot %d, want %d, which it waited for", got, slotA)
		}
		if waited := next.when().Sub(start); waited != time.Second {
			t.Errorf("waited %v, want the 1s the slot took to free", waited)
		}
		l.Release()
	})
}

func TestAnAffineCallStopsWaitingAfterAsLongAsReadingThePrefixTakes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		running := acquire(t, q, turn("a", 4000))
		slotA, _ := running.SlotHint()
		start := time.Now()

		next := begin(context.Background(), q, turn("a", 4000))
		time.Sleep(2*time.Second - time.Millisecond)
		if next.admitted() {
			t.Fatal("the call gave up before reading the prefix again would have been as slow as waiting")
		}
		time.Sleep(time.Millisecond)
		l := next.get(t)
		if waited := next.when().Sub(start); waited != 2*time.Second {
			t.Errorf("waited %v, want 2s: 4000 tokens at %d a second", waited, DefaultPrefillRate)
		}
		got, ok := l.SlotHint()
		if !ok || got == slotA {
			t.Fatalf("the call named slot %d (hint %v), want the other slot than %d", got, ok, slotA)
		}
		l.Release()
		running.Release()

		// The session's entry followed it: its next call goes to the new slot
		// while the old one is free, and does not wait for anything.
		if again := slotOf(t, q, "a"); again != got {
			t.Errorf("the next call went to slot %d, want %d, where the session moved", again, got)
		}
	})
}

func TestTheLongerThePrefixTheLongerTheCallWaitsForItsSlot(t *testing.T) {
	for _, tt := range []struct {
		prompt int
		want   time.Duration
	}{
		{prompt: 1000, want: 500 * time.Millisecond},
		{prompt: 20000, want: 10 * time.Second},
		{prompt: 100000, want: 50 * time.Second},
	} {
		synctest.Test(t, func(t *testing.T) {
			q := newQueue(llamaPool(2))
			running := acquire(t, q, turn("a", tt.prompt))
			start := time.Now()
			next := begin(context.Background(), q, turn("a", tt.prompt))
			time.Sleep(tt.want)
			l := next.get(t)
			if waited := next.when().Sub(start); waited != tt.want {
				t.Errorf("a prefix of %d tokens waited %v, want %v", tt.prompt, waited, tt.want)
			}
			l.Release()
			running.Release()
		})
	}
}

func TestAPolicyStatesHowFastTheServerReadsAPrefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		policy := q.Policy()
		policy.PrefillRate = 1000
		q.SetPolicy(policy)
		running := acquire(t, q, turn("a", 4000))
		start := time.Now()
		next := begin(context.Background(), q, turn("a", 4000))
		time.Sleep(4 * time.Second)
		next.get(t).Release()
		if waited := next.when().Sub(start); waited != 4*time.Second {
			t.Errorf("waited %v, want 4s at 1000 tokens a second", waited)
		}
		running.Release()
	})
}

func TestACallWaitingForItsSlotHoldsTheLine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		running := acquire(t, q, turn("a", 4000))
		slotA, _ := running.SlotHint()
		start := time.Now()

		waiting := begin(context.Background(), q, turn("a", 4000))
		settle()
		// b is a new session and the other slot is free, but the call ahead of
		// it is about to need that slot if its own does not come free.
		behind := begin(context.Background(), q, turn("b", 4000))
		if behind.admitted() {
			t.Fatal("a call behind one that waits for its slot was admitted into the room it would need")
		}
		time.Sleep(2 * time.Second)
		l := waiting.get(t)
		if got, _ := l.SlotHint(); got == slotA {
			t.Fatalf("the waiting call named the busy slot %d", got)
		}
		if at := waiting.when().Sub(start); at != 2*time.Second {
			t.Errorf("the waiting call was admitted at %v, want 2s", at)
		}
		l.Release()
		behind.get(t).Release()
		running.Release()
	})
}

func TestACallThatStopsWaitingForItsSlotFreesTheLine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		running := acquire(t, q, turn("a", 40000))
		ctx, cancel := context.WithCancel(context.Background())
		waiting := begin(ctx, q, turn("a", 40000))
		settle()
		behind := begin(context.Background(), q, turn("b", 1000))
		if behind.admitted() {
			t.Fatal("a call behind one that waits for its slot was admitted early")
		}
		cancel()
		if err := waiting.failed(); err == nil {
			t.Fatal("the cancelled call was admitted")
		}
		behind.get(t).Release()
		running.Release()
	})
}

func TestACallDoesNotWaitForASlotItsSessionNoLongerHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		slotA := slotOf(t, q, "a")
		slotB := slotOf(t, q, "b")
		// c takes a's slot, and runs a long time. a's prefix is gone with it:
		// a is a new session and takes the free slot at once.
		slotC := slotOf(t, q, "c")
		if slotC != slotA {
			t.Fatalf("c on %d, want %d", slotC, slotA)
		}
		c := acquire(t, q, turn("c", 90000))
		got := slotOf(t, q, "a")
		if got != slotB {
			t.Errorf("a on %d, want %d, the free slot", got, slotB)
		}
		c.Release()
	})
}

func TestACallThatContinuesASessionWithNothingToWaitForTakesAFreeSlotAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		a := acquire(t, q, turn("a", 1000))
		b := acquire(t, q, turn("b", 1000))
		// The pool is full: the third call waits in the queue as any call does,
		// and takes the first slot that frees, whoever held it.
		c := begin(context.Background(), q, turn("c", 1000))
		if c.admitted() {
			t.Fatal("a third call was admitted on a two-slot pool")
		}
		slotB, _ := b.SlotHint()
		b.Release()
		l := c.get(t)
		if got, _ := l.SlotHint(); got != slotB {
			t.Errorf("c on %d, want the slot %d that freed", got, slotB)
		}
		l.Release()
		a.Release()
	})
}

func TestSetPoolsKeepsWhatTheSlotsHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		slotA := slotOf(t, q, "a")
		slotB := slotOf(t, q, "b")
		q.SetPools([]Pool{llamaPool(2)})
		if got := slotOf(t, q, "a"); got != slotA {
			t.Errorf("a on %d after the pools were described again, want %d", got, slotA)
		}
		if got := slotOf(t, q, "b"); got != slotB {
			t.Errorf("b on %d after the pools were described again, want %d", got, slotB)
		}
	})
}

func TestSetPoolsGrowsAndShrinksTheBookOfAnEndpoint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		slotA := slotOf(t, q, "a")
		slotB := slotOf(t, q, "b")
		q.SetPools([]Pool{{Name: "gpu", Slots: 2, Hinted: []SlotEndpoint{{Name: "llama", Slots: 4}}}})
		if got := slotOf(t, q, "a"); got != slotA {
			t.Errorf("a on %d after the server grew, want %d", got, slotA)
		}
		if got := slotOf(t, q, "c"); got == slotA || got == slotB || got > 3 {
			t.Errorf("c on %d, want one of the new slots 2 and 3", got)
		}
		q.SetPools([]Pool{{Name: "gpu", Slots: 2, Hinted: []SlotEndpoint{{Name: "llama", Slots: 2}}}})
		for _, s := range []string{"a", "b", "c", "d"} {
			if got := slotOf(t, q, s); got > 1 {
				t.Errorf("%s on %d after the server shrank to two slots", s, got)
			}
		}
	})
}

func TestSnapshotSaysWhichRunningCallsNamedTheirSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(llamaPool(2))
		named := acquire(t, q, turn("a", 1000))
		unnamed := acquire(t, q, Need{Pool: "gpu", Endpoint: "llama", SessionID: "a"})
		var hinted []bool
		for _, r := range q.Snapshot()[0].Running {
			hinted = append(hinted, r.Hinted)
		}
		if len(hinted) != 2 || hinted[0] == hinted[1] {
			t.Errorf("hinted = %v, want one call that named its slot and one that did not", hinted)
		}
		named.Release()
		unnamed.Release()
	})
}

func TestPoolsListTheEndpointsThatTakeSlotHints(t *testing.T) {
	got := Pools([]Endpoint{
		{Name: "llama", Pool: "gpu", Slots: 2, SlotHints: true},
		{Name: "single", Pool: "gpu2", Slots: 1, SlotHints: true},
		{Name: "vllm", Pool: "gpu", Slots: 8},
		{Name: "big", Pool: "gpu", Slots: 4, SlotHints: true},
	})
	var gpu, gpu2 Pool
	for _, p := range got {
		switch p.Name {
		case "gpu":
			gpu = p
		case "gpu2":
			gpu2 = p
		}
	}
	want := []SlotEndpoint{{Name: "big", Slots: 4}, {Name: "llama", Slots: 2}}
	if !reflect.DeepEqual(gpu.Hinted, want) {
		t.Errorf("gpu hinted = %v, want %v: the endpoint without hints is left out", gpu.Hinted, want)
	}
	if gpu.Slots != 2 {
		t.Errorf("gpu slots = %d, want 2, the fewest the rows state", gpu.Slots)
	}
	if len(gpu2.Hinted) != 0 {
		t.Errorf("a one-slot endpoint is hinted: %v", gpu2.Hinted)
	}
}
