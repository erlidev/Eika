package agent_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/tool"
)

// newQueue returns a queue with one pool of the given size.
func newQueue(slots int) *inference.Queue {
	q := inference.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.SetPools([]inference.Pool{{Name: "gpu", Slots: slots}})
	return q
}

// running is how many leases the pool holds.
func running(q *inference.Queue) int { return len(q.Snapshot()[0].Running) }

// queuedRun is an agent whose model calls take leases from a queue.
type queuedRun struct {
	agent    *agent.Agent
	provider provider.Provider
	events   *recorder
	session  *agent.Session
}

// newQueuedRun builds an agent over p that queues its calls on q at the
// priority rank gives.
func newQueuedRun(t *testing.T, q *inference.Queue, rank *inference.Rank, p provider.Provider, retries int, extra ...tool.Tool) *queuedRun {
	t.Helper()
	run := &queuedRun{provider: p, events: &recorder{}, session: agent.NewSession("session-1", "computer-1")}
	run.agent = agent.New(p, registry(t, extra...), agent.Options{
		Executor:     computer(t, nil),
		Emitter:      run.events,
		Store:        newMemoryStore(),
		Logger:       quietLogger(),
		RetryBackoff: 20 * time.Millisecond,
		MaxRetries:   retries,
		Inference:    agent.Inference{Queue: q, Pool: "gpu", Endpoint: "llama", Rank: rank},
	})
	return run
}

// start runs the agent in the background and returns its result.
func (r *queuedRun) start(ctx context.Context, message string) <-chan error {
	done := make(chan error, 1)
	go func() { done <- r.agent.Run(ctx, r.session, provider.UserMessage(message)) }()
	return done
}

// within waits for a run to end.
func within(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not end")
		return nil
	}
}

// eventually polls cond until it holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// watcher is a provider that notes how many leases the pool holds each time
// it is asked for a response.
type watcher struct {
	provider.Provider
	q *inference.Queue

	mu   sync.Mutex
	held []int
}

func (w *watcher) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	w.mu.Lock()
	w.held = append(w.held, running(w.q))
	w.mu.Unlock()
	return w.Provider.Stream(ctx, req)
}

func TestTheLeaseCoversTheModelCallAndNeitherToolCallsNorBackoff(t *testing.T) {
	q := newQueue(1)
	var duringTool []int
	slow := callTool{name: "slow", run: func(context.Context) string {
		duringTool = append(duringTool, running(q))
		return "done"
	}}
	w := &watcher{Provider: providertest.New(
		providertest.Calls("", providertest.Call("c1", "slow", map[string]any{})),
		providertest.Fail(&provider.Error{Op: "stream", Retryable: true, Err: errors.New("overloaded")}),
		providertest.Text("finished"),
	), q: q}
	run := newQueuedRun(t, q, nil, w, 3, slow)
	if err := within(t, run.start(context.Background(), "go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(duringTool, []int{0}) {
		t.Errorf("leases held during the tool call = %v, want none", duringTool)
	}
	// Each of the three attempts held exactly its own lease while it asked.
	if !reflect.DeepEqual(w.held, []int{1, 1, 1}) {
		t.Errorf("leases held at each model request = %v, want one each", w.held)
	}
	if running(q) != 0 {
		t.Error("the run left a lease behind")
	}
}

// stalling is a provider that streams a delta and then waits for its context
// to end, as a model that has not finished.
type stalling struct {
	started chan struct{}
}

func (s *stalling) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	out := make(chan provider.Event)
	go func() {
		defer close(out)
		out <- provider.TextDelta("par")
		close(s.started)
		<-ctx.Done()
		out <- provider.Event{Kind: provider.KindError, Err: ctx.Err()}
	}()
	return out, nil
}

func TestACancelledRunReleasesItsLease(t *testing.T) {
	q := newQueue(1)
	p := &stalling{started: make(chan struct{})}
	run := newQueuedRun(t, q, nil, p, 3)
	ctx, cancel := context.WithCancel(context.Background())
	done := run.start(ctx, "go")
	<-p.started
	if running(q) != 1 {
		t.Fatalf("leases held while the model streams = %d, want 1", running(q))
	}
	cancel()
	if err := within(t, done); err == nil {
		t.Fatal("a cancelled run succeeded")
	}
	eventually(t, "the lease to be released", func() bool { return running(q) == 0 })
}

// preemptible is a provider whose first request stalls until it is
// cancelled, and whose later requests answer.
type preemptible struct {
	mu       sync.Mutex
	requests []provider.Request
	stalls   int
	started  chan struct{}
}

func (p *preemptible) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	stall := len(p.requests) <= p.stalls
	p.mu.Unlock()
	out := make(chan provider.Event)
	go func() {
		defer close(out)
		if stall {
			out <- provider.TextDelta("partial")
			p.started <- struct{}{}
			<-ctx.Done()
			out <- provider.Event{Kind: provider.KindError, Err: &provider.Error{Op: "stream", Err: ctx.Err()}}
			return
		}
		out <- provider.TextDelta("answer")
		out <- provider.Done("stop")
	}()
	return out, nil
}

func TestAPreemptedCallIsRetriedAndTheUserNeverSeesAFailure(t *testing.T) {
	q := newQueue(1)
	q.SetPolicy(inference.Policy{PreemptRunning: true, Aging: time.Hour})
	// Two preemptions in a row against a retry budget of one: preemption is
	// not the endpoint failing, so it does not spend the budget.
	p := &preemptible{stalls: 2, started: make(chan struct{}, 2)}
	run := newQueuedRun(t, q, inference.NewRank(inference.Background), p, 1)
	done := run.start(context.Background(), "go")

	for range 2 {
		<-p.started
		user, err := q.Acquire(context.Background(), inference.Need{Pool: "gpu", Priority: inference.Interactive})
		if err != nil {
			t.Fatal(err)
		}
		// The run's retry is queued behind the user's call.
		eventually(t, "the preempted call to queue again", func() bool { return len(q.Snapshot()[0].Waiting) == 1 })
		user.Release()
	}
	if err := within(t, done); err != nil {
		t.Fatalf("Run = %v, want the preempted call to succeed on retry", err)
	}

	if len(p.requests) != 3 {
		t.Fatalf("model requests = %d, want the same request three times", len(p.requests))
	}
	for i, req := range p.requests[1:] {
		if !reflect.DeepEqual(req, p.requests[0]) {
			t.Errorf("retry %d sent a different request", i+1)
		}
	}
	for _, e := range run.events.all() {
		if e.Type == event.TypeRunError {
			t.Errorf("the run reported an error: %s", e.Payload)
		}
	}
	if running(q) != 0 {
		t.Error("the run left a lease behind")
	}
}
