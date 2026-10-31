package agent

import (
	"context"
	"time"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
)

// Inference says how an agent's model calls wait for the GPU: the queue they
// take a lease from, the pool they run on, and how urgent the run is. The
// zero value takes no lease, which is how a test or a tool outside Core runs
// an agent without a queue.
type Inference struct {
	// Queue hands out the leases. Nil means model calls take none and have no
	// queue time to record.
	Queue *inference.Queue
	// Pool is the hardware group of the run's endpoint, and Endpoint the
	// provider's name, for the state the queue reports.
	Pool     string
	Endpoint string
	// Class is the model class the run was resolved from, empty when the
	// run named a model directly.
	Class string
	// Rank is the run's priority. The agent reads it at every model call, so
	// raising it makes the run's next call more urgent; nil means
	// interactive.
	Rank *inference.Rank
}

// QueueRecord is what the inference queue did for one model call.
type QueueRecord struct {
	// Queued is false for a call that took no lease, and the rest is then
	// empty.
	Queued bool
	// Waited is the time the call spent waiting for leases, summed over its
	// attempts: a call that was preempted and queued again waited for both.
	Waited time.Duration
	// Slot is the queue's slot of the lease that served the call.
	Slot int
	// Priority is what the call asked for, and Class the model class the run
	// was resolved from, empty if none.
	Priority string
	Class    string
}

// queued follows one model call through its attempts. A retry takes a new
// lease, so a more urgent call can get in between attempts, but the call keeps
// the age it has earned and the time it has waited.
type queued struct {
	// tokens is the room the call is expected to take in the cache: its
	// predicted prompt plus its maximum output.
	tokens int
	since  time.Time
	record QueueRecord
}

// callTokens is what a call occupies in a shared cache at worst: the prompt
// the counter predicted and the output the request may produce. A request
// with no output limit may fill the rest of the window.
func (a *Agent) callTokens(prompt int, c Context) int {
	if m := c.Parameters.Sampling.MaxOutput; m != nil {
		return prompt + *m
	}
	return max(prompt, a.opts.ContextWindow)
}

// acquire takes the lease for one attempt at the call. It waits behind the
// rest of the pool, and gives up when ctx ends.
func (a *Agent) acquire(ctx context.Context, s *Session, q *queued) (*inference.Lease, error) {
	in := a.opts.Inference
	if q.since.IsZero() {
		q.since = time.Now()
	}
	lease, err := in.Queue.Acquire(ctx, inference.Need{
		Pool:      in.Pool,
		Priority:  in.Rank.Priority(),
		Tokens:    q.tokens,
		Endpoint:  in.Endpoint,
		Class:     in.Class,
		SessionID: s.ID,
		Since:     q.since,
	})
	if err != nil || !lease.Queued() {
		return lease, err
	}
	q.record = QueueRecord{
		Queued:   true,
		Waited:   q.record.Waited + lease.Waited(),
		Slot:     lease.Slot(),
		Priority: lease.Priority().String(),
		Class:    in.Class,
	}
	return lease, nil
}

// preemptedError is what an attempt that an interactive call cancelled
// returns. It is retryable, so the retry path takes it, and it is not a
// failure: the run does not report it.
func preemptedError() error {
	return &provider.Error{Op: "call model", Retryable: true, Err: inference.ErrPreempted}
}
