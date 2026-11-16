package inference

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// TestPreemption runs calls on a pool, then asks for one more, and checks
// which running calls are cancelled to make room for it.
func TestPreemption(t *testing.T) {
	shared := Pool{Name: "gpu", Slots: 4, KV: KVShared, Cache: 1000}
	type run struct {
		p      Priority
		tokens int
	}
	for _, tt := range []struct {
		name    string
		off     bool
		pool    Pool
		running []run // started a second apart, oldest first
		waiter  Need
		victims []int
	}{
		{name: "off by default", off: true, pool: onePool("gpu"),
			running: []run{{p: Background}}, waiter: Need{Priority: Interactive}},
		{name: "interactive takes a background call's slot", pool: onePool("gpu"),
			running: []run{{p: Background}}, waiter: Need{Priority: Interactive}, victims: []int{0}},
		{name: "only an interactive call preempts", pool: onePool("gpu"),
			running: []run{{p: Maintenance}}, waiter: Need{Priority: Foreground}},
		{name: "foreground is never a victim", pool: onePool("gpu"),
			running: []run{{p: Foreground}}, waiter: Need{Priority: Interactive}},
		{name: "a free slot cancels nothing", pool: Pool{Name: "gpu", Slots: 2},
			running: []run{{p: Background}}, waiter: Need{Priority: Interactive}},
		{name: "maintenance goes before background", pool: Pool{Name: "gpu", Slots: 2},
			running: []run{{p: Background}, {p: Maintenance}}, waiter: Need{Priority: Interactive}, victims: []int{1}},
		{name: "the newest of equals loses least", pool: Pool{Name: "gpu", Slots: 3},
			running: []run{{p: Background}, {p: Background}, {p: Background}}, waiter: Need{Priority: Interactive}, victims: []int{2}},
		{name: "just enough cache room", pool: shared,
			running: []run{{Interactive, 500}, {Background, 100}, {Background, 300}}, waiter: Need{Priority: Interactive, Tokens: 350}, victims: []int{2}},
		{name: "nothing that would not make room", pool: shared,
			running: []run{{Interactive, 800}, {Background, 100}}, waiter: Need{Priority: Interactive, Tokens: 500}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				q := newQueue(tt.pool)
				q.SetPolicy(Policy{PreemptRunning: !tt.off, Aging: time.Hour})
				var leases []*Lease
				for _, r := range tt.running {
					leases = append(leases, acquire(t, q, Need{Pool: "gpu", Priority: r.p, Tokens: r.tokens}))
					time.Sleep(time.Second)
				}
				tt.waiter.Pool = "gpu"
				w := begin(context.Background(), q, tt.waiter)
				settle()
				var victims []int
				for i, l := range leases {
					if l.Preempted() {
						victims = append(victims, i)
						if !errors.Is(context.Cause(l.Context()), ErrPreempted) {
							t.Errorf("call %d cancelled with %v, want ErrPreempted", i, context.Cause(l.Context()))
						}
					}
				}
				if !slices.Equal(victims, tt.victims) {
					t.Fatalf("preempted %v, want %v", victims, tt.victims)
				}
				if len(victims) > 0 && w.admitted() {
					t.Fatal("the waiter was admitted before its victim let go")
				}
				for _, l := range leases {
					l.Release()
				}
				w.get(t).Release()
			})
		})
	}
}

func TestDoMakesAPreemptedCallAgainWithANewLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{PreemptRunning: true, Aging: time.Hour})
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
		l := user.get(t)
		l.Release()
		if err := <-result; err != nil || attempts != 2 {
			t.Fatalf("Do = %v after %d attempts, want the retry to succeed", err, attempts)
		}
	})
}
