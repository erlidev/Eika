package agent_test

import (
	"context"
	"io"
	"log/slog"
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

// A call waits for its lease before it asks the model, and the wait is not
// counted as silence.
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
	if p.Attempts() != 0 {
		t.Fatal("the model was called before the lease was granted")
	}
	held.Release()
	if err := within(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := p.Attempts(); got != 1 {
		t.Errorf("%d attempts: the wait for the lease was counted as a stall", got)
	}
}
