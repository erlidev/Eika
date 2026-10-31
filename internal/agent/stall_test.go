package agent_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
)

// streamer is a provider whose attempts are scripted by a function, so that a
// test decides what each one sends and when. It counts the attempts.
type streamer struct {
	mu       sync.Mutex
	attempts int
	script   func(ctx context.Context, attempt int, out chan<- provider.Event)
}

func (s *streamer) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	s.mu.Lock()
	s.attempts++
	attempt := s.attempts
	s.mu.Unlock()
	out := make(chan provider.Event)
	go func() {
		defer close(out)
		s.script(ctx, attempt, out)
	}()
	return out, nil
}

func (s *streamer) Attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

// send delivers one event, or gives up when the attempt is aborted.
func send(ctx context.Context, out chan<- provider.Event, e provider.Event) bool {
	select {
	case out <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

// answer ends an attempt with a short text reply.
func answer(ctx context.Context, out chan<- provider.Event) {
	if send(ctx, out, provider.TextDelta("ok")) {
		send(ctx, out, provider.Done("stop"))
	}
}

// pause waits for d, or until the attempt is aborted.
func pause(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// stallRun is an agent over a streamer with a short stall limit. A prefill rate
// far above anything a test prompt needs keeps the allowance before the first
// token at the stall limit, unless a test sets its own.
type stallRun struct {
	agent   *agent.Agent
	session *agent.Session
	events  *recorder
	queue   *inference.Queue
}

func newStallRun(t *testing.T, p provider.Provider, stall time.Duration, in agent.Inference, backoff time.Duration) *stallRun {
	t.Helper()
	if in.PrefillRate == 0 {
		in.PrefillRate = 1_000_000
	}
	events := &recorder{}
	a := agent.New(p, nil, agent.Options{
		Emitter:       events,
		ContextWindow: 400_000,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetryBackoff:  backoff,
		Limits:        agent.Limits{Stall: stall},
		Inference:     in,
	})
	return &stallRun{agent: a, session: agent.NewSession("s1", "c1"), events: events, queue: in.Queue}
}

func (r *stallRun) run(t *testing.T, message string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- r.agent.Run(context.Background(), r.session, provider.UserMessage(message)) }()
	return within(t, done)
}

// count returns how many events of a type were emitted.
func (r *stallRun) count(typ string) int {
	n := 0
	for _, e := range r.events.all() {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func TestASilentStreamIsAbortedAndTheAttemptIsRetried(t *testing.T) {
	p := &streamer{script: func(ctx context.Context, attempt int, out chan<- provider.Event) {
		if attempt == 1 {
			<-ctx.Done() // the server has stopped answering
			return
		}
		answer(ctx, out)
	}}
	r := newStallRun(t, p, 40*time.Millisecond, agent.Inference{}, time.Millisecond)
	if err := r.run(t, "hi"); err != nil {
		t.Fatalf("Run: %v, want the retry to answer", err)
	}
	if got := p.Attempts(); got != 2 {
		t.Errorf("%d attempts, want the stalled one and its retry", got)
	}
	if r.count(event.TypeMessageReset) != 1 || r.count(event.TypeRunError) != 0 {
		t.Errorf("message.reset x%d, run.error x%d: a stall is retried like any retryable failure and the user sees no failure",
			r.count(event.TypeMessageReset), r.count(event.TypeRunError))
	}
	msgs := r.session.Conversation.Messages()
	if last := msgs[len(msgs)-1]; last.Content != "ok" {
		t.Errorf("the run ended with %+v", last)
	}
}

func TestAStallThatKeepsHappeningFailsTheRunAsRetryable(t *testing.T) {
	p := &streamer{script: func(ctx context.Context, _ int, _ chan<- provider.Event) { <-ctx.Done() }}
	r := newStallRun(t, p, 20*time.Millisecond, agent.Inference{}, time.Millisecond)
	err := r.run(t, "hi")
	if err == nil || !provider.Retryable(err) || !strings.Contains(err.Error(), "sent nothing") {
		t.Fatalf("Run error = %v, want a retryable stall", err)
	}
	if got := p.Attempts(); got != 4 {
		t.Errorf("%d attempts, want the first and the three retries", got)
	}
}

func TestBytesThatBecomeNoEventStillCountAsProgress(t *testing.T) {
	// A server that keeps the connection warm with comments while it reads a
	// long prompt sends bytes no provider turns into an event.
	p := &streamer{script: func(ctx context.Context, _ int, out chan<- provider.Event) {
		for range 8 {
			if !pause(ctx, 30*time.Millisecond) {
				return
			}
			provider.Progressed(ctx)
		}
		answer(ctx, out)
	}}
	r := newStallRun(t, p, 120*time.Millisecond, agent.Inference{}, time.Millisecond)
	if err := r.run(t, "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := p.Attempts(); got != 1 {
		t.Errorf("%d attempts: 240 ms of keep-alive bytes under a limit of 120 ms are not a stall", got)
	}
}

func TestReasoningCountsAsProgress(t *testing.T) {
	p := &streamer{script: func(ctx context.Context, _ int, out chan<- provider.Event) {
		for range 8 {
			if !pause(ctx, 30*time.Millisecond) {
				return
			}
			if !send(ctx, out, provider.Event{Kind: provider.KindReasoningDelta, ReasoningDelta: "hmm "}) {
				return
			}
		}
		answer(ctx, out)
	}}
	r := newStallRun(t, p, 120*time.Millisecond, agent.Inference{}, time.Millisecond)
	if err := r.run(t, "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := p.Attempts(); got != 1 {
		t.Errorf("%d attempts: reasoning is output, and a model that is thinking has not stalled", got)
	}
}

// bigPrompt is a message of about two thousand estimated tokens.
var bigPrompt = strings.Repeat("a long prompt that takes a while to read. ", 200)

func TestSilenceBeforeTheFirstTokenIsAllowedInProportionToThePrompt(t *testing.T) {
	// 2000 tokens at 10 000 a second take 0.2 s to read; twice that and the
	// stall base is a 0.44 s allowance, and the server is silent for 0.15 s.
	slowStart := func(ctx context.Context, _ int, out chan<- provider.Event) {
		if pause(ctx, 150*time.Millisecond) {
			answer(ctx, out)
		}
	}
	long := &streamer{script: slowStart}
	r := newStallRun(t, long, 40*time.Millisecond, agent.Inference{PrefillRate: 10_000}, time.Millisecond)
	if err := r.run(t, bigPrompt); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := long.Attempts(); got != 1 {
		t.Errorf("%d attempts: a prompt this long is allowed more than the base before its first token", got)
	}

	// The same silence after a short prompt is a stall.
	short := &streamer{script: func(ctx context.Context, attempt int, out chan<- provider.Event) {
		if attempt == 1 {
			slowStart(ctx, attempt, out)
			return
		}
		answer(ctx, out)
	}}
	r = newStallRun(t, short, 40*time.Millisecond, agent.Inference{PrefillRate: 10_000}, time.Millisecond)
	if err := r.run(t, "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := short.Attempts(); got != 2 {
		t.Errorf("%d attempts: 150 ms of nothing after a short prompt is more than the 40 ms base allows", got)
	}
}

func TestAfterTheFirstTokenOnlyTheBaseApplies(t *testing.T) {
	p := &streamer{script: func(ctx context.Context, attempt int, out chan<- provider.Event) {
		if attempt > 1 {
			answer(ctx, out)
			return
		}
		if send(ctx, out, provider.TextDelta("The ")) {
			<-ctx.Done()
		}
	}}
	// The allowance for this prompt is 0.44 s, far longer than the silence that
	// ends the attempt: the prefill is over.
	r := newStallRun(t, p, 40*time.Millisecond, agent.Inference{PrefillRate: 10_000}, time.Millisecond)
	begin := time.Now()
	if err := r.run(t, bigPrompt); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := p.Attempts(); got != 2 {
		t.Errorf("%d attempts, want the stalled one and its retry", got)
	}
	if took := time.Since(begin); took > 400*time.Millisecond {
		t.Errorf("took %v: a stream that went silent mid-answer was held to the prefill allowance", took)
	}
}

func TestAStallReleasesItsLease(t *testing.T) {
	q := newQueue(1)
	p := &streamer{script: func(ctx context.Context, attempt int, out chan<- provider.Event) {
		if attempt == 1 {
			<-ctx.Done()
			return
		}
		answer(ctx, out)
	}}
	// A long backoff leaves a window in which the call holds nothing.
	r := newStallRun(t, p, 30*time.Millisecond, agent.Inference{Queue: q, Pool: "gpu", Endpoint: "llama"}, 400*time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- r.agent.Run(context.Background(), r.session, provider.UserMessage("hi")) }()

	eventually(t, "the call to take the lease", func() bool { return running(q) == 1 })
	eventually(t, "the stalled attempt to give it back", func() bool { return running(q) == 0 })
	if p.Attempts() != 1 {
		t.Fatalf("%d attempts, want the retry to still be waiting out its backoff", p.Attempts())
	}
	// Another session gets the slot while the stalled call backs off.
	other, err := q.Acquire(context.Background(), inference.Need{Pool: "gpu"})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	other.Release()
	if err := within(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p.Attempts() != 2 {
		t.Errorf("%d attempts, want 2", p.Attempts())
	}
}

func TestTimeWaitingForALeaseIsNotSilence(t *testing.T) {
	q := newQueue(1)
	held, err := q.Acquire(context.Background(), inference.Need{Pool: "gpu"})
	if err != nil {
		t.Fatal(err)
	}
	p := &streamer{script: func(ctx context.Context, _ int, out chan<- provider.Event) { answer(ctx, out) }}
	r := newStallRun(t, p, 30*time.Millisecond, agent.Inference{Queue: q, Pool: "gpu", Endpoint: "llama"}, time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- r.agent.Run(context.Background(), r.session, provider.UserMessage("hi")) }()

	eventually(t, "the call to queue", func() bool { return len(q.Snapshot()[0].Waiting) == 1 })
	time.Sleep(150 * time.Millisecond) // five times the stall limit
	held.Release()
	if err := within(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := p.Attempts(); got != 1 {
		t.Errorf("%d attempts: the wait for the lease was counted as a stall", got)
	}
}

func TestThePrefillAllowance(t *testing.T) {
	for _, tc := range []struct {
		name            string
		stall           time.Duration
		prefilled, rate int
		want            time.Duration
	}{
		{"the bench's 66k prompt at its measured speed", 30 * time.Second, 66002, 2894, 30*time.Second + 45*time.Second + 611*time.Millisecond},
		{"a prompt of 20000 tokens at the default speed", 30 * time.Second, 20000, 2000, 50 * time.Second},
		{"nothing to read", 30 * time.Second, 0, 2000, 30 * time.Second},
		{"a rate not known", 30 * time.Second, 20000, 0, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := agent.PrefillAllowance(tc.stall, tc.prefilled, tc.rate)
			if diff := got - tc.want; diff < -time.Second || diff > time.Second {
				t.Errorf("allowance = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestThePrefillRateHasOneDefaultAndTheMeasuredRateWins(t *testing.T) {
	q := newQueue(1)
	q.SetPolicy(inference.Policy{PrefillRate: 500})
	for _, tc := range []struct {
		name string
		in   agent.Inference
		want int
	}{
		{"no queue and no measurement", agent.Inference{}, inference.DefaultPrefillRate},
		{"the queue's assumption", agent.Inference{Queue: q}, 500},
		{"a measured rate", agent.Inference{Queue: q, PrefillRate: 3100}, 3100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := agent.New(&streamer{}, nil, agent.Options{Inference: tc.in})
			if got := a.PrefillRate(); got != tc.want {
				t.Errorf("rate = %d, want %d", got, tc.want)
			}
		})
	}
}
