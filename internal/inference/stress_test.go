package inference

import (
	"context"
	"io"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentCallsNeverOverfillAPool hammers one pool from many goroutines
// with mixed priorities, sizes, cancellations and preemption, and checks the
// admission limits from inside the calls. It is meant for go test -race.
func TestConcurrentCallsNeverOverfillAPool(t *testing.T) {
	const (
		slots   = 3
		cache   = 1000
		workers = 24
		rounds  = 40
	)
	q := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.SetPools([]Pool{{Name: "gpu", Slots: slots, KV: KVShared, Cache: cache}})
	q.SetPolicy(Policy{PreemptRunning: true, MaintenanceIdle: 0, Aging: 5 * time.Millisecond})

	var running, tokens, admitted, failed, preempted atomic.Int64
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(w), 7))
			for range rounds {
				need := Need{
					Pool:     "gpu",
					Priority: Priority(rng.IntN(4)),
					Tokens:   1 + rng.IntN(400),
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(1+rng.IntN(30))*time.Millisecond)
				l, err := q.Acquire(ctx, need)
				if err != nil {
					failed.Add(1)
					cancel()
					continue
				}
				admitted.Add(1)
				now, used := running.Add(1), tokens.Add(int64(need.Tokens))
				if now > slots {
					t.Errorf("%d calls running on a %d-slot pool", now, slots)
				}
				if used > cache && now > 1 {
					t.Errorf("%d tokens running in a %d-token cache with %d calls", used, cache, now)
				}
				select {
				case <-time.After(time.Duration(rng.IntN(500)) * time.Microsecond):
				case <-l.Context().Done():
					if l.Preempted() {
						preempted.Add(1)
					}
				}
				running.Add(-1)
				tokens.Add(-int64(need.Tokens))
				l.Release()
				l.Release()
				cancel()
			}
		})
	}
	wg.Wait()
	if admitted.Load() == 0 {
		t.Fatal("no call was admitted")
	}
	st := q.Snapshot()[0]
	if len(st.Running) != 0 || len(st.Waiting) != 0 || st.Used != 0 {
		t.Errorf("pool after the storm = %d running, %d waiting, %d tokens; want it empty", len(st.Running), len(st.Waiting), st.Used)
	}
	t.Logf("%d admitted, %d gave up waiting, %d preempted", admitted.Load(), failed.Load(), preempted.Load())
}

// BenchmarkUncontendedLease is what the queue adds to a model call when
// nothing else wants the pool: one Acquire and one Release.
func BenchmarkUncontendedLease(b *testing.B) {
	q := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.SetPools([]Pool{{Name: "gpu", Slots: 2, KV: KVShared, Cache: 131072}})
	ctx := context.Background()
	need := Need{Pool: "gpu", Priority: Interactive, Tokens: 20000}
	for b.Loop() {
		l, err := q.Acquire(ctx, need)
		if err != nil {
			b.Fatal(err)
		}
		l.Release()
	}
}
