package event

import (
	"context"
	"log/slog"
	"sync"
)

// subscriberBuffer is how many events one subscriber may fall behind before
// the bus starts dropping events for it. It is a value rather than an option
// because one slow client is a client the user interface has to recover from
// anyway, and a larger buffer only delays that.
const subscriberBuffer = 256

// Bus fans events out to the clients watching them. One process has one bus;
// every agent run emits into it and every WebSocket connection subscribes to
// it. A subscriber that stops reading loses events rather than blocking the
// run that produced them.
type Bus struct {
	log *slog.Logger

	mu   sync.Mutex
	next int64
	subs map[int64]*Subscription
}

// NewBus returns an empty bus.
func NewBus(log *slog.Logger) *Bus {
	if log == nil {
		log = slog.Default()
	}
	return &Bus{log: log, subs: make(map[int64]*Subscription)}
}

// Emit delivers e to every subscriber of its topic. It never blocks: a
// subscriber whose buffer is full loses the event and is told how many it lost
// with a bus.dropped event as soon as it reads again.
func (b *Bus) Emit(_ context.Context, e Event) {
	b.mu.Lock()
	subs := make([]*Subscription, 0, len(b.subs))
	for _, s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		s.deliver(e)
	}
}

// Subscribe returns a subscription that receives the events of the given
// topics. Close it when it is no longer read.
func (b *Bus) Subscribe(topics ...string) *Subscription {
	s := &Subscription{
		bus:    b,
		ch:     make(chan Event, subscriberBuffer),
		topics: make(map[string]bool, len(topics)),
	}
	for _, t := range topics {
		s.topics[t] = true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	s.id = b.next
	b.subs[s.id] = s
	return s
}

// Subscribers reports how many open subscriptions the bus has.
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// Subscription is one client's view of the bus: a set of topics and the
// events published on them.
type Subscription struct {
	bus *Bus
	id  int64
	ch  chan Event

	mu      sync.Mutex
	topics  map[string]bool
	dropped int
	closed  bool
}

// Events is the channel the subscription's events arrive on. It is closed by
// Close.
func (s *Subscription) Events() <-chan Event { return s.ch }

// Subscribe replaces the subscription's topics with the given ones.
func (s *Subscription) Subscribe(topics ...string) {
	next := make(map[string]bool, len(topics))
	for _, t := range topics {
		next[t] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.topics = next
}

// Topics reports which topics the subscription currently receives.
func (s *Subscription) Topics() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.topics))
	for t := range s.topics {
		out = append(out, t)
	}
	return out
}

// Close stops the subscription and closes its channel. Closing twice is
// harmless, which lets the reader and the writer both defer it.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	delete(s.bus.subs, s.id)
	s.bus.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

// deliver queues e when the subscription wants its topic, unless the
// subscriber is too far behind. Holding s.mu is also what makes closing the
// channel safe: every send happens under the lock that sets closed.
func (s *Subscription) deliver(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.topics[e.Topic] {
		return
	}
	if s.dropped > 0 && !s.reportDropped() {
		s.dropped++
		return
	}
	select {
	case s.ch <- e:
	default:
		s.dropped++
	}
}

// reportDropped tells the subscriber how many events it missed and reports
// whether the report itself got through. The caller holds s.mu.
func (s *Subscription) reportDropped() bool {
	notice, err := New(TypeBusDropped, TopicGlobal, BusDropped{Dropped: s.dropped})
	if err != nil {
		s.bus.log.Error("encode dropped event notice", "error", err)
		return false
	}
	select {
	case s.ch <- notice:
		s.dropped = 0
		return true
	default:
		return false
	}
}

var _ Emitter = (*Bus)(nil)
