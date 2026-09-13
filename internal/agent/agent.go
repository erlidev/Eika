package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/tool"
)

// Default bounds for a run. They are values rather than configuration because
// only tests have a reason to change them.
const (
	defaultMaxRetries   = 3
	defaultRetryBackoff = 500 * time.Millisecond
	maxRetryBackoff     = 30 * time.Second
)

// Options configure an Agent. Executor, Emitter, Store, and Logger default to
// something harmless, so the zero value runs a model-only agent.
type Options struct {
	// Model overrides the provider's configured model name.
	Model string
	// MaxTokens bounds one response. Zero leaves it to the provider.
	MaxTokens int
	// Temperature overrides the model default when it is not nil.
	Temperature *float64
	// ReasoningEffort selects how much a reasoning model thinks.
	ReasoningEffort string
	// SystemPrompt is appended after the base prompt and the workspace's
	// context files.
	SystemPrompt string
	// ContextDir is the workspace-relative directory whose context files
	// apply. Empty means the workspace root.
	ContextDir string
	// Queue decides how many follow-up messages one turn delivers.
	Queue QueueMode
	// MaxRetries bounds how often a retryable provider failure is retried.
	MaxRetries int
	// RetryBackoff is the delay before the first retry; it doubles after
	// each attempt.
	RetryBackoff time.Duration
	// Executor is what tools act through. A nil executor means the agent has
	// no workspace: tools that need one fail and no context files are read.
	Executor executor.Executor
	// Emitter receives the run's events. Nil drops them.
	Emitter event.Emitter
	// Store persists every message. Nil keeps them in the conversation only.
	Store Store
	// Logger receives one line per finished or failed turn.
	Logger *slog.Logger
}

// Agent runs the loop for one session at a time.
type Agent struct {
	provider  provider.Provider
	tools     *tool.Registry
	opts      Options
	steering  queue
	followUps queue
}

// New returns an Agent that calls p and may use the tools in r, which may be
// nil when the agent has no tools.
func New(p provider.Provider, r *tool.Registry, opts Options) *Agent {
	if opts.Queue == "" {
		opts.Queue = QueueOneAtATime
	}
	if opts.MaxRetries == 0 {
		opts.MaxRetries = defaultMaxRetries
	}
	if opts.RetryBackoff <= 0 {
		opts.RetryBackoff = defaultRetryBackoff
	}
	if opts.Emitter == nil {
		opts.Emitter = event.Discard
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Agent{provider: p, tools: r, opts: opts}
}

// Steer queues a message to be delivered as soon as the tool call that is
// running finishes, before the next model call.
func (a *Agent) Steer(msg string) { a.steering.push(msg) }

// FollowUp queues a message to be delivered after the current turn ends.
func (a *Agent) FollowUp(msg string) { a.followUps.push(msg) }

// PendingSteering returns the steering messages that have not been delivered.
func (a *Agent) PendingSteering() []string { return a.steering.pending() }

// PendingFollowUps returns the follow-up messages that have not been
// delivered.
func (a *Agent) PendingFollowUps() []string { return a.followUps.pending() }

// Run appends userMessage to the session and drives turns until the model asks
// for no more tools and no follow-up message is queued. Cancelling ctx aborts
// the run and puts the messages it had taken from a queue back.
func (a *Agent) Run(ctx context.Context, s *Session, userMessage string) error {
	if s == nil || s.Conversation == nil {
		return errors.New("run agent: session has no conversation")
	}
	system, err := a.systemPrompt(ctx)
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}

	msgs, origin := []string{userMessage}, (*queue)(nil)
	if userMessage == "" {
		msgs = nil
	}
	for {
		if err := a.turn(ctx, s, system, msgs, origin); err != nil {
			return err
		}
		count := 1
		if a.opts.Queue == QueueAll {
			count = 0
		}
		msgs, origin = a.followUps.take(count), &a.followUps
		if len(msgs) == 0 {
			return nil
		}
	}
}

// turn runs one assistant turn: deliver msgs, then alternate model calls and
// tool calls until the model stops asking for tools. origin is the queue msgs
// came from, so that an aborted turn can put them back.
func (a *Agent) turn(ctx context.Context, s *Session, system string, msgs []string, origin *queue) error {
	runID := newRunID()
	// Undelivered messages are in the conversation, because the model call
	// needs them, but not yet in the store. A turn that never gets a response
	// takes them out again, so that the queued copies are not replayed twice.
	pendingMark := -1
	var pending []provider.Message
	addPending := func(m provider.Message) {
		if pendingMark < 0 {
			pendingMark = s.Conversation.Len()
		}
		s.Conversation.Append(m)
		pending = append(pending, m)
	}
	for _, m := range msgs {
		addPending(provider.UserMessage(m))
	}
	a.emit(ctx, s, event.TypeTurnStart, event.TurnStart{
		RunID:       runID,
		SessionID:   s.ID,
		WorkspaceID: s.WorkspaceID,
		Message:     strings.Join(msgs, "\n\n"),
	})

	var steered []string
	var usage event.Usage
	// restore undoes everything the turn took but never got a model response
	// for: the pending messages leave the conversation and the queued copies
	// go back where they came from.
	restore := func() {
		if pendingMark >= 0 {
			s.Conversation.Truncate(pendingMark)
		}
		pending, pendingMark = nil, -1
		if origin != nil {
			origin.unshift(msgs)
		}
		a.steering.unshift(steered)
	}

	for {
		for _, m := range a.steering.drain() {
			addPending(provider.UserMessage(m))
			steered = append(steered, m)
		}
		if err := ctx.Err(); err != nil {
			restore()
			return a.fail(ctx, s, runID, fmt.Errorf("run turn: %w", err))
		}

		reply, turnUsage, stop, err := a.call(ctx, s, runID, system)
		if err != nil {
			restore()
			return a.fail(ctx, s, runID, err)
		}
		usage.InputTokens += turnUsage.InputTokens
		usage.OutputTokens += turnUsage.OutputTokens
		usage.TotalTokens += turnUsage.TotalTokens
		// The model has seen the pending messages; they are delivered for
		// good and can be persisted.
		for _, m := range pending {
			if err := a.store(ctx, s, m); err != nil {
				return err
			}
		}
		pending, pendingMark = nil, -1
		msgs, steered, origin = nil, nil, nil
		if err := a.append(ctx, s, reply); err != nil {
			return err
		}

		if len(reply.ToolCalls) == 0 {
			a.emit(ctx, s, event.TypeTurnEnd, event.TurnEnd{RunID: runID, StopReason: stop, Usage: usage})
			a.opts.Logger.Info("turn finished",
				"session_id", s.ID, "run_id", runID, "total_tokens", usage.TotalTokens)
			return nil
		}
		for i, call := range reply.ToolCalls {
			if err := ctx.Err(); err != nil {
				err = fmt.Errorf("run turn: %w", err)
				a.abandonToolCalls(ctx, s, runID, reply.ToolCalls[i:], err)
				return a.fail(ctx, s, runID, err)
			}
			result, err := a.callTool(ctx, s, runID, call)
			if err != nil {
				a.abandonToolCalls(ctx, s, runID, reply.ToolCalls[i:], err)
				return a.fail(ctx, s, runID, err)
			}
			if err := a.append(ctx, s, result); err != nil {
				return err
			}
		}
	}
}

// abandonToolCalls answers the calls a failed turn never ran. Every tool call
// in an assistant message needs a result, or the next request to the model is
// malformed and the session cannot be resumed.
func (a *Agent) abandonToolCalls(ctx context.Context, s *Session, runID string, calls []provider.ToolCall, cause error) {
	for _, c := range calls {
		content := fmt.Sprintf("the run stopped before this tool call finished: %v", cause)
		a.emit(ctx, s, event.TypeToolResult, event.ToolResult{
			RunID:   runID,
			CallID:  c.ID,
			Name:    c.Name,
			Content: content,
			IsError: true,
		})
		if err := a.append(ctx, s, provider.ToolResultMessage(c.ID, content, true)); err != nil {
			a.opts.Logger.Error("store abandoned tool result",
				"session_id", s.ID, "run_id", runID, "call_id", c.ID, "error", err)
		}
	}
}

// call sends the conversation to the model, streaming the response as
// message.delta events, and retries a retryable failure with backoff.
func (a *Agent) call(ctx context.Context, s *Session, runID, system string) (provider.Message, provider.Usage, string, error) {
	req := provider.Request{
		Model:           a.opts.Model,
		System:          system,
		Messages:        s.Conversation.Messages(),
		MaxTokens:       a.opts.MaxTokens,
		Temperature:     a.opts.Temperature,
		ReasoningEffort: a.opts.ReasoningEffort,
	}
	if a.tools != nil {
		req.Tools = a.tools.Schemas()
	}

	backoff := a.opts.RetryBackoff
	for attempt := 0; ; attempt++ {
		msg, usage, stop, err := a.stream(ctx, s, runID, req)
		if err == nil {
			return msg, usage, stop, nil
		}
		if ctx.Err() != nil {
			return provider.Message{}, provider.Usage{}, "", fmt.Errorf("call model: %w", ctx.Err())
		}
		if !provider.Retryable(err) || attempt >= a.opts.MaxRetries {
			return provider.Message{}, provider.Usage{}, "", err
		}
		wait := min(backoff, maxRetryBackoff)
		if after := provider.RetryAfter(err); after > wait {
			wait = min(after, maxRetryBackoff)
		}
		a.opts.Logger.Warn("model call failed, retrying",
			"session_id", s.ID, "run_id", runID, "attempt", attempt+1, "wait", wait, "error", err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return provider.Message{}, provider.Usage{}, "", fmt.Errorf("call model: %w", ctx.Err())
		}
		backoff *= 2
	}
}

// stream consumes one provider response.
func (a *Agent) stream(ctx context.Context, s *Session, runID string, req provider.Request) (provider.Message, provider.Usage, string, error) {
	events, err := a.provider.Stream(ctx, req)
	if err != nil {
		return provider.Message{}, provider.Usage{}, "", fmt.Errorf("call model: %w", err)
	}
	var text strings.Builder
	var calls []provider.ToolCall
	var usage provider.Usage
	var stop string
	for e := range events {
		switch e.Kind {
		case provider.KindTextDelta:
			text.WriteString(e.Text)
			a.emit(ctx, s, event.TypeMessageDelta, event.MessageDelta{RunID: runID, Text: e.Text})
		case provider.KindToolCall:
			calls = append(calls, e.ToolCall)
		case provider.KindUsage:
			usage = e.Usage
		case provider.KindDone:
			stop = e.StopReason
		case provider.KindError:
			return provider.Message{}, provider.Usage{}, "", fmt.Errorf("call model: %w", e.Err)
		}
	}
	return provider.AssistantMessage(text.String(), calls), usage, stop, nil
}

// callTool runs one tool call and reports it as tool.call and tool.result
// events. A tool that fails for a reason the model can act on returns a result
// with IsError set; an error here is the harness's own failure.
func (a *Agent) callTool(ctx context.Context, s *Session, runID string, c provider.ToolCall) (provider.Message, error) {
	a.emit(ctx, s, event.TypeToolCall, event.ToolCall{
		RunID:     runID,
		CallID:    c.ID,
		Name:      c.Name,
		Arguments: c.Arguments,
	})

	start := time.Now()
	result, err := a.dispatch(ctx, s, runID, c)
	if err != nil {
		return provider.Message{}, fmt.Errorf("run tool %s: %w", c.Name, err)
	}
	a.emit(ctx, s, event.TypeToolResult, event.ToolResult{
		RunID:      runID,
		CallID:     c.ID,
		Name:       c.Name,
		Content:    result.Content,
		IsError:    result.IsError,
		Details:    result.Details,
		DurationMS: time.Since(start).Milliseconds(),
	})
	return provider.ToolResultMessage(c.ID, result.Content, result.IsError), nil
}

// dispatch finds the tool and runs it. An unknown tool is the model's mistake,
// not the harness's, so it comes back as a failed result.
func (a *Agent) dispatch(ctx context.Context, s *Session, runID string, c provider.ToolCall) (tool.Result, error) {
	if a.tools == nil {
		return tool.Errorf("no tools are available in this session"), nil
	}
	t, ok := a.tools.Get(c.Name)
	if !ok {
		return tool.Errorf("unknown tool %q", c.Name), nil
	}
	if a.opts.Executor == nil {
		return tool.Errorf("this session has no workspace, so %s cannot run", c.Name), nil
	}
	return t.Call(ctx, tool.CallContext{
		Exec:        a.opts.Executor,
		Emit:        a.opts.Emitter,
		WorkspaceID: s.WorkspaceID,
		SessionID:   s.ID,
		RunID:       runID,
		CallID:      c.ID,
	}, c.Arguments)
}

// append records a message in the conversation and in the store.
func (a *Agent) append(ctx context.Context, s *Session, m provider.Message) error {
	s.Conversation.Append(m)
	return a.store(ctx, s, m)
}

// store records a message that is already in the conversation.
func (a *Agent) store(ctx context.Context, s *Session, m provider.Message) error {
	if a.opts.Store == nil {
		return nil
	}
	if err := a.opts.Store.Append(ctx, s.ID, m); err != nil {
		return fmt.Errorf("store message: %w", err)
	}
	return nil
}

// fail reports a run failure as an event and returns it.
func (a *Agent) fail(ctx context.Context, s *Session, runID string, err error) error {
	a.opts.Logger.Error("run failed", "session_id", s.ID, "run_id", runID, "error", err)
	a.emit(ctx, s, event.TypeRunError, event.RunError{
		RunID:     runID,
		Message:   err.Error(),
		Retryable: provider.Retryable(err),
	})
	return err
}

// emit sends one event for the session's topic. An event that cannot be
// encoded is a programming error in a payload struct, so it is logged and
// dropped rather than failing the run.
func (a *Agent) emit(ctx context.Context, s *Session, typ string, payload any) {
	e, err := event.New(typ, event.SessionTopic(s.ID), payload)
	if err != nil {
		a.opts.Logger.Error("encode event", "type", typ, "session_id", s.ID, "error", err)
		return
	}
	a.opts.Emitter.Emit(ctx, e)
}

// newRunID returns an identifier for one turn.
func newRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(b[:])
}
