package inference

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestPoolAdmitsAsManyLeasesAsSlots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 2})
		a := acquire(t, q, Need{Pool: "gpu"})
		b := acquire(t, q, Need{Pool: "gpu"})
		if a.Slot() == b.Slot() {
			t.Errorf("two leases hold slot %d", a.Slot())
		}
		c := begin(context.Background(), q, Need{Pool: "gpu"})
		if c.admitted() {
			t.Fatal("a third call was admitted on a two-slot pool")
		}
		a.Release()
		l := c.get(t)
		if l.Slot() != a.Slot() {
			t.Errorf("slot = %d, want the released slot %d", l.Slot(), a.Slot())
		}
		b.Release()
		l.Release()
	})
}

func TestPoolsAreIndependent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("a"), onePool("b"))
		x := acquire(t, q, Need{Pool: "a"})
		y := acquire(t, q, Need{Pool: "b"})
		blocked := begin(context.Background(), q, Need{Pool: "a"})
		if blocked.admitted() {
			t.Fatal("a second call was admitted on a full pool")
		}
		x.Release()
		blocked.get(t).Release()
		y.Release()
	})
}

func TestReleasedLeaseWakesTheMostUrgentWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{Aging: time.Hour})
		held := acquire(t, q, Need{Pool: "gpu"})
		order := []Priority{Maintenance, Background, Interactive, Foreground}
		waiters := map[Priority]*pending{}
		for _, p := range order {
			waiters[p] = begin(context.Background(), q, Need{Pool: "gpu", Priority: p})
			settle()
		}
		held.Release()
		served := []Priority{Interactive, Foreground, Background, Maintenance}
		for i, p := range served {
			l := waiters[p].get(t)
			for _, later := range served[i+1:] {
				if waiters[later].admitted() {
					t.Fatalf("%s was admitted while %s was still ahead of it", later, p)
				}
			}
			l.Release()
		}
	})
}

func TestEqualPrioritiesAreServedInArrivalOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		held := acquire(t, q, Need{Pool: "gpu"})
		first := begin(context.Background(), q, Need{Pool: "gpu", Priority: Background})
		settle()
		time.Sleep(time.Second)
		second := begin(context.Background(), q, Need{Pool: "gpu", Priority: Background})
		settle()
		held.Release()
		l := first.get(t)
		if second.admitted() {
			t.Fatal("the later call was admitted first")
		}
		l.Release()
		second.get(t).Release()
	})
}

func TestReleaseTwiceReleasesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		a := acquire(t, q, Need{Pool: "gpu"})
		next := begin(context.Background(), q, Need{Pool: "gpu"})
		settle()
		blocked := begin(context.Background(), q, Need{Pool: "gpu"})
		settle()
		a.Release()
		l := next.get(t)
		a.Release() // must not free the slot the next call now holds
		if blocked.admitted() {
			t.Fatal("a second Release of one lease freed another call's slot")
		}
		l.Release()
		blocked.get(t).Release()
	})
}

func TestCancelledWaiterLeavesTheQueueAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		held := acquire(t, q, Need{Pool: "gpu"})
		ctx, cancel := context.WithCancel(context.Background())
		w := begin(ctx, q, Need{Pool: "gpu", Priority: Background})
		settle()
		if got := waiting(q, "gpu"); got != 1 {
			t.Fatalf("waiting = %d, want 1", got)
		}
		cancel()
		if err := w.failed(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Acquire = %v, want context.Canceled", err)
		}
		if got := waiting(q, "gpu"); got != 0 {
			t.Errorf("waiting after cancel = %d, want 0", got)
		}
		held.Release()
		// The slot is free for the next call: the cancelled one took nothing.
		acquire(t, q, Need{Pool: "gpu"}).Release()
	})
}

func TestAcquireWithAnEndedContextWaitsForNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := q.Acquire(ctx, Need{Pool: "gpu"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Acquire = %v, want context.Canceled", err)
		}
		if got := waiting(q, "gpu"); got != 0 {
			t.Errorf("waiting = %d, want 0", got)
		}
		// Nothing was taken from the pool.
		acquire(t, q, Need{Pool: "gpu"}).Release()
	})
}

func TestCancelledHeadOfLineLetsTheNextCallIn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 2, KV: KVShared, Cache: 100})
		running := acquire(t, q, Need{Pool: "gpu", Tokens: 80})
		ctx, cancel := context.WithCancel(context.Background())
		big := begin(ctx, q, Need{Pool: "gpu", Tokens: 60})
		settle()
		small := begin(context.Background(), q, Need{Pool: "gpu", Tokens: 10})
		if small.admitted() {
			t.Fatal("a call overtook the large one waiting ahead of it")
		}
		cancel()
		big.failed()
		small.get(t).Release()
		running.Release()
	})
}

func TestLeaseContextEndsWithTheCallersContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		ctx, cancel := context.WithCancel(context.Background())
		l, err := q.Acquire(ctx, Need{Pool: "gpu"})
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		<-l.Context().Done()
		if l.Preempted() {
			t.Error("a cancelled caller was reported as preemption")
		}
		l.Release()
	})
}

func TestReleaseEndsTheLeaseContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		l := acquire(t, q, Need{Pool: "gpu"})
		ctx := l.Context()
		if ctx.Err() != nil {
			t.Fatal("the context ended before the release")
		}
		l.Release()
		if ctx.Err() == nil {
			t.Error("the context outlived the release")
		}
	})
}

func TestDoReleasesTheLeaseWhenTheCallPanics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		func() {
			defer func() {
				if recover() == nil {
					t.Error("the panic was swallowed")
				}
			}()
			_ = q.Do(context.Background(), Need{Pool: "gpu"}, func(context.Context, *Lease) error {
				panic("the provider blew up")
			})
		}()
		acquire(t, q, Need{Pool: "gpu"}).Release()
	})
}

func TestDoReleasesTheLeaseWhenTheCallFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		boom := errors.New("boom")
		err := q.Do(context.Background(), Need{Pool: "gpu"}, func(context.Context, *Lease) error { return boom })
		if !errors.Is(err, boom) {
			t.Fatalf("Do = %v, want the call's error", err)
		}
		acquire(t, q, Need{Pool: "gpu"}).Release()
	})
}

func TestLeaseReportsWhatItWaited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		held := acquire(t, q, Need{Pool: "gpu"})
		w := begin(context.Background(), q, Need{Pool: "gpu", Priority: Foreground})
		settle()
		time.Sleep(1500 * time.Millisecond)
		held.Release()
		l := w.get(t)
		if got := l.Waited(); got != 1500*time.Millisecond {
			t.Errorf("Waited = %v, want 1.5s", got)
		}
		if l.Priority() != Foreground {
			t.Errorf("Priority = %v, want foreground", l.Priority())
		}
		if !l.Queued() {
			t.Error("a lease of a queue reports it is not queued")
		}
		l.Release()
	})
}

func TestNilQueueAdmitsEverything(t *testing.T) {
	var q *Queue
	l, err := q.Acquire(context.Background(), Need{Pool: "gpu"})
	if err != nil {
		t.Fatal(err)
	}
	if l.Queued() || l.Waited() != 0 || l.Context() == nil {
		t.Errorf("lease of a nil queue = queued %v, waited %v", l.Queued(), l.Waited())
	}
	l.Release()
	l.Release()
	ran := false
	if err := q.Do(context.Background(), Need{}, func(context.Context, *Lease) error { ran = true; return nil }); err != nil || !ran {
		t.Errorf("Do on a nil queue = %v, ran %v", err, ran)
	}
	if q.Snapshot() != nil {
		t.Error("a nil queue has state")
	}
}

func TestACallNamingNoPoolRunsOnOneSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue()
		a := acquire(t, q, Need{})
		b := begin(context.Background(), q, Need{})
		if b.admitted() {
			t.Fatal("an undescribed pool admitted two calls")
		}
		a.Release()
		b.get(t).Release()
	})
}

func TestSnapshotShowsWhatHoldsAPoolAndWhatWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 1, KV: KVShared, Cache: 1000, Endpoints: []string{"llama"}})
		held := acquire(t, q, Need{Pool: "gpu", Endpoint: "llama", Class: "main", SessionID: "s1", Priority: Foreground, Tokens: 300})
		w := begin(context.Background(), q, Need{Pool: "gpu", Endpoint: "llama", SessionID: "s2", Priority: Background, Tokens: 100})
		settle()
		time.Sleep(10 * time.Second)

		states := q.Snapshot()
		if len(states) != 1 {
			t.Fatalf("pools = %d, want 1", len(states))
		}
		st := states[0]
		if st.Name != "gpu" || st.Slots != 1 || st.Cache != 1000 || st.Used != 300 || st.Idle {
			t.Errorf("pool = %+v", st)
		}
		if len(st.Running) != 1 || st.Running[0].SessionID != "s1" || st.Running[0].Priority != Foreground ||
			st.Running[0].Tokens != 300 || st.Running[0].Held != 10*time.Second || st.Running[0].Class != "main" {
			t.Errorf("running = %+v", st.Running)
		}
		if len(st.Waiting) != 1 || st.Waiting[0].SessionID != "s2" || st.Waiting[0].Waited != 10*time.Second ||
			st.Waiting[0].Priority != Background || st.Waiting[0].Gated {
			t.Errorf("waiting = %+v", st.Waiting)
		}
		held.Release()
		w.get(t).Release()
	})
}
