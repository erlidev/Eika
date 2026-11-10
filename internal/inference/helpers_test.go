package inference

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// newQueue returns a queue with the pools and the default policy, logging
// nowhere.
func newQueue(pools ...Pool) *Queue {
	q := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.SetPools(pools)
	return q
}

// pending is an Acquire running in a goroutine of the test's bubble.
type pending struct {
	mu    sync.Mutex
	done  bool
	lease *Lease
	err   error
	at    time.Time
}

// begin starts an Acquire and returns without waiting for it.
func begin(ctx context.Context, q *Queue, n Need) *pending {
	p := &pending{}
	go func() {
		l, err := q.Acquire(ctx, n)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.lease, p.err, p.done, p.at = l, err, true, time.Now()
	}()
	return p
}

// settle lets every goroutine of the bubble run until it blocks.
func settle() { synctest.Wait() }

// admitted reports whether the Acquire has returned a lease.
func (p *pending) admitted() bool {
	settle()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done && p.err == nil
}

// failed returns the Acquire's error, nil while it is still waiting.
func (p *pending) failed() error {
	settle()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// get returns the lease of an Acquire that must have been admitted.
func (p *pending) get(t *testing.T) *Lease {
	t.Helper()
	if !p.admitted() {
		t.Fatal("the call is still waiting; want it admitted")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lease
}

// when is the time the Acquire returned.
func (p *pending) when() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.at
}

// acquire takes a lease that must be granted at once.
func acquire(t *testing.T, q *Queue, n Need) *Lease {
	t.Helper()
	return begin(context.Background(), q, n).get(t)
}

// waiting returns how many calls wait in the named pool.
func waiting(q *Queue, pool string) int {
	settle()
	for _, p := range q.Snapshot() {
		if p.Name == pool {
			return len(p.Waiting)
		}
	}
	return 0
}

// pool is a single-slot pool named p without a cache bound.
func onePool(name string) Pool { return Pool{Name: name, Slots: 1} }
