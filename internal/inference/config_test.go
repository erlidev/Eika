package inference

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestMoreSlotsAdmitWaitingCallsWithoutARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		a := acquire(t, q, Need{Pool: "gpu"})
		b := begin(context.Background(), q, Need{Pool: "gpu"})
		settle()
		if b.admitted() {
			t.Fatal("admitted on a full pool")
		}
		q.SetPools([]Pool{{Name: "gpu", Slots: 2}})
		l := b.get(t)
		a.Release()
		l.Release()
	})
}

func TestFewerSlotsLeaveRunningCallsAloneAndAdmitNothingUntilTheyFit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 3})
		a := acquire(t, q, Need{Pool: "gpu"})
		b := acquire(t, q, Need{Pool: "gpu"})
		c := acquire(t, q, Need{Pool: "gpu"})
		q.SetPools([]Pool{{Name: "gpu", Slots: 1}})
		for i, l := range []*Lease{a, b, c} {
			if l.Context().Err() != nil {
				t.Errorf("lease %d was cancelled by a change of settings", i)
			}
		}
		next := begin(context.Background(), q, Need{Pool: "gpu"})
		settle()
		a.Release()
		b.Release()
		if next.admitted() {
			t.Fatal("admitted with one slot while a call still ran")
		}
		c.Release()
		l := next.get(t)
		if l.Slot() != 0 {
			t.Errorf("slot = %d, want the lowest free number", l.Slot())
		}
		l.Release()
	})
}

func TestACacheChangeTakesEffectOnTheNextAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 4, KV: KVShared, Cache: 1000})
		a := acquire(t, q, Need{Pool: "gpu", Tokens: 600})
		b := begin(context.Background(), q, Need{Pool: "gpu", Tokens: 600})
		settle()
		if b.admitted() {
			t.Fatal("admitted into a full cache")
		}
		q.SetPools([]Pool{{Name: "gpu", Slots: 4, KV: KVShared, Cache: 2000}})
		l := b.get(t)
		q.SetPools([]Pool{{Name: "gpu", Slots: 4, KV: KVPartitioned}})
		c := acquire(t, q, Need{Pool: "gpu", Tokens: 100_000})
		a.Release()
		l.Release()
		c.Release()
	})
}

func TestAPoolTakenOutOfTheSettingsKeepsServingItsCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("old"), onePool("new"))
		held := acquire(t, q, Need{Pool: "old"})
		waiter := begin(context.Background(), q, Need{Pool: "old"})
		settle()
		q.SetPools([]Pool{onePool("new")})
		held.Release()
		waiter.get(t).Release()
		q.SetPools([]Pool{onePool("new")})
		for _, st := range q.Snapshot() {
			if st.Name == "old" {
				t.Error("an idle pool the settings no longer name is still listed")
			}
		}
	})
}

func TestAShorterIdleTimeAdmitsMaintenanceAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{MaintenanceIdle: time.Hour, Aging: time.Hour})
		acquire(t, q, Need{Pool: "gpu"}).Release()
		title := begin(context.Background(), q, Need{Pool: "gpu", Priority: Maintenance})
		settle()
		if title.admitted() {
			t.Fatal("admitted before the idle time")
		}
		q.SetPolicy(Policy{MaintenanceIdle: 0, Aging: time.Hour})
		title.get(t).Release()
	})
}

func TestAShorterAgingPeriodRaisesWaitingCallsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(onePool("gpu"))
		q.SetPolicy(Policy{Aging: time.Hour})
		held := acquire(t, q, Need{Pool: "gpu"})
		old := begin(context.Background(), q, Need{Pool: "gpu", Priority: Background})
		settle()
		time.Sleep(time.Minute)
		fresh := begin(context.Background(), q, Need{Pool: "gpu", Priority: Interactive})
		settle()
		q.SetPolicy(Policy{Aging: 30 * time.Second}) // the old call has now earned two levels
		held.Release()
		l := old.get(t)
		if fresh.admitted() {
			t.Error("the call that had aged was not first")
		}
		l.Release()
		fresh.get(t).Release()
	})
}

func TestUnusablePolicyValuesFallBack(t *testing.T) {
	q := newQueue()
	q.SetPolicy(Policy{MaintenanceIdle: -time.Second, Aging: 0})
	if got := q.Policy(); got.Aging != DefaultAging || got.MaintenanceIdle != 0 {
		t.Errorf("policy = %+v", got)
	}
	if got := New(nil).Policy(); got != DefaultPolicy() {
		t.Errorf("a new queue's policy = %+v, want the default", got)
	}
}
