package inference

import (
	"time"
)

// DefaultPrefillRate is the speed, in prompt tokens per second, the queue
// assumes a server reads a prompt at when it prices a prefix: how long a call
// may wait for the slot that holds its prefix before reading the prefix again
// elsewhere would have been faster. The bench's 5090 read 66002 tokens in
// 22.8 s (about 2900 a second); a slower machine waits longer than it needs
// to, a faster one gives up later than it could, and neither loses more than a
// factor of two, which is all the bound promises.
const DefaultPrefillRate = 2000

// SlotEndpoint is an endpoint of a pool whose requests name the server slot
// they run on, with the number of slots its server has. Slot numbers mean
// something on one server only, so the queue keeps a book for each of them.
type SlotEndpoint struct {
	// Name is the endpoint's name, as Need.Endpoint states it.
	Name string
	// Slots is how many slots the server has: the numbers a request may name
	// are 0 up to it.
	Slots int
}

// slotBook records what each slot of one endpoint's server holds, so that a
// session's next call goes where its prefix is. It is the "LRU map from
// session to slot" of docs/design/runtime.md section 3, with the least
// recently used owner of a slot being the first to lose it. It has one entry
// per slot because a slot holds one prefix: a map with more sessions than
// slots would remember prefixes the server no longer has.
type slotBook struct {
	holds []slotHold
}

// slotHold is what one slot holds: the session whose call last ran there, and
// how large that call's prompt was.
type slotHold struct {
	// session is empty for a slot that holds nothing worth keeping.
	session string
	// prompt is the tokens of the prompt the session's last call there read,
	// which the next call finds in the slot's cache if nothing evicted it.
	prompt int
	// used is when the last call there was admitted or ended.
	used time.Time
}

// newSlotBook returns a book of n empty slots, keeping what old held in the
// slots both have.
func newSlotBook(n int, old *slotBook) *slotBook {
	b := &slotBook{holds: make([]slotHold, n)}
	if old != nil {
		copy(b.holds, old.holds)
	}
	return b
}

// slotOf returns the slot that holds the session's prefix, or -1.
func (b *slotBook) slotOf(session string) int {
	for i, h := range b.holds {
		if h.session == session {
			return i
		}
	}
	return -1
}

// victim returns the free slot a session without one takes: a slot holding
// nothing first, otherwise the one whose owner went longest without a call. It
// is -1 when every slot is busy.
func (b *slotBook) victim(busy map[int]bool) int {
	best := -1
	for i, h := range b.holds {
		if !busy[i] && (best < 0 || h.cheaperThan(b.holds[best])) {
			best = i
		}
	}
	return best
}

// cheaperThan reports whether taking the slot from h's owner loses less than
// taking it from o's: a slot holding nothing, then the older use.
func (h slotHold) cheaperThan(o slotHold) bool {
	if (h.session == "") != (o.session == "") {
		return h.session == ""
	}
	return h.used.Before(o.used)
}

// take records that a call of the session runs on slot, which is now where its
// prefix is. The slot it held before keeps a stale copy of a shorter prefix, so
// it is given up: nothing is gained by sending the session back to it.
func (b *slotBook) take(slot int, session string, prompt int, now time.Time) {
	for i := range b.holds {
		if b.holds[i].session == session {
			b.holds[i] = slotHold{}
		}
	}
	b.holds[slot] = slotHold{session: session, prompt: prompt, used: now}
}

// touch records that the call of the session on slot ended, if the slot still
// belongs to the session.
func (b *slotBook) touch(slot int, session string, now time.Time) {
	if slot >= 0 && slot < len(b.holds) && b.holds[slot].session == session {
		b.holds[slot].used = now
	}
}

// describeBooks makes the pool's books match the endpoints its description
// lists: a book is kept, and resized, for each, and dropped for the rest.
func (p *pool) describeBooks() {
	books := make(map[string]*slotBook, len(p.spec.Hinted))
	for _, e := range p.spec.Hinted {
		books[e.Name] = newSlotBook(e.Slots, p.books[e.Name])
	}
	p.books = books
}

// holdSlotLocked records that the waiter's session now has its prefix in slot,
// and says so when it gave up waiting for its old one.
func (q *Queue) holdSlotLocked(p *pool, w *waiter, slot int, now time.Time) {
	book := p.books[w.need.Endpoint]
	if from := book.slotOf(w.need.SessionID); from >= 0 && from != slot && !w.waitingForSlot.IsZero() {
		q.log.Info("model call took another slot after waiting for its own", "pool", p.name, "endpoint", w.need.Endpoint,
			"session_id", w.need.SessionID, "from_slot", from, "slot", slot,
			"waited_ms", now.Sub(w.waitingForSlot).Milliseconds(), "prefix_tokens", book.holds[from].prompt)
	}
	book.take(slot, w.need.SessionID, w.need.prefixOf(), now)
}

// prefixOf is the tokens a call is expected to find already read when it runs
// where its session's prefix is: its prompt, else the room it was counted at.
func (n Need) prefixOf() int {
	if n.Prompt > 0 {
		return n.Prompt
	}
	return n.Tokens
}

// affine reports whether the call asks for the slot that holds its session's
// prefix: it continues a session's conversation and says whose.
func (n Need) affine() bool { return n.Affinity && n.SessionID != "" }

// placement is where the queue puts a call that may be admitted: the slot it
// runs on and whether it names that slot to the server, or the moment it stops
// waiting for a slot that is busy.
type placement struct {
	slot   int
	hinted bool
	// until is zero for a call placed now. Otherwise the call prefers a slot
	// that a running call holds, and waits for it until this time.
	until time.Time
}

// placeLocked decides where the waiter w runs.
//
// A call that continues a session on an endpoint that takes slot hints goes
// to the slot that holds its session's prefix when that is free, and
// otherwise to the free slot whose owner went longest without a call (a slot
// holding nothing first). When its own slot is busy it waits for it by the
// ski-rental rule: as long as reading the prefix again would take, and no
// longer. The wait costs the call a delay and the move costs it the prefill,
// and the occupant's remaining time is unknown, so waiting exactly as long as
// the prefill would have taken never costs more than twice what the better of
// the two would. After that it takes another slot, and the session's entry
// follows it there.
//
// Every other call is not placed by session: it names no slot, takes the
// lowest number no other unnamed call holds, and leaves the books alone, so a
// title, the model test or a probe never moves a session's prefix and never
// claims the slot of one. The server picks where they run.
func (q *Queue) placeLocked(p *pool, w *waiter, now time.Time) placement {
	book := p.books[w.need.Endpoint]
	if book == nil || !w.need.affine() {
		return placement{slot: p.freeSlot(), hinted: false}
	}
	busy := map[int]bool{}
	for l := range p.running {
		if l.hinted && l.need.Endpoint == w.need.Endpoint {
			busy[l.slot] = true
		}
	}
	if own := book.slotOf(w.need.SessionID); own >= 0 {
		if !busy[own] {
			return placement{slot: own, hinted: true}
		}
		if w.waitingForSlot.IsZero() {
			w.waitingForSlot = now
		}
		cost := time.Duration(book.holds[own].prompt) * time.Second / time.Duration(q.policy.PrefillRate)
		if until := w.waitingForSlot.Add(cost); now.Before(until) {
			return placement{until: until}
		}
	}
	if slot := book.victim(busy); slot >= 0 {
		return placement{slot: slot, hinted: true}
	}
	return placement{slot: p.freeSlot(), hinted: false}
}
