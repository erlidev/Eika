package inference

import (
	"context"
	"testing"
	"testing/synctest"
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
