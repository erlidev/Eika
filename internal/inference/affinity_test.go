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
