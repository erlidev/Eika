package inference

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// ErrPreempted is the cause a lease's context is cancelled with when an
// interactive call took its place. The call that held it retries.
var ErrPreempted = errors.New("the call was preempted by an interactive call")

// maxPreemptions bounds how often Do retries one call that interactive
// calls keep preempting. Aging protects a call that has waited long enough;
// this only stops a loop the protection somehow missed.
const maxPreemptions = 16

// waitLogThreshold is the shortest wait the queue logs when a call is
// finally admitted. A shorter one is the queue doing nothing worth reading.
const waitLogThreshold = 250 * time.Millisecond

// defaultPool is the pool a call that names none waits in.
const defaultPool = "default"

// Need describes the call that wants a lease.
type Need struct {
	// Pool is the hardware group the call runs on. Empty means the default
	// pool.
	Pool string
	// Priority is how urgent the call is, fixed for this call. A run whose
	// urgency can change between its calls reads it from a Rank.
	Priority Priority
	// Tokens is the room the call is expected to take in the cache: its
	// predicted prompt plus its maximum output. Zero means unknown, and the
	// call is counted as taking none.
	Tokens int
	// Endpoint, Class and SessionID say who the call is, for the state the
	// queue reports and the logs. Endpoint is also the server a slot belongs
	// to.
	Endpoint  string
	Class     string
	SessionID string
	// Affinity asks for the slot that holds the session's prefix, and Prompt is
	// the tokens of the prompt the call reads, which the slot's cache is
	// expected to hold already (zero means Tokens). It is set by a call that
	// continues SessionID's conversation, and left false by one that shares
	// nothing with it, such as a title. It takes effect only on an endpoint the
	// pool lists in Hinted.
	Affinity bool
	Prompt   int
	// Since is when the caller first began waiting for this call. A retry
	// passes the first attempt's, so the aging it earned is kept. Zero means
	// now.
	Since time.Time
}

// Queue admits model calls to pools. The zero value is not usable; use New.
// A nil *Queue admits everything at once and holds nothing, for code that
// runs without one.
type Queue struct {
	log *slog.Logger

	mu     sync.Mutex
	policy Policy
	pools  map[string]*pool
	seq    uint64
}

// pool is the state of one hardware group. All fields are guarded by the
// queue's mu.
type pool struct {
	name string
	spec Pool
	// configured is false for a pool a call named before SetPools described
	// it: one slot, nothing else known.
	configured bool

	running map[*Lease]struct{}
	// used is the sum of the running leases' tokens.
	used    int
	waiting []*waiter
	// active counts the running leases that are not maintenance, and
	// lastActive is when the last of them ended. A pool is idle when none is
	// running and the last one ended long enough ago; a pool that never ran
	// one has been idle forever.
	active     int
	lastActive time.Time

	// books record what the slots of each hinted endpoint hold, by endpoint
	// name, and holdUntil is when the call at the head of the line stops
	// waiting for its busy slot, zero while none does.
	books     map[string]*slotBook
	holdUntil time.Time

	timer   *time.Timer
	timerAt time.Time
}

// waiter is a call waiting for a lease.
type waiter struct {
	need     Need
	ctx      context.Context
	enqueued time.Time
	since    time.Time
	seq      uint64
	granted  chan struct{}
	// waitingForSlot is when the call first found its session's slot busy,
	// zero until it does.
	waitingForSlot time.Time
	// lease is set, with granted closed, when the call is admitted.
	lease *Lease
}

// New returns a Queue with the default policy and no pools. Pools are added
// with SetPools; a call naming a pool nothing described runs on one slot.
func New(log *slog.Logger) *Queue {
	if log == nil {
		log = slog.Default()
	}
	return &Queue{log: log, policy: DefaultPolicy(), pools: map[string]*pool{}}
}

// SetPools describes the pools. It replaces every earlier description and
// takes effect at once: waiting calls are admitted if a pool grew, and a
// pool that shrank admits nothing until its running leases fall below the new
// size. A lease already running keeps running; it is never cancelled by a
// change of settings. A pool the new list leaves out is dropped when it is
// idle, and keeps its last description while it still has leases or waiters.
func (q *Queue) SetPools(pools []Pool) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	named := map[string]struct{}{}
	for _, spec := range pools {
		spec.Slots = max(spec.Slots, 1)
		spec.Cache = max(spec.Cache, 0)
		named[spec.Name] = struct{}{}
		p := q.poolLocked(spec.Name)
		p.spec, p.configured = spec, true
		p.describeBooks()
	}
	for name, p := range q.pools {
		if _, ok := named[name]; ok {
			continue
		}
		if len(p.running) == 0 && len(p.waiting) == 0 {
			stopTimer(p)
			delete(q.pools, name)
		}
	}
	for _, p := range q.pools {
		q.dispatchLocked(p, now)
	}
}

// SetPolicy replaces the policy and takes effect at once.
func (q *Queue) SetPolicy(policy Policy) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.policy = policy.normalised()
	now := time.Now()
	for _, p := range q.pools {
		q.dispatchLocked(p, now)
	}
}

// Policy returns the policy in force.
func (q *Queue) Policy() Policy {
	if q == nil {
		return DefaultPolicy()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.policy
}

// poolLocked returns the named pool, making an undescribed one.
func (q *Queue) poolLocked(name string) *pool {
	if name == "" {
		name = defaultPool
	}
	p, ok := q.pools[name]
	if !ok {
		p = &pool{
			name:    name,
			spec:    Pool{Name: name, Slots: 1, KV: KVUnknown},
			running: map[*Lease]struct{}{},
			books:   map[string]*slotBook{},
		}
		q.pools[name] = p
	}
	return p
}

// Acquire waits for a lease on the pool the call needs and returns it. It
// returns an error wrapping the context's when ctx ends first, and a waiter
// that gives up leaves the queue at once. The caller releases the lease when
// its model call is over, whichever way, and makes the call with the lease's
// Context, which is cancelled if the call is preempted.
func (q *Queue) Acquire(ctx context.Context, n Need) (*Lease, error) {
	if q == nil {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("start model call: %w", err)
		}
		return &Lease{ctx: ctx, slot: -1}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("wait for the %s pool: %w", poolLabel(n.Pool), err)
	}
	q.mu.Lock()
	now := time.Now()
	p := q.poolLocked(n.Pool)
	if n.Since.IsZero() || n.Since.After(now) {
		n.Since = now
	}
	n.Tokens = max(n.Tokens, 0)
	q.seq++
	w := &waiter{need: n, ctx: ctx, enqueued: now, since: n.Since, seq: q.seq, granted: make(chan struct{})}
	p.waiting = append(p.waiting, w)
	q.dispatchLocked(p, now)
	q.mu.Unlock()

	select {
	case <-w.granted:
		return w.lease, nil
	case <-ctx.Done():
	}
	q.mu.Lock()
	if l := w.lease; l != nil {
		// Admitted in the instant the context ended: the call is not going
		// to be made, so the lease goes back.
		q.mu.Unlock()
		l.Release()
	} else {
		p.waiting = slices.DeleteFunc(p.waiting, func(o *waiter) bool { return o == w })
		q.dispatchLocked(p, time.Now())
		q.mu.Unlock()
		q.log.Debug("model call stopped waiting", "pool", p.name, "priority", n.Priority.String(), "endpoint", n.Endpoint,
			"waited_ms", time.Since(w.enqueued).Milliseconds())
	}
	return nil, fmt.Errorf("wait for the %s pool: %w", p.name, ctx.Err())
}

// poolLabel names a pool in an error.
func poolLabel(name string) string {
	if name == "" {
		return defaultPool
	}
	return name
}

// Do runs fn holding a lease on the pool the call needs, and releases it
// when fn returns or panics. A call that is preempted while fn runs is made
// again with a new lease, as many times as it takes up to a bound, since its
// caller has nowhere to put a retry; the result of the last run is returned.
// fn gets the lease's context.
func (q *Queue) Do(ctx context.Context, n Need, fn func(ctx context.Context, l *Lease) error) error {
	if n.Since.IsZero() {
		n.Since = time.Now()
	}
	for attempt := 1; ; attempt++ {
		preempted, err := q.attempt(ctx, n, fn)
		switch {
		case !preempted || ctx.Err() != nil:
			return err
		case attempt >= maxPreemptions:
			return fmt.Errorf("model call preempted %d times: %w: %w", attempt, ErrPreempted, err)
		}
	}
}

// attempt is one run of fn under a lease. It reports whether the lease was
// preempted and the call did not succeed.
func (q *Queue) attempt(ctx context.Context, n Need, fn func(ctx context.Context, l *Lease) error) (bool, error) {
	l, err := q.Acquire(ctx, n)
	if err != nil {
		return false, err
	}
	defer l.Release()
	err = fn(l.Context(), l)
	return err != nil && l.Preempted(), err
}

// effective is the priority a waiter has earned by waiting: one level more
// urgent for every aging period since it began waiting, never past
// interactive.
func effective(base Priority, since, now time.Time, aging time.Duration) Priority {
	levels := max(int(now.Sub(since)/aging), 0)
	return max(base-Priority(min(levels, int(Maintenance))), Interactive)
}

// idleLocked reports whether the pool has run nothing but maintenance for
// the policy's idle time.
func (q *Queue) idleLocked(p *pool, now time.Time) bool {
	if p.active > 0 {
		return false
	}
	return p.lastActive.IsZero() || now.Sub(p.lastActive) >= q.policy.MaintenanceIdle
}

// eligibleLocked reports whether the waiter may be admitted: a call that is
// still maintenance after its aging waits for an idle pool.
func (q *Queue) eligibleLocked(p *pool, w *waiter, now time.Time) bool {
	if q.policy.MaintenanceIdle <= 0 {
		return true
	}
	if effective(w.need.Priority, w.since, now, q.policy.Aging) < Maintenance {
		return true
	}
	return q.idleLocked(p, now)
}

// bestLocked returns the waiter to admit next: the most urgent by what it
// has earned, then the one that has waited longest, among those eligible.
func (q *Queue) bestLocked(p *pool, now time.Time) *waiter {
	var best *waiter
	var bestPriority Priority
	for _, w := range p.waiting {
		if !q.eligibleLocked(p, w, now) {
			continue
		}
		pr := effective(w.need.Priority, w.since, now, q.policy.Aging)
		if best == nil || pr < bestPriority ||
			(pr == bestPriority && (w.since.Before(best.since) || (w.since.Equal(best.since) && w.seq < best.seq))) {
			best, bestPriority = w, pr
		}
	}
	return best
}

// fitsLocked reports whether a call needing tokens can be admitted if the
// leases in gone were not running. A pool with nothing running admits any
// call, even one larger than its whole cache: the estimate is a worst case,
// the call may still fit, and refusing it would fail a turn for a guess.
func (p *pool) fitsLocked(tokens int, gone map[*Lease]struct{}) bool {
	running, used := len(p.running), p.used
	for l := range gone {
		running--
		used -= l.need.Tokens
	}
	if running >= p.spec.Slots {
		return false
	}
	if p.spec.Cache <= 0 || running <= 0 {
		return true
	}
	return used+max(tokens, 0) <= p.spec.Cache
}

// dispatchLocked admits waiters in order while they fit, and otherwise asks
// whether an interactive one may take a running call's place. It stops at the
// first waiter that does not fit, so a large call is never overtaken by a
// stream of small ones.
func (q *Queue) dispatchLocked(p *pool, now time.Time) {
	p.holdUntil = time.Time{}
	for {
		w := q.bestLocked(p, now)
		if w == nil {
			break
		}
		if !p.fitsLocked(w.need.Tokens, nil) {
			q.preemptLocked(p, w, now)
			break
		}
		// A call that waits for the busy slot of its session holds the line
		// like any call that does not fit, so a stream of small calls cannot
		// take the room it will need when it stops waiting.
		place := q.placeLocked(p, w, now)
		if !place.until.IsZero() {
			p.holdUntil = place.until
			break
		}
		q.grantLocked(p, w, now, place)
	}
	q.armLocked(p, now)
}

// grantLocked admits a waiter where place puts it.
func (q *Queue) grantLocked(p *pool, w *waiter, now time.Time, place placement) {
	p.waiting = slices.DeleteFunc(p.waiting, func(o *waiter) bool { return o == w })
	slot := place.slot
	earned := effective(w.need.Priority, w.since, now, q.policy.Aging)
	l := &Lease{q: q, pool: p, need: w.need, slot: slot, hinted: place.hinted, earned: earned, started: now, waited: now.Sub(w.enqueued)}
	l.ctx, l.cancel = context.WithCancelCause(w.ctx)
	p.running[l] = struct{}{}
	p.used += w.need.Tokens
	if w.need.Priority != Maintenance {
		p.active++
	}
	if place.hinted {
		q.holdSlotLocked(p, w, slot, now)
	}
	w.lease = l
	close(w.granted)
	if l.waited >= waitLogThreshold {
		q.log.Info("model call admitted after waiting", "pool", p.name, "slot", slot, "hinted", place.hinted,
			"endpoint", w.need.Endpoint, "class", w.need.Class, "session_id", w.need.SessionID,
			"priority", w.need.Priority.String(), "earned", earned.String(), "waited_ms", l.waited.Milliseconds())
	}
	if p.spec.Cache > 0 && len(p.running) == 1 && l.need.Tokens > p.spec.Cache {
		q.log.Warn("model call is larger than the pool's cache; admitted alone", "pool", p.name,
			"endpoint", w.need.Endpoint, "tokens", l.need.Tokens, "cache", p.spec.Cache)
	}
}

// freeSlot returns the lowest slot number no running lease holds. A pool whose
// size was lowered under its running leases may hold numbers above its new
// size, so the lowest free number is still below it whenever the pool admits.
func (p *pool) freeSlot() int {
	for slot := 0; ; slot++ {
		held := false
		for l := range p.running {
			if l.slot == slot {
				held = true
				break
			}
		}
		if !held {
			return slot
		}
	}
}

// preemptLocked cancels running background and maintenance calls so that the
// interactive waiter w can be admitted, when the policy allows it and
// cancelling them would be enough. The calls go in the order of least value:
// the least urgent first, and the one that started last, which has the least
// prefill to lose. A call already being cancelled counts as gone, so the same
// waiter does not cancel a second victim for the room the first will free.
func (q *Queue) preemptLocked(p *pool, w *waiter, now time.Time) {
	if !q.policy.PreemptRunning || w.need.Priority != Interactive {
		return
	}
	gone := map[*Lease]struct{}{}
	var candidates []*Lease
	for l := range p.running {
		switch {
		case l.preempted:
			gone[l] = struct{}{}
		case l.earned >= Background:
			candidates = append(candidates, l)
		}
	}
	slices.SortFunc(candidates, func(a, b *Lease) int {
		if a.need.Priority != b.need.Priority {
			return int(b.need.Priority) - int(a.need.Priority)
		}
		if c := b.started.Compare(a.started); c != 0 {
			return c
		}
		return b.slot - a.slot
	})
	var victims []*Lease
	for !p.fitsLocked(w.need.Tokens, gone) {
		if len(candidates) == 0 {
			return
		}
		gone[candidates[0]] = struct{}{}
		victims = append(victims, candidates[0])
		candidates = candidates[1:]
	}
	// Spare a victim the others already make room without.
	victims = slices.DeleteFunc(victims, func(l *Lease) bool {
		delete(gone, l)
		if p.fitsLocked(w.need.Tokens, gone) {
			return true
		}
		gone[l] = struct{}{}
		return false
	})
	for _, l := range victims {
		l.preempted = true
		l.cancel(ErrPreempted)
		q.log.Warn("model call preempted for an interactive call", "pool", p.name, "slot", l.slot,
			"endpoint", l.need.Endpoint, "session_id", l.need.SessionID, "priority", l.need.Priority.String(),
			"held_ms", now.Sub(l.started).Milliseconds(), "for_session_id", w.need.SessionID)
	}
}

// armLocked sets the timer that wakes the pool when a waiter that cannot be
// admitted now will become eligible by the passing of time alone: a
// maintenance call when the pool's idle time is up, or when its aging lifts it
// out of maintenance, and a call that waits for its session's slot when it
// stops.
func (q *Queue) armLocked(p *pool, now time.Time) {
	var next time.Time
	soonest := func(t time.Time) {
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	for _, w := range p.waiting {
		if q.eligibleLocked(p, w, now) {
			continue
		}
		soonest(w.since.Add(q.policy.Aging))
		if p.active == 0 {
			soonest(p.lastActive.Add(q.policy.MaintenanceIdle))
		}
	}
	if !p.holdUntil.IsZero() {
		soonest(p.holdUntil)
	}
	if next.IsZero() {
		stopTimer(p)
		return
	}
	if p.timer != nil && p.timerAt.Equal(next) {
		return
	}
	stopTimer(p)
	p.timerAt = next
	p.timer = time.AfterFunc(max(next.Sub(now), 0), func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.pools[p.name] != p {
			return
		}
		if p.timerAt.Equal(next) {
			p.timer, p.timerAt = nil, time.Time{}
		}
		q.dispatchLocked(p, time.Now())
	})
}

// stopTimer cancels the pool's wake-up.
func stopTimer(p *pool) {
	if p.timer != nil {
		p.timer.Stop()
	}
	p.timer, p.timerAt = nil, time.Time{}
}
