package inference

import (
	"cmp"
	"slices"
	"time"
)

// PoolState is what holds one pool and what waits behind it, at one moment.
type PoolState struct {
	Pool
	// Used is the tokens the running calls are counted at.
	Used int
	// Idle reports that the pool has run nothing but maintenance for the
	// policy's idle time, and IdleFor how long it has been so, zero while it
	// is not idle and when it has never run anything else.
	Idle    bool
	IdleFor time.Duration
	// Running are the leases held, by slot.
	Running []RunningState
	// Waiting are the calls waiting, in the order they will be admitted.
	Waiting []WaitingState
}

// RunningState is one lease held.
type RunningState struct {
	// Slot is the slot held, and Hinted says it is the server's own slot
	// number the call named, rather than the queue's count.
	Slot      int
	Hinted    bool
	Endpoint  string
	Class     string
	SessionID string
	Priority  Priority
	Tokens    int
	// Started is when the lease was admitted.
	Started time.Time
	// Held is how long the lease has been held.
	Held time.Duration
	// Preempted reports that the call has been cancelled and has not yet
	// let go.
	Preempted bool
}

// WaitingState is one call waiting.
type WaitingState struct {
	Endpoint  string
	Class     string
	SessionID string
	Priority  Priority
	// Earned is the priority its waiting has raised it to.
	Earned Priority
	Tokens int
	// Waited is how long it has waited for this lease, and WaitedSince how
	// long since the caller first began waiting for the call, which a retry
	// carries.
	Waited      time.Duration
	WaitedSince time.Duration
	// Gated reports that it is not eligible yet: a maintenance call waiting
	// for an idle pool.
	Gated bool
}

// Snapshot returns the state of every pool, sorted by name. It is a copy:
// nothing in it changes afterwards.
func (q *Queue) Snapshot() []PoolState {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	out := make([]PoolState, 0, len(q.pools))
	for _, p := range q.pools {
		st := PoolState{Pool: p.spec, Used: p.used, Idle: q.idleLocked(p, now)}
		st.Endpoints = slices.Clone(p.spec.Endpoints)
		if st.Idle && !p.lastActive.IsZero() {
			st.IdleFor = now.Sub(p.lastActive)
		}
		for l := range p.running {
			st.Running = append(st.Running, RunningState{
				Slot: l.slot, Hinted: l.hinted, Endpoint: l.need.Endpoint, Class: l.need.Class, SessionID: l.need.SessionID,
				Priority: l.need.Priority, Tokens: l.need.Tokens, Started: l.started.UTC(), Held: now.Sub(l.started), Preempted: l.preempted,
			})
		}
		slices.SortFunc(st.Running, func(a, b RunningState) int { return cmp.Compare(a.Slot, b.Slot) })
		for _, w := range p.waiting {
			st.Waiting = append(st.Waiting, WaitingState{
				Endpoint: w.need.Endpoint, Class: w.need.Class, SessionID: w.need.SessionID,
				Priority: w.need.Priority, Earned: effective(w.need.Priority, w.since, now, q.policy.Aging),
				Tokens: w.need.Tokens, Waited: now.Sub(w.enqueued), WaitedSince: now.Sub(w.since),
				Gated: !q.eligibleLocked(p, w, now),
			})
		}
		slices.SortStableFunc(st.Waiting, func(a, b WaitingState) int {
			return cmp.Or(cmp.Compare(a.Earned, b.Earned), cmp.Compare(b.WaitedSince, a.WaitedSince))
		})
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b PoolState) int { return cmp.Compare(a.Name, b.Name) })
	return out
}
