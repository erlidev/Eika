package agent_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
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
	calls    *callRecorder
	session  *agent.Session
}

// newQueuedRun builds an agent over p that queues its calls on q at the
// priority rank gives.
func newQueuedRun(t *testing.T, q *inference.Queue, rank *inference.Rank, p provider.Provider, retries int, extra ...tool.Tool) *queuedRun {
	t.Helper()
	exec, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	r, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	for _, x := range extra {
		if err := r.Register(x); err != nil {
			t.Fatalf("register %s: %v", x.Name(), err)
		}
	}
	run := &queuedRun{provider: p, events: &recorder{}, calls: &callRecorder{}, session: agent.NewSession("session-1", "computer-1")}
	run.agent = agent.New(p, r, agent.Options{
		Executor:      exec,
		Emitter:       run.events,
		Store:         newMemoryStore(),
		Recorder:      run.calls,
		ContextWindow: 400_000,
		Compaction:    agent.CompactionSettings{KeepRecentTokens: 1500},
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetryBackoff:  20 * time.Millisecond,
		MaxRetries:    retries,
		Inference:     agent.Inference{Queue: q, Pool: "gpu", Endpoint: "llama", Rank: rank},
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

func TestAModelCallWaitsForALeaseAndRecordsTheWait(t *testing.T) {
	q := newQueue(1)
	held, err := q.Acquire(context.Background(), inference.Need{Pool: "gpu"})
	if err != nil {
		t.Fatal(err)
	}
	p := providertest.New(providertest.Text("hello"))
	run := newQueuedRun(t, q, nil, p, 3)
	done := run.start(context.Background(), "hi")

	eventually(t, "the call to queue", func() bool { return len(q.Snapshot()[0].Waiting) == 1 })
	time.Sleep(60 * time.Millisecond)
	if p.Calls() != 0 {
		t.Fatal("the model was called before the lease was granted")
	}
	held.Release()
	if err := within(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}

	calls, _ := run.calls.recorded()
	if len(calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(calls))
	}
	got := calls[0].Queue
	if !got.Queued || got.Waited < 50*time.Millisecond || got.Slot != nil || got.Priority != "interactive" {
		t.Errorf("queue record = %+v, want a wait of at least 50ms at interactive and no slot, since the endpoint has none to name", got)
	}
	if running(q) != 0 {
		t.Error("the run left its lease behind")
	}
}

func TestACallWithoutAQueueRecordsNoQueueTime(t *testing.T) {
	f := newFixture(t, []providertest.Step{providertest.Text("hello")})
	calls := &callRecorder{}
	a := agent.New(f.provider, nil, agent.Options{Recorder: calls, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := a.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatal(err)
	}
	got, _ := calls.recorded()
	if len(got) != 1 || got[0].Queue.Queued {
		t.Errorf("queue record = %+v, want none", got)
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

func TestEveryWayAModelCallEndsReleasesItsLease(t *testing.T) {
	tests := []struct {
		name string
		step providertest.Step
	}{
		{"the request is refused", providertest.Fail(errors.New("refused"))},
		{"the stream is cut off before it finishes", providertest.Stream(provider.TextDelta("par"))},
		{"the stream fails midway", providertest.Stream(provider.TextDelta("par"), provider.Event{Kind: provider.KindError, Err: errors.New("connection reset")})},
		{"the response has no stop reason", providertest.Stream(provider.TextDelta("x"), provider.Done(""))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := newQueue(1)
			run := newQueuedRun(t, q, nil, providertest.New(tt.step), 3)
			if err := within(t, run.start(context.Background(), "go")); err == nil {
				t.Fatal("Run succeeded, want the failure")
			}
			if running(q) != 0 {
				t.Error("the failed call kept its lease")
			}
			l, err := q.Acquire(context.Background(), inference.Need{Pool: "gpu"})
			if err != nil {
				t.Fatalf("the pool is not usable after the failure: %v", err)
			}
			l.Release()
		})
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

func TestARetryTakesANewLeaseSoAMoreUrgentCallCanGetInBetween(t *testing.T) {
	q := newQueue(1)
	p := providertest.New(
		providertest.Fail(&provider.Error{Op: "stream", Retryable: true, Err: errors.New("overloaded")}),
		providertest.Text("finished"),
	)
	run := newQueuedRun(t, q, inference.NewRank(inference.Background), p, 3)
	done := run.start(context.Background(), "go")

	// The first attempt failed at once; the run is now backing off with no
	// lease, and a call that arrives takes the pool.
	eventually(t, "the first attempt", func() bool { return p.Calls() == 1 })
	var user *inference.Lease
	eventually(t, "the pool to be free during the backoff", func() bool {
		if running(q) != 0 {
			return false
		}
		l, err := q.Acquire(context.Background(), inference.Need{Pool: "gpu", Priority: inference.Interactive})
		if err != nil {
			t.Fatal(err)
		}
		user = l
		return true
	})
	eventually(t, "the retry to queue behind it", func() bool { return len(q.Snapshot()[0].Waiting) == 1 })
	if p.Calls() != 1 {
		t.Fatal("the retry ran while another call held the pool")
	}
	user.Release()
	if err := within(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p.Calls() != 2 {
		t.Errorf("model calls = %d, want 2", p.Calls())
	}
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
	var resets int
	for _, e := range run.events.all() {
		switch e.Type {
		case event.TypeRunError:
			t.Errorf("the run reported an error: %s", e.Payload)
		case event.TypeMessageReset:
			resets++
		}
	}
	if resets != 2 {
		t.Errorf("message.reset events = %d, want one per preemption", resets)
	}
	calls, _ := run.calls.recorded()
	if len(calls) != 1 || calls[0].Queue.Priority != "background" || !calls[0].Queue.Queued {
		t.Fatalf("recorded calls = %+v, want one background call", calls)
	}
	if running(q) != 0 {
		t.Error("the run left a lease behind")
	}
}

func TestARaisedRankMakesTheNextCallInteractiveAndNothingBefore(t *testing.T) {
	q := newQueue(1)
	rank := inference.NewRank(inference.Background)
	// A user message joins the run while its first tool runs.
	joins := callTool{name: "joins", run: func(context.Context) string {
		rank.Raise(inference.Interactive)
		return "ok"
	}}
	p := providertest.New(
		providertest.Calls("", providertest.Call("c1", "joins", map[string]any{})),
		providertest.Text("done"),
	)
	run := newQueuedRun(t, q, rank, p, 3, joins)
	if err := within(t, run.start(context.Background(), "go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls, _ := run.calls.recorded()
	var got []string
	for _, c := range calls {
		got = append(got, c.Queue.Priority)
	}
	if !reflect.DeepEqual(got, []string{"background", "interactive"}) {
		t.Errorf("priorities of the run's calls = %v, want background then interactive", got)
	}
}

func TestACompactionSummaryIsAModelCallOnTheQueue(t *testing.T) {
	q := newQueue(1)
	var steps []providertest.Step
	for i := range 4 {
		steps = append(steps, providertest.Text(filler(fmt.Sprintf("a%d", 2*i+1), 1000)))
	}
	steps = append(steps, providertest.Text("SUMMARY-1"))
	w := &watcher{Provider: providertest.New(steps...), q: q}
	run := newQueuedRun(t, q, inference.NewRank(inference.Foreground), w, 3)
	for i := range 4 {
		if err := run.agent.Run(context.Background(), run.session, provider.UserMessage(filler(fmt.Sprintf("u%d", 2*i), 1000))); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
	if err := run.agent.Compact(context.Background(), run.session, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if want := []int{1, 1, 1, 1, 1}; !reflect.DeepEqual(w.held, want) {
		t.Errorf("leases held at each model request = %v, want %v: the summary is a call like the others", w.held, want)
	}
	if running(q) != 0 {
		t.Error("the compaction left a lease behind")
	}
}
