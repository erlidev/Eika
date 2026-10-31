package inference

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestAWaitingCallGainsOneLevelPerAgingPeriod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{Aging: 100 * time.Second})
		held := acquire(t, q, Need{Pool: "gpu", Priority: Interactive})
		old := begin(context.Background(), q, Need{Pool: "gpu", Priority: Maintenance})
		settle()

		earned := func() Priority {
			settle()
			return q.Snapshot()[0].Waiting[0].Earned
		}
		for i, want := range []Priority{Maintenance, Background, Foreground, Interactive, Interactive} {
			if got := earned(); got != want {
				t.Fatalf("after %d aging periods the call is %s, want %s", i, got, want)
			}
			time.Sleep(100 * time.Second)
		}

		// Aged to interactive, it is ahead of an interactive call that
		// arrived later.
		fresh := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive})
		settle()
		held.Release()
		l := old.get(t)
		if fresh.admitted() {
			t.Error("a fresh interactive call was admitted ahead of a call that had aged to interactive")
		}
		l.Release()
		fresh.get(t).Release()
	})
}

func TestACallThatAgedPastAFresherOneOfItsOwnLevelGoesFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{Aging: 100 * time.Second})
		held := acquire(t, q, Need{Pool: "gpu"})
		background := begin(context.Background(), q, Need{Pool: "gpu", Priority: Background})
		settle()
		time.Sleep(100 * time.Second) // background is now foreground
		foreground := begin(context.Background(), q, Need{Pool: "gpu", Priority: Foreground})
		settle()
		held.Release()
		l := background.get(t)
		if foreground.admitted() {
			t.Error("the call that had waited longer at the same level was not first")
		}
		l.Release()
		foreground.get(t).Release()
	})
}

func TestAStreamOfInteractiveCallsCannotStarveABackgroundOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const aging = 60 * time.Second
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{Aging: aging})

		ctx, stop := context.WithCancel(context.Background())
		var producers sync.WaitGroup
		defer producers.Wait()
		defer stop()
		// Two producers keep an interactive call waiting at all times: each
		// call takes a second, and a new one is asked for as one ends.
		for range 2 {
			producers.Go(func() {
				for ctx.Err() == nil {
					l, err := q.Acquire(ctx, Need{Pool: "gpu", Priority: Interactive})
					if err != nil {
						return
					}
					time.Sleep(time.Second)
					l.Release()
				}
			})
		}
		settle()

		start := time.Now()
		bg := begin(context.Background(), q, Need{Pool: "gpu", Priority: Background})
		settle()
		// Background ages to foreground after one period and interactive
		// after two: from then on it is ahead of every call that arrives.
		for !bg.admitted() && time.Since(start) < 10*aging {
			time.Sleep(time.Second)
		}
		if !bg.admitted() {
			t.Fatal("the background call was starved by a stream of interactive calls")
		}
		if waited := bg.when().Sub(start); waited > 2*aging+2*time.Second {
			t.Errorf("the background call waited %v; want at most two aging periods and a call", waited)
		}
		bg.get(t).Release()
	})
}

func TestMaintenanceWaitsForTheIdleTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{MaintenanceIdle: 120 * time.Second, Aging: time.Hour})
		user := acquire(t, q, Need{Pool: "gpu", Priority: Interactive})
		time.Sleep(30 * time.Second)
		title := begin(context.Background(), q, Need{Pool: "gpu", Priority: Maintenance})
		settle()
		user.Release()
		released := time.Now()

		time.Sleep(119 * time.Second)
		if title.admitted() {
			t.Fatal("a maintenance call ran before the pool had been idle for the idle time")
		}
		if st := q.Snapshot()[0]; len(st.Waiting) != 1 || !st.Waiting[0].Gated {
			t.Errorf("waiting = %+v, want the call shown as waiting for idle", st.Waiting)
		}
		time.Sleep(time.Second)
		l := title.get(t)
		if got := title.when().Sub(released); got != 120*time.Second {
			t.Errorf("admitted %v after the pool went idle, want 120s", got)
		}
		l.Release()
	})
}

func TestAnIdlePoolAdmitsMaintenanceAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		acquire(t, q, Need{Pool: "gpu", Priority: Maintenance}).Release()
	})
}

func TestMaintenanceCallsDoNotKeepAPoolFromBeingIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 3})
		q.SetPolicy(Policy{MaintenanceIdle: 120 * time.Second, Aging: time.Hour})
		user := acquire(t, q, Need{Pool: "gpu"})
		user.Release()
		later := begin(context.Background(), q, Need{Pool: "gpu", Priority: Maintenance})
		more := begin(context.Background(), q, Need{Pool: "gpu", Priority: Maintenance})
		settle()
		time.Sleep(120 * time.Second)
		// Both run together: the first one's lease is not activity the
		// second has to wait out.
		a, b := later.get(t), more.get(t)
		a.Release()
		b.Release()
	})
}

func TestAPoolWithARunningCallIsNotIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 2})
		q.SetPolicy(Policy{MaintenanceIdle: 10 * time.Second, Aging: time.Hour})
		user := acquire(t, q, Need{Pool: "gpu", Priority: Background})
		title := begin(context.Background(), q, Need{Pool: "gpu", Priority: Maintenance})
		settle()
		time.Sleep(time.Hour / 2)
		if title.admitted() {
			t.Fatal("a maintenance call ran beside a call that has been running for half an hour")
		}
		user.Release()
		time.Sleep(10 * time.Second)
		title.get(t).Release()
	})
}

func TestMaintenanceOnABusyPoolIsNotBlockedForever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const aging = 300 * time.Second
		q := newQueue(Pool{Name: "gpu", Slots: 2})
		q.SetPolicy(Policy{MaintenanceIdle: 120 * time.Second, Aging: aging})

		ctx, stop := context.WithCancel(context.Background())
		var producer sync.WaitGroup
		defer producer.Wait()
		defer stop()
		// The pool is never idle: a background call runs back to back on
		// one slot, with the other free.
		producer.Go(func() {
			for ctx.Err() == nil {
				l, err := q.Acquire(ctx, Need{Pool: "gpu", Priority: Background})
				if err != nil {
					return
				}
				time.Sleep(10 * time.Second)
				l.Release()
			}
		})
		settle()

		start := time.Now()
		title := begin(context.Background(), q, Need{Pool: "gpu", Priority: Maintenance})
		settle()
		time.Sleep(aging - time.Second)
		if title.admitted() {
			t.Fatal("the maintenance call ran before it aged, on a pool that was never idle")
		}
		time.Sleep(time.Second)
		l := title.get(t)
		if got := title.when().Sub(start); got != aging {
			t.Errorf("admitted after %v, want one aging period (%v)", got, aging)
		}
		l.Release()
	})
}

func TestMaintenanceWithNoIdleTimeIsOrderedLast(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{MaintenanceIdle: 0, Aging: time.Hour})
		held := acquire(t, q, Need{Pool: "gpu"})
		maintenance := begin(context.Background(), q, Need{Pool: "gpu", Priority: Maintenance})
		settle()
		background := begin(context.Background(), q, Need{Pool: "gpu", Priority: Background})
		settle()
		held.Release()
		l := background.get(t)
		if maintenance.admitted() {
			t.Error("maintenance was admitted ahead of a background call")
		}
		l.Release()
		maintenance.get(t).Release()
	})
}

func TestAnAgedCallKeepsItsAgeAcrossARetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{Aging: 100 * time.Second})
		first := time.Now()
		held := acquire(t, q, Need{Pool: "gpu"})
		time.Sleep(200 * time.Second)
		// A retry of a call that began waiting 200s ago: it is already
		// interactive, whatever the attempt's own wait.
		retry := begin(context.Background(), q, Need{Pool: "gpu", Priority: Background, Since: first})
		settle()
		rival := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive})
		settle()
		held.Release()
		l := retry.get(t)
		if rival.admitted() {
			t.Error("a retry lost the aging its first attempt earned")
		}
		l.Release()
		rival.get(t).Release()
	})
}

func TestARaisedRankChangesTheNextCallOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{Aging: time.Hour})
		rank := NewRank(Background)

		// A run asks for each lease at the rank it has then.
		call := func() *pending {
			return begin(context.Background(), q, Need{Pool: "gpu", Priority: rank.Priority()})
		}
		held := acquire(t, q, Need{Pool: "gpu"})
		first := call()
		settle()

		// A user message joins the run: the call already waiting stays as it
		// was, and the next one is interactive.
		if !rank.Raise(Interactive) {
			t.Fatal("Raise reported no change")
		}
		other := begin(context.Background(), q, Need{Pool: "gpu", Priority: Foreground})
		settle()
		second := call()
		settle()
		if got := q.Snapshot()[0].Waiting; len(got) != 3 || got[0].Priority != Interactive || got[1].Priority != Foreground || got[2].Priority != Background {
			t.Fatalf("waiting = %+v, want interactive, foreground, background", got)
		}
		if rank.Raise(Background) {
			t.Error("Raise lowered the rank")
		}
		if rank.Priority() != Interactive {
			t.Errorf("rank = %v after an attempt to lower it, want interactive", rank.Priority())
		}

		held.Release()
		for _, p := range []*pending{second, other, first} {
			p.get(t).Release()
		}
	})
}

func TestARankNobodySetIsInteractive(t *testing.T) {
	var r *Rank
	if r.Priority() != Interactive {
		t.Errorf("nil rank = %v, want interactive", r.Priority())
	}
	if r.Raise(Interactive) {
		t.Error("a nil rank changed")
	}
}

func TestPriorityNames(t *testing.T) {
	for p := Interactive; p <= Maintenance; p++ {
		back, ok := ParsePriority(p.String())
		if !ok || back != p {
			t.Errorf("ParsePriority(%q) = %v, %v", p.String(), back, ok)
		}
	}
	if _, ok := ParsePriority("urgent"); ok {
		t.Error("ParsePriority accepted a made-up name")
	}
}
