package inference

import (
	"context"
	"errors"
	"time"
)

// Lease is the right to make one model call on a pool. The caller makes the
// call with Context, and releases the lease when the call is over, whether it
// finished, failed, was cut off or panicked. A lease covers a model call
// only: a tool call, and the wait between two model calls, take none.
type Lease struct {
	// q and pool are nil for the lease of a nil Queue.
	q    *Queue
	pool *pool
	ctx  context.Context
	// cancel cancels ctx with a cause. It is nil for the lease of a nil
	// Queue, whose context is the caller's own.
	cancel context.CancelCauseFunc

	need Need
	slot int
	// hinted reports that slot is a slot of the server the call names, which
	// only a call that continues a session on an endpoint taking slot hints
	// holds. Otherwise slot is the queue's own count.
	hinted  bool
	earned  Priority
	started time.Time
	waited  time.Duration

	// preempted and released are guarded by q.mu.
	preempted bool
	released  bool
}

// Context is the context the model call is made with. It ends when the
// context given to Acquire ends, when the lease is released, and, with
// ErrPreempted as its cause, when an interactive call takes the lease's place.
func (l *Lease) Context() context.Context { return l.ctx }

// Queued reports whether the lease came from a queue. The lease of a nil
// Queue holds nothing, waited for nothing, and has no slot to report.
func (l *Lease) Queued() bool { return l.q != nil }

// Waited is how long this lease waited in the queue before it was admitted.
func (l *Lease) Waited() time.Duration { return l.waited }

// Slot is the number of the slot the lease holds, counted from zero. It names
// the slot of the server when SlotHint says the call names it, and is the
// queue's own count otherwise.
func (l *Lease) Slot() int { return l.slot }

// SlotHint is the slot of the server the call should run on, and whether it
// should name one at all. It is true only for a call that continues a session
// on an endpoint that takes slot hints; any other call, and the lease of a nil
// Queue, names none, and the server places it.
func (l *Lease) SlotHint() (slot int, ok bool) { return l.slot, l.hinted }

// Priority is the priority the call asked for, which is the one a record of
// the call states. The priority it was admitted at, aged by waiting, may be
// higher.
func (l *Lease) Priority() Priority { return l.need.Priority }

// Preempted reports whether an interactive call cancelled this lease. The
// call it covered failed for that reason alone, and should be made again with
// a new lease.
func (l *Lease) Preempted() bool {
	return l.ctx != nil && errors.Is(context.Cause(l.ctx), ErrPreempted)
}

// Release gives the lease back and admits the call that should run next. It
// is safe to call more than once and from any goroutine; only the first call
// does anything.
func (l *Lease) Release() {
	if l == nil || l.q == nil {
		return
	}
	q := l.q
	q.mu.Lock()
	if l.released {
		q.mu.Unlock()
		return
	}
	l.released = true
	now := time.Now()
	p := l.pool
	delete(p.running, l)
	p.used -= l.need.Tokens
	if l.hinted {
		if book := p.books[l.need.Endpoint]; book != nil {
			book.touch(l.slot, l.need.SessionID, now)
		}
	}
	if l.need.Priority != Maintenance {
		p.active--
		p.lastActive = now
	}
	q.dispatchLocked(p, now)
	q.mu.Unlock()
	l.cancel(context.Canceled)
}
