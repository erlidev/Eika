package inference

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func preemptPolicy() Policy {
	return Policy{PreemptRunning: true, Aging: time.Hour}
}

func TestRunningCallsAreNotCancelledByDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		bg := acquire(t, q, Need{Pool: "gpu", Priority: Background})
		user := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive})
		settle()
		time.Sleep(time.Hour / 2)
		if bg.Context().Err() != nil {
			t.Fatal("a running call was cancelled with PreemptRunning off")
		}
		if user.admitted() {
			t.Fatal("the interactive call was admitted with no slot free")
		}
		bg.Release()
		user.get(t).Release()
	})
}

func TestAnInteractiveCallCancelsARunningBackgroundCallWhenNoSlotIsFree(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(preemptPolicy())
		bg := acquire(t, q, Need{Pool: "gpu", Priority: Background})
		user := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive})
		settle()

		if !bg.Preempted() {
			t.Fatal("the running background call was not cancelled")
		}
		if cause := context.Cause(bg.Context()); !errors.Is(cause, ErrPreempted) {
			t.Errorf("cause = %v, want ErrPreempted", cause)
		}
		// The slot is not free until the victim lets go.
		if user.admitted() {
			t.Fatal("the interactive call was admitted before the cancelled call released")
		}
		if st := q.Snapshot()[0]; !st.Running[0].Preempted {
			t.Error("the state does not show the call as being preempted")
		}
		bg.Release()
		user.get(t).Release()
	})
}

func TestMaintenanceIsPreemptedBeforeBackground(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 2})
		q.SetPolicy(preemptPolicy())
		bg := acquire(t, q, Need{Pool: "gpu", Priority: Background})
		maint := acquire(t, q, Need{Pool: "gpu", Priority: Maintenance})
		user := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive})
		settle()
		if !maint.Preempted() || bg.Preempted() {
			t.Fatalf("preempted: maintenance %v, background %v; want only maintenance", maint.Preempted(), bg.Preempted())
		}
		maint.Release()
		user.get(t).Release()
		bg.Release()
	})
}

func TestTheNewestOfEqualVictimsLosesLeast(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 2})
		q.SetPolicy(preemptPolicy())
		older := acquire(t, q, Need{Pool: "gpu", Priority: Background})
		time.Sleep(time.Second)
		newer := acquire(t, q, Need{Pool: "gpu", Priority: Background})
		user := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive})
		settle()
		if !newer.Preempted() || older.Preempted() {
			t.Fatalf("preempted: older %v, newer %v; want the newer, which has the least prefill to lose", older.Preempted(), newer.Preempted())
		}
		newer.Release()
		user.get(t).Release()
		older.Release()
	})
}

func TestAFreeSlotMeansNothingIsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 2})
		q.SetPolicy(preemptPolicy())
		bg := acquire(t, q, Need{Pool: "gpu", Priority: Background})
		acquire(t, q, Need{Pool: "gpu", Priority: Interactive}).Release()
		if bg.Preempted() {
			t.Error("a call was cancelled although a slot was free")
		}
		bg.Release()
	})
}

func TestOnlyAnInteractiveCallPreempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(preemptPolicy())
		bg := acquire(t, q, Need{Pool: "gpu", Priority: Maintenance})
		fg := begin(context.Background(), q, Need{Pool: "gpu", Priority: Foreground})
		settle()
		if bg.Preempted() {
			t.Error("a foreground call preempted a running call")
		}
		bg.Release()
		fg.get(t).Release()
	})
}

func TestInteractiveAndForegroundCallsAreNeverPreempted(t *testing.T) {
	for _, p := range []Priority{Interactive, Foreground} {
		t.Run(p.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				q := newQueue(onePool("gpu"))
				q.SetPolicy(preemptPolicy())
				running := acquire(t, q, Need{Pool: "gpu", Priority: p})
				user := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive})
				settle()
				if running.Preempted() {
					t.Errorf("a running %s call was cancelled", p)
				}
				running.Release()
				user.get(t).Release()
			})
		})
	}
}

func TestACallThatAgedIntoInteractiveIsNotPreempted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{PreemptRunning: true, Aging: 100 * time.Second})
		held := acquire(t, q, Need{Pool: "gpu", Priority: Interactive})
		bg := begin(context.Background(), q, Need{Pool: "gpu", Priority: Background})
		settle()
		time.Sleep(200 * time.Second) // earned interactive
		held.Release()
		l := bg.get(t)

		user := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive})
		settle()
		if l.Preempted() {
			t.Error("a call that waited its way up to interactive was cancelled for an interactive one; it would never finish")
		}
		l.Release()
		user.get(t).Release()
	})
}

func TestOneWaiterCancelsOnlyAsMuchAsItNeeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 3})
		q.SetPolicy(preemptPolicy())
		a := acquire(t, q, Need{Pool: "gpu", Priority: Background})
		b := acquire(t, q, Need{Pool: "gpu", Priority: Background})
		c := acquire(t, q, Need{Pool: "gpu", Priority: Background})
		user := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive})
		settle()
		n := 0
		for _, l := range []*Lease{a, b, c} {
			if l.Preempted() {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%d calls cancelled for one waiting call, want 1", n)
		}
		// The same waiter is looked at again when anything changes; the
		// victim already on its way out is not a reason for another.
		extra := begin(context.Background(), q, Need{Pool: "gpu", Priority: Background})
		settle()
		n = 0
		for _, l := range []*Lease{a, b, c} {
			if l.Preempted() {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%d calls cancelled after another arrival, want still 1", n)
		}
		for _, l := range []*Lease{a, b, c} {
			l.Release()
		}
		user.get(t).Release()
		extra.get(t).Release()
	})
}

func TestACallThatNeedsCacheRoomCancelsEnoughToMakeIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 4, KV: KVShared, Cache: 1000})
		q.SetPolicy(preemptPolicy())
		keep := acquire(t, q, Need{Pool: "gpu", Priority: Interactive, Tokens: 500})
		small := acquire(t, q, Need{Pool: "gpu", Priority: Background, Tokens: 100})
		big := acquire(t, q, Need{Pool: "gpu", Priority: Background, Tokens: 300})
		user := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive, Tokens: 350})
		settle()
		// A free slot, but 900 of 1000 tokens in use: 350 fits once the
		// 300 call is gone, and the 100 call can stay.
		if !big.Preempted() || small.Preempted() {
			t.Fatalf("preempted: big %v, small %v; want only the 300-token call", big.Preempted(), small.Preempted())
		}
		big.Release()
		user.get(t).Release()
		small.Release()
		keep.Release()
	})
}

func TestNothingIsCancelledThatWouldNotMakeRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 4, KV: KVShared, Cache: 1000})
		q.SetPolicy(preemptPolicy())
		keep := acquire(t, q, Need{Pool: "gpu", Priority: Interactive, Tokens: 800})
		bg := acquire(t, q, Need{Pool: "gpu", Priority: Background, Tokens: 100})
		user := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive, Tokens: 500})
		settle()
		if bg.Preempted() {
			t.Error("a call was cancelled although the waiting one would not fit without the interactive call as well")
		}
		keep.Release()
		l := user.get(t)
		l.Release()
		bg.Release()
	})
}

func TestDoMakesAPreemptedCallAgainWithANewLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(preemptPolicy())
		attempts := 0
		started := make(chan struct{}, 4)
		result := make(chan error, 1)
		go func() {
			result <- q.Do(context.Background(), Need{Pool: "gpu", Priority: Maintenance}, func(ctx context.Context, _ *Lease) error {
				attempts++
				started <- struct{}{}
				if attempts == 1 {
					<-ctx.Done() // the first attempt is cancelled mid-call
					return ctx.Err()
				}
				return nil
			})
		}()
		<-started
		user := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive})
		settle()
		// The user's call has the slot; the retry waits behind it.
		l := user.get(t)
		if attempts != 1 {
			t.Fatalf("attempts = %d while the interactive call runs, want the retry to wait", attempts)
		}
		time.Sleep(time.Second)
		l.Release()
		if err := <-result; err != nil {
			t.Fatalf("Do = %v, want the retry to succeed", err)
		}
		if attempts != 2 {
			t.Errorf("attempts = %d, want 2", attempts)
		}
	})
}

func TestDoStopsRetryingWhenItsContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(preemptPolicy())
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		started := make(chan struct{})
		go func() {
			result <- q.Do(ctx, Need{Pool: "gpu", Priority: Background}, func(ctx context.Context, _ *Lease) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			})
		}()
		<-started
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("Do = %v, want context.Canceled", err)
		}
	})
}
