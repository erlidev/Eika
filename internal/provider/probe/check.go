package probe

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
)

// Verdict is the outcome of a check that asks the model: the Responses
// conformance check or the slot affinity check.
type Verdict string

// The outcomes of a check.
const (
	// Passed means the endpoint took a streamed tool-call round trip with the
	// reasoning it returned, stated its usage with a cached count, and, where
	// the check could ask, was counted to the token.
	Passed Verdict = "passed"
	// Failed means the endpoint did something else, and Reason says what.
	Failed Verdict = "failed"
	// Skipped means the check could not run, for want of a model to ask or
	// because the inference pool stayed busy, and Reason says why.
	Skipped Verdict = "skipped"
)

// current reports whether a check with verdict, made against build and model,
// was made against the same build and model as wantBuild and wantModel, so
// that running it again would learn nothing.
func current(verdict Verdict, build, model, wantBuild, wantModel string) bool {
	return verdict != "" && build == wantBuild && model == wantModel
}

// answer is one response read to the end.
type answer struct {
	message provider.Message
	stop    string
	usage   provider.Usage
}

// waitError is a request that never got its lease: the pool was busy until the
// check's own time ran out, which says nothing about the endpoint.
type waitError struct{ err error }

func (e *waitError) Error() string { return e.err.Error() }
func (e *waitError) Unwrap() error { return e.err }

// busyReason says why a check was skipped when err is a request that never
// got its lease, and reports false for any other error.
func busyReason(err error) (string, bool) {
	var wait *waitError
	if !errors.As(err, &wait) {
		return "", false
	}
	return fmt.Sprintf("the inference pool stayed busy, so the check did not run and will be tried again: %v", wait.err), true
}

// asker sends a check's requests to the model under test, each under a lease
// on the check's pool.
type asker struct {
	provider provider.Provider
	// queue is the pool to take leases from; nil takes none. need says whose
	// requests they are.
	queue *inference.Queue
	need  inference.Need
	// promptTokens bounds the prompt of any request of the check, for the
	// room it asks the queue for beside the request's output limit.
	promptTokens int
}

// ask reads one request to the end under a lease. The request's own time
// limit starts when the lease is granted. A request that never got its lease
// fails with a waitError.
func (k asker) ask(ctx context.Context, req provider.Request) (answer, error) {
	need := k.need
	need.Tokens = k.promptTokens
	if req.Sampling.MaxOutput != nil {
		need.Tokens += *req.Sampling.MaxOutput
	}
	var (
		a        answer
		admitted bool
	)
	err := k.queue.Do(ctx, need, func(ctx context.Context, _ *inference.Lease) error {
		admitted = true
		var err error
		a, err = collect(ctx, k.provider, req)
		return err
	})
	if err != nil && !admitted && k.queue != nil {
		return answer{}, &waitError{err: err}
	}
	return a, err
}

// collect streams one request to the end within the check's time limit and
// assembles the assistant message it produced, with its reasoning items as the
// endpoint returned them.
func collect(ctx context.Context, p provider.Provider, req provider.Request) (answer, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	events, err := p.Stream(ctx, req)
	if err != nil {
		return answer{}, err
	}
	var (
		text, reasoning strings.Builder
		a               answer
		done            bool
	)
	for e := range events {
		switch e.Kind {
		case provider.KindTextDelta:
			text.WriteString(e.Text)
		case provider.KindReasoningDelta:
			reasoning.WriteString(e.ReasoningDelta)
		case provider.KindReasoningItem:
			a.message.ReasoningItems = append(a.message.ReasoningItems, e.ReasoningItem)
		case provider.KindToolCall:
			a.message.ToolCalls = append(a.message.ToolCalls, e.ToolCall)
		case provider.KindUsage:
			a.usage = a.usage.Merge(e.Usage)
		case provider.KindDone:
			a.stop, done = e.StopReason, true
		case provider.KindError:
			return answer{}, e.Err
		}
	}
	if !done {
		if err := ctx.Err(); err != nil {
			return answer{}, err
		}
		return answer{}, errors.New("the response ended without finishing")
	}
	a.message.Role = provider.RoleAssistant
	a.message.Content, a.message.Reasoning = text.String(), reasoning.String()
	return a, nil
}
