package agent

import (
	"context"
	"math"
	"time"

	"github.com/erlidev/eika/internal/provider"
)

// DefaultCountTimeout is what Options.CountTimeout is when it is zero. It
// bounds everything the token counter does for one model call: the prediction
// before it, and what the counter learns after it. A tokenizer that answers in
// tens of milliseconds at 200k tokens, and one that has stopped answering,
// must both leave the call to go ahead. A preview of a request is counted
// under it too.
const DefaultCountTimeout = 10 * time.Second

// counted is what the token counter said about one model call.
type counted struct {
	// prediction is the size the request was expected to be before it was
	// sent, with the method that reached it.
	prediction provider.Prediction
	// drift is the prediction less the prompt the endpoint measured, nil when
	// it stated none.
	drift *int
}

// predict returns the size of the request c describes, from the session's
// counter. Counting never fails a call: a counter that cannot answer in time
// or at all is replaced by its estimate, and the method says which one the
// number is.
func (a *Agent) predict(ctx context.Context, c Context) provider.Prediction {
	req := c.Request()
	countCtx, cancel := context.WithTimeout(ctx, a.opts.CountTimeout)
	defer cancel()
	pred, err := a.counter.Predict(countCtx, req)
	if err == nil {
		return pred
	}
	if ctx.Err() == nil {
		a.opts.Logger.Warn("count request tokens failed; estimating", "model", c.Parameters.Model, "error", err)
	}
	return a.counter.Estimate(req)
}

// measure tells the counter what the endpoint measured for the request it
// predicted, so that it learns, and returns the drift. An exact prediction
// that drifted is a bug in the counting or in the request that was counted, so
// it is logged with its numbers and without the request.
func (a *Agent) measure(ctx context.Context, s *Session, runID string, req provider.Request, pred provider.Prediction, usage provider.Usage) counted {
	out := counted{prediction: pred}
	measured, stated := usage.InputTokens.Value()
	if !stated {
		return out
	}
	drift := pred.Tokens - measured
	out.drift = &drift
	countCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.opts.CountTimeout)
	defer cancel()
	a.counter.Observe(countCtx, req, usage)
	if pred.Method == provider.MethodExact && drift != 0 {
		a.opts.Logger.Warn("exact token prediction drifted",
			"session_id", s.ID, "run_id", runID, "model", req.Model,
			"predicted", pred.Tokens, "measured", measured, "drift", drift)
	}
	return out
}

// seedCounter teaches the session's counter the newest measurement the
// conversation carries, if the counter has learned nothing yet. A counter is
// held in memory, so a session resumed after a restart would otherwise be
// sized from nothing on an endpoint that counts no better than by bytes, where
// the stored measurement says how large it was.
func (a *Agent) seedCounter(ctx context.Context, s *Session, prompt []Section) {
	msgs := s.Conversation.Messages()
	if _, at := lastMeasured(msgs); at >= 0 {
		countCtx, cancel := context.WithTimeout(ctx, a.opts.CountTimeout)
		defer cancel()
		req := a.sessionBase(s, prompt).WithMessages(msgs[:at]).Request()
		a.counter.Seed(countCtx, req, msgs[at].Metrics.Context)
	}
}

// reasoningTokens returns how many tokens of a response were reasoning and
// where the number came from. An endpoint's own count is used as it is. One
// that states none, such as llama.cpp, has its reasoning text tokenised by the
// endpoint when it can, and that count is labelled, since it approximates what
// the model generated. No reasoning text and no count is no number: the
// endpoint may have hidden what it thought.
func (a *Agent) reasoningTokens(ctx context.Context, r response) (provider.Count, string) {
	if n := r.usage.ReasoningTokens; n.Reported() {
		return n, ReasoningReported
	}
	if r.message.Reasoning == "" {
		return provider.Count{}, ""
	}
	countCtx, cancel := context.WithTimeout(ctx, a.opts.CountTimeout)
	defer cancel()
	if n, ok := a.counter.CountText(countCtx, r.message.Reasoning); ok {
		return provider.Reported(n), ReasoningTokenised
	}
	return provider.Count{}, ""
}

// widenedReserve is the room compaction keeps free when the prompt of tokens
// tokens was only estimated: the reserve plus the error estimates have shown,
// which is relative to the size predicted. The reserve is never more than half
// the window, or a bad estimator would compact every call.
func widenedReserve(reserve, window, tokens int, relativeError float64) int {
	widened := reserve + int(math.Ceil(relativeError*float64(tokens)))
	return min(widened, window/2)
}
