package agent

import (
	"context"
	"encoding/json"
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
func (a *Agent) call(ctx context.Context, s *Session, runID string, prompt []Section, gen *generation) (response, Context, error) {
	sent := a.assemble(s, prompt)
	req := sent.Request()
	if err := withinContextWindow(req, a.opts.ContextWindow); err != nil {
		return response{}, Context{}, err
	}

	backoff := a.opts.RetryBackoff
	for attempt := 0; ; attempt++ {
		resp, err := a.stream(ctx, s, runID, req, gen)
		if err == nil {
			return resp, sent, nil
		}
		if ctx.Err() != nil {
			return response{}, Context{}, fmt.Errorf("call model: %w", ctx.Err())
		}
		if !provider.Retryable(err) || attempt >= a.opts.MaxRetries {
			return response{}, Context{}, err
		}
		a.emit(ctx, s, event.TypeMessageReset, event.MessageReset{RunID: runID})
		wait := min(backoff, maxRetryBackoff)
		if after := provider.RetryAfter(err); after > wait {
			wait = min(after, maxRetryBackoff)
		}
		a.opts.Logger.Warn("model call failed, retrying",
			"session_id", s.ID, "run_id", runID, "attempt", attempt+1, "wait", wait, "error", err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return response{}, Context{}, fmt.Errorf("call model: %w", ctx.Err())
		}
		backoff *= 2
	}
}

// stream consumes one provider response. It emits the answer and the model's
// reasoning as they arrive, and a turn.progress event each time the endpoint
// reports usage, timed from the response's first token so that what a client
// derives from it is measured rather than estimated.
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
			a.emit(ctx, s, event.TypeMessageDelta, event.MessageDelta{RunID: runID, Text: e.Text})
		case provider.KindReasoningDelta:
			begin()
			reasoning.WriteString(e.ReasoningDelta)
			a.emit(ctx, s, event.TypeReasoningDelta, event.ReasoningDelta{RunID: runID, Text: e.ReasoningDelta})
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
			a.emitProgress(ctx, s, runID, gen, usage, timings, started)
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
	elapsed := elapsedSince(started)
	gen.add(usage, elapsed, withHarnessDecode(timings, usage, elapsed))
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

// withinContextWindow applies a conservative token upper bound. Chat
// Completions tokenizers encode UTF-8 bytes into no more tokens than bytes;
// using the JSON wire size also includes message and tool framing. The
// request's messages are the ones assemble prepared, which carry nothing the
// provider does not receive.
func withinContextWindow(req provider.Request, limit int) error {
	if limit <= 0 {
		return nil
	}
	data, err := json.Marshal(struct {
		System   string             `json:"system"`
		Messages []provider.Message `json:"messages"`
		Tools    []provider.ToolDef `json:"tools"`
	}{req.System, req.Messages, req.Tools})
	if err != nil {
		return fmt.Errorf("call model: estimate context: %w", err)
	}
	estimated := len(data)
	if m := req.Sampling.MaxOutput; m != nil {
		estimated += *m
	}
	if estimated > limit {
		return fmt.Errorf("call model: context upper bound %d exceeds configured window %d", estimated, limit)
	}
	return nil
}
