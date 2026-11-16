package inference

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

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
		time.Sleep(time.Second)
		l := title.get(t)
		if got := title.when().Sub(released); got != 120*time.Second {
			t.Errorf("admitted %v after the pool went idle, want 120s", got)
		}
		l.Release()
	})
}

func TestARankOnlyRises(t *testing.T) {
	var unset *Rank
	if unset.Priority() != Interactive {
		t.Errorf("nil rank = %v, want interactive", unset.Priority())
	}
	r := NewRank(Background)
	if !r.Raise(Interactive) || r.Raise(Background) || r.Priority() != Interactive {
		t.Errorf("rank = %v, want raised to interactive and kept there", r.Priority())
	}
}
