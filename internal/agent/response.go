package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
)

// generation is what a turn has measured about the model responses it has
// consumed so far: the usage the provider reported and the wall time spent
// producing it. turn.progress and turn.end report both, so a client can state
// a decode rate that was measured rather than guessed.
type generation struct {
	usage   event.Usage
	elapsed time.Duration
	// context is the last model call's own usage: the prompt it sent plus
	// what it produced, which is what fills the model's context window.
	context event.Usage
	// timings is how fast the last model call ran. Like context it is the
	// last call rather than the turn: a rate summed over calls with prompts
	// of very different sizes describes none of them.
	timings provider.Timings
}

// add folds one finished model response into the turn's totals.
func (g *generation) add(u provider.Usage, elapsed time.Duration, t provider.Timings) {
	g.usage.InputTokens += u.InputTokens
	g.usage.OutputTokens += u.OutputTokens
	g.usage.TotalTokens += u.TotalTokens
	g.elapsed += elapsed
	if u != (provider.Usage{}) {
		g.context = event.Usage{
			InputTokens:  u.InputTokens,
			OutputTokens: u.OutputTokens,
			TotalTokens:  u.TotalTokens,
		}
	}
	if t.Known() {
		g.timings = t
	}
}

// reported returns the last call's timings for a wire payload, and nil when
// nothing measured them.
func (g *generation) reported() *provider.Timings {
	if !g.timings.Known() {
		return nil
	}
	t := g.timings
	return &t
}

// withHarnessDecode fills in a generation rate the endpoint did not report,
// timed from the response's first streamed token. The first token is not
// counted: the clock starts when it arrives, so the window it opens holds the
// tokens that came after it. A response of one token measures nothing.
func withHarnessDecode(t provider.Timings, u provider.Usage, elapsed time.Duration) provider.Timings {
	if t.HasDecode() || u.OutputTokens < 2 || elapsed <= 0 {
		return t
	}
	t.DecodeTokens = u.OutputTokens - 1
	t.DecodeMS = float64(elapsed.Microseconds()) / 1000
	t.Source = provider.TimedByHarness
	return t
}

// providerUsage converts the event protocol's usage shape to the provider
// message shape stored with an assistant response.
func providerUsage(u event.Usage) provider.Usage {
	return provider.Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
	}
}

// response is one model response the agent consumed.
type response struct {
	message provider.Message
	stop    string
	// usage is what the endpoint measured for this response alone.
	usage provider.Usage
}

// call sends the conversation to the model, streaming the response as
// message.delta events, and retries a retryable failure with backoff. It
// returns the response and the request it answered.
//
// With automatic compaction on, a request that would leave less of the
// window free than the reserve is compacted before it is sent, and a request
// the endpoint refuses as too large is compacted and sent again, once each.
// pendingMark is the turn's index of its first undelivered message, or -1;
// a compaction moves it.
func (a *Agent) call(ctx context.Context, s *Session, runID string, prompt []Section, gen *generation, pendingMark *int) (response, Context, error) {
	compacted, recovered := false, false
	for {
		msgs := s.Conversation.Messages()
		sent := a.base(prompt).WithMessages(msgs)
		tokens := estimateContext(sent, msgs)
		if !compacted && a.shouldCompact(tokens) {
			compacted = true
			// A compaction that fails leaves the conversation as it was,
			// which may still fit: the window bound decides.
			if err := a.autoCompact(ctx, s, runID, prompt, pendingMark, event.CompactThreshold); err != nil && ctx.Err() != nil {
				return response{}, Context{}, fmt.Errorf("call model: %w", ctx.Err())
			}
			continue
		}
		sent, err := fit(sent, tokens, a.opts.ContextWindow)
		if err != nil {
			return response{}, Context{}, err
		}
		resp, err := a.retrying(ctx, s, runID, true, func() (response, error) {
			return a.stream(ctx, s, runID, sent.Request(), gen)
		})
		auto := a.opts.Compaction.Auto && !recovered
		if err == nil && auto && silentOverflow(resp, a.opts.ContextWindow) {
			err = errSilentOverflow
		}
		if err == nil {
			return resp, sent, nil
		}
		overflowed := provider.ContextOverflow(err) || errors.Is(err, errSilentOverflow)
		if !auto || ctx.Err() != nil || !overflowed {
			return response{}, Context{}, err
		}
		recovered, compacted = true, true
		a.emit(ctx, s, event.TypeMessageReset, event.MessageReset{RunID: runID})
		if cerr := a.autoCompact(ctx, s, runID, prompt, pendingMark, event.CompactOverflow); cerr != nil {
			return response{}, Context{}, errors.Join(err, cerr)
		}
	}
}

// autoCompact compacts the conversation in the middle of a turn, whose
// undelivered messages begin at *pendingMark, or -1 for none. They stay
// after the kept messages, and *pendingMark moves with them.
func (a *Agent) autoCompact(ctx context.Context, s *Session, runID string, prompt []Section, pendingMark *int, reason string) error {
	stored := s.Conversation.Len()
	if *pendingMark >= 0 {
		stored = *pendingMark
	}
	n, err := a.compact(ctx, s, runID, prompt, stored, reason, "")
	if err != nil {
		return err
	}
	if *pendingMark >= 0 {
		*pendingMark = n
	}
	return nil
}

// retrying runs attempt, and again after each retryable failure up to the
// retry budget, backing off in between. reset announces each retry with
// message.reset, for an attempt that streams to the client. An overflow is
// never retried: the same request fails the same way.
func (a *Agent) retrying(ctx context.Context, s *Session, runID string, reset bool, attempt func() (response, error)) (response, error) {
	backoff := a.opts.RetryBackoff
	for n := 0; ; n++ {
		resp, err := attempt()
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return response{}, fmt.Errorf("call model: %w", ctx.Err())
		}
		if !provider.Retryable(err) || provider.ContextOverflow(err) || n >= a.opts.MaxRetries {
			return response{}, err
		}
		if reset {
			a.emit(ctx, s, event.TypeMessageReset, event.MessageReset{RunID: runID})
		}
		wait := min(backoff, maxRetryBackoff)
		if after := provider.RetryAfter(err); after > wait {
			wait = min(after, maxRetryBackoff)
		}
		a.opts.Logger.Warn("model call failed, retrying",
			"session_id", s.ID, "run_id", runID, "attempt", n+1, "wait", wait, "error", err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return response{}, fmt.Errorf("call model: %w", ctx.Err())
		}
		backoff *= 2
	}
}

// stream consumes one provider response. It emits the answer and the model's
// reasoning as they arrive, and a turn.progress event each time the endpoint
// reports usage, timed from the response's first token so that what a client
// derives from it is measured rather than estimated. A nil gen reads the
// response quietly, emitting and measuring nothing, which is how a summary
// is read.
func (a *Agent) stream(ctx context.Context, s *Session, runID string, req provider.Request, gen *generation) (response, error) {
	events, err := a.provider.Stream(ctx, req)
	if err != nil {
		return response{}, fmt.Errorf("call model: %w", err)
	}
	var text strings.Builder
	var reasoning strings.Builder
	var calls []provider.ToolCall
	var usage provider.Usage
	var timings provider.Timings
	var stop string
	// started is the moment the response began producing tokens; the time
	// before it is the endpoint's queue and prefill, not its generation time.
	var started time.Time
	begin := func() {
		if started.IsZero() {
			started = time.Now()
		}
	}
	done := false
	for e := range events {
		if done {
			return response{}, errors.New("call model: provider sent an event after completion")
		}
		switch e.Kind {
		case provider.KindTextDelta:
			begin()
			text.WriteString(e.Text)
			if gen != nil {
				a.emit(ctx, s, event.TypeMessageDelta, event.MessageDelta{RunID: runID, Text: e.Text})
			}
		case provider.KindReasoningDelta:
			begin()
			reasoning.WriteString(e.ReasoningDelta)
			if gen != nil {
				a.emit(ctx, s, event.TypeReasoningDelta, event.ReasoningDelta{RunID: runID, Text: e.ReasoningDelta})
			}
		case provider.KindToolCallDelta:
			begin()
		case provider.KindToolCall:
			calls = append(calls, e.ToolCall)
		case provider.KindUsage:
			// A usage event can carry only timings: an endpoint may measure
			// its own speed in a chunk that repeats no token counts.
			if e.Usage != (provider.Usage{}) {
				usage = e.Usage
			}
			if e.Timings.Known() {
				timings = e.Timings
			}
			if gen != nil {
				a.emitProgress(ctx, s, runID, gen, usage, timings, started)
			}
		case provider.KindDone:
			stop = e.StopReason
			done = true
		case provider.KindError:
			return response{}, fmt.Errorf("call model: %w", e.Err)
		}
	}
	if !done {
		return response{}, errors.New("call model: provider stream closed without a completion event")
	}
	if stop == "" {
		return response{}, errors.New("call model: provider completion has no stop reason")
	}
	if cutOff(stop) {
		// A response the endpoint cut off is kept, not thrown away. The user
		// watched it stream, and the next turn must send it back to the model
		// or the model reads its own answer as never given and apologises for
		// a turn it did in fact take. Its tool calls go: one cut off partway
		// through its arguments must not run, and an assistant message that
		// carries a call with no result makes the next request malformed.
		calls = nil
	}
	if gen != nil {
		elapsed := elapsedSince(started)
		gen.add(usage, elapsed, withHarnessDecode(timings, usage, elapsed))
	}
	return response{
		message: provider.AssistantMessageWithReasoning(text.String(), reasoning.String(), calls),
		stop:    stop,
		usage:   usage,
	}, nil
}

// empty reports whether an assistant message carries nothing at all.
func empty(m provider.Message) bool {
	return m.Content == "" && m.Reasoning == "" && len(m.ToolCalls) == 0
}

// cutOff reports whether a stop reason means the model stopped before it was
// finished, so the turn ends on what arrived rather than on the model's own
// decision to stop.
func cutOff(stop string) bool {
	return stop == "length" || stop == "content_filter"
}

// emitProgress reports the turn's usage so far, counting the response in
// flight. A response that reported usage before it produced a token has no
// measured generation time yet, which the zero duration says honestly.
func (a *Agent) emitProgress(ctx context.Context, s *Session, runID string, gen *generation, usage provider.Usage, timings provider.Timings, started time.Time) {
	total := *gen
	elapsed := elapsedSince(started)
	total.add(usage, elapsed, withHarnessDecode(timings, usage, elapsed))
	a.emit(ctx, s, event.TypeTurnProgress, event.TurnProgress{
		RunID:         runID,
		Usage:         total.usage,
		Context:       total.context,
		GenerationMS:  total.elapsed.Milliseconds(),
		ContextWindow: a.opts.ContextWindow,
		Timings:       total.reported(),
	})
}

// elapsedSince is the time since start, and zero for a start that never
// happened because the response produced nothing.
func elapsedSince(start time.Time) time.Duration {
	if start.IsZero() {
		return 0
	}
	return time.Since(start)
}
