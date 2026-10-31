package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
)

// DefaultStall is how long a stream may be silent before the attempt is
// given up, and the base of the allowance a response gets before its first
// token: 30 seconds.
const DefaultStall = 30 * time.Second

// ErrStalled is the cause of an attempt that was aborted because the stream
// sent no bytes for as long as it was allowed to. The attempt fails as a
// retryable provider error that wraps it.
var ErrStalled = errors.New("the model server sent nothing")

// stalledError says how long the stream was allowed to be silent.
type stalledError struct{ after time.Duration }

func (e *stalledError) Error() string {
	return fmt.Sprintf("%v for %v", ErrStalled, e.after.Round(time.Millisecond))
}

func (e *stalledError) Unwrap() error { return ErrStalled }

// prefillAllowance is how long a response may be silent before its first token:
// the stall base plus twice the time reading the prompt should take at the
// measured rate, 30 s + predicted_prefilled / rate x 2 (docs/design/runtime.md
// section 5). Dense prefill of 200k tokens takes minutes, and a limit that
// fires during a real prefill throws it away and starts it again.
func prefillAllowance(stall time.Duration, prefilled, rate int) time.Duration {
	if prefilled <= 0 || rate <= 0 {
		return stall
	}
	return stall + 2*time.Duration(prefilled)*time.Second/time.Duration(rate)
}

// prefilled is how many tokens of the call's prompt the server is expected to
// read. Nothing is known to be cached: the server may have evicted what it
// held, and a limit too long costs a wait while one too short costs the whole
// prefill twice, so the whole predicted prompt counts.
func (q *queued) prefilled() int { return q.prompt }

// prefillRate is the prompt tokens per second the server is taken to read at.
// The run's Inference states the rate measured on the endpoint from its recorded
// calls; without one it is the queue's own assumption, which the affinity book
// prices a prefix with, so that there is one number and not two.
func (a *Agent) prefillRate() int {
	in := a.opts.Inference
	switch {
	case in.PrefillRate > 0:
		return in.PrefillRate
	case in.Queue != nil:
		return in.Queue.Policy().PrefillRate
	}
	return inference.DefaultPrefillRate
}

// stallWatch aborts one attempt at a model call when its stream goes silent.
// Any bytes count as progress, including reasoning, comments and chunks that
// become no event; the limit is the prefill allowance until the response
// produces its first token, and the stall base after.
type stallWatch struct {
	stall  time.Duration
	cancel context.CancelCauseFunc

	mu    sync.Mutex
	timer *time.Timer
	// limit is how long the stream may be silent now.
	limit time.Duration
	done  bool
}

// watchStall starts watching the attempt whose context cancel ends. The clock
// starts now, which is after the lease was granted: the wait for a lease is
// the queue's, and counts for nothing here.
func (a *Agent) watchStall(cancel context.CancelCauseFunc, q *queued) *stallWatch {
	w := &stallWatch{
		stall:  a.opts.Limits.Stall,
		cancel: cancel,
		limit:  prefillAllowance(a.opts.Limits.Stall, q.prefilled(), a.prefillRate()),
	}
	w.timer = time.AfterFunc(w.limit, w.fire)
	return w
}

// fire ends the attempt, once.
func (w *stallWatch) fire() {
	w.mu.Lock()
	if w.done {
		w.mu.Unlock()
		return
	}
	w.done = true
	limit := w.limit
	w.mu.Unlock()
	w.cancel(&stalledError{after: limit})
}

// touch records that bytes arrived, so the silence starts again from now.
func (w *stallWatch) touch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.done {
		w.timer.Reset(w.limit)
	}
}

// tokenSeen records that the response began to produce tokens, after which it
// is held to the stall base alone.
func (w *stallWatch) tokenSeen() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	w.limit = w.stall
	w.timer.Reset(w.limit)
}

// stop ends the watch. It is safe to call more than once.
func (w *stallWatch) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.done = true
	w.timer.Stop()
}

// stalled returns the error that ended the attempt as a retryable provider
// failure, if a stall did.
func stalled(cause error) error {
	var s *stalledError
	if !errors.As(cause, &s) {
		return nil
	}
	return &provider.Error{Op: "call model", Retryable: true, Err: s}
}
