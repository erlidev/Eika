package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/tool"
)

// Default bounds for a run. They are values rather than configuration because
// only tests have a reason to change them.
const (
	defaultMaxRetries    = 3
	defaultRetryBackoff  = 500 * time.Millisecond
	maxRetryBackoff      = 30 * time.Second
	terminalWriteTimeout = 30 * time.Second
)

// Options configure an Agent. Executor, Emitter, Store, and Logger default to
// something harmless, so the zero value runs a model-only agent.
type Options struct {
	// Model overrides the provider's configured model name.
	Model string
	// ContextWindow bounds the input and requested output of one model call.
	// Zero disables the preflight bound.
	ContextWindow int
	// Sampling holds the sampling parameters every model call sends; a nil
	// field is left to the endpoint.
	Sampling provider.Sampling
	// ThinkingSwitch is the request field that carries the effort "none".
	ThinkingSwitch provider.ThinkingSwitch
	// PreserveThinking keeps compatible Chat Completions reasoning data in
	// assistant messages and replays it on later model calls.
	PreserveThinking bool
	// BasePrompt replaces the built-in base prompt when it is not nil, the
	// empty string included. The built-in one is WorkspacePrompt for an
	// agent with an Executor and ChatPrompt for one without, so the caller
	// passes the override for the agent's kind.
	BasePrompt *string
	// SkipContextFiles leaves the workspace's context files out of the
	// system prompt; none are read.
	SkipContextFiles bool
	// Instructions follow the base prompt and the workspace's context files
	// in the system prompt.
	Instructions string
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
	// no workspace, as in a chat: tools that need one fail, standalone tools
	// run with a nil executor, no context files are read, and the system
	// prompt says there is no workspace.
	Executor executor.Executor
	// Emitter receives the run's events. Nil drops them.
	Emitter event.Emitter
	// Store persists every message. Nil keeps them in the conversation only.
	Store Store
	// Logger receives one line per finished or failed turn.
	Logger *slog.Logger
	// Recorder is told about every model call that returned a response. Nil
	// records nothing.
	Recorder Recorder
}

// Agent runs the loop for one session at a time.
type Agent struct {
	provider  provider.Provider
	tools     *tool.Registry
	opts      Options
	steering  queue
	followUps queue

	mu sync.Mutex
	// accepting is guarded by mu. Closing it in the same critical section
	// that checks both queues prevents a message from being accepted after
	// the loop has made its final queue check.
	accepting bool
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
	return &Agent{provider: p, tools: r, opts: opts, accepting: true}
}

// Steer queues a message to be delivered as soon as the tool call that is
// running finishes, before the next model call.
func (a *Agent) Steer(msg string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.accepting {
		return false
	}
	a.steering.push(msg)
	return true
}

// FollowUp queues a message to be delivered after the current turn ends.
func (a *Agent) FollowUp(msg string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.accepting {
		return false
	}
	a.followUps.push(msg)
	return true
}

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
	prompt, err := a.prompt(ctx)
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}
	a.mu.Lock()
	a.accepting = true
	a.mu.Unlock()
	defer a.closeQueues()

	msgs, origin := []string{userMessage}, (*queue)(nil)
	if userMessage == "" {
		msgs = nil
	}
	for {
		if err := a.turn(ctx, s, prompt, msgs, origin); err != nil {
			return err
		}
		msgs, origin = a.nextQueued()
		if len(msgs) == 0 {
			return nil
		}
	}
}

// turn runs one assistant turn: deliver msgs, then alternate model calls and
// tool calls until the model stops asking for tools. prompt is the run's
// system prompt by section. origin is the queue msgs came from, so that an
// aborted turn can put them back.
func (a *Agent) turn(ctx context.Context, s *Session, prompt []Section, msgs []string, origin *queue) error {
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
	var gen generation
	// restoreFrom keeps the durably stored prefix and takes back the suffix.
	// Queue messages in the suffix return to the queue they came from.
	restoreFrom := func(stored int) {
		if pendingMark >= 0 {
			s.Conversation.Truncate(pendingMark + stored)
		}
		pending, pendingMark = nil, -1
		originStored := min(stored, len(msgs))
		if origin != nil && originStored < len(msgs) {
			origin.unshift(msgs[originStored:])
		}
		steeringStored := max(stored-len(msgs), 0)
		if steeringStored < len(steered) {
			a.steering.unshift(steered[steeringStored:])
		}
		msgs, steered, origin = nil, nil, nil
	}

	for {
		for _, m := range a.drainSteering() {
			addPending(provider.UserMessage(m))
			steered = append(steered, m)
		}
		if err := ctx.Err(); err != nil {
			restoreFrom(0)
			return a.fail(ctx, s, runID, fmt.Errorf("run turn: %w", err))
		}

		resp, sent, err := a.call(ctx, s, runID, prompt, &gen)
		if err != nil {
			restoreFrom(0)
			return a.fail(ctx, s, runID, err)
		}
		// The model has seen the pending messages; they are delivered for
		// good and can be persisted.
		for i, m := range pending {
			if err := a.store(ctx, s, m); err != nil {
				restoreFrom(i)
				return a.fail(ctx, s, runID, err)
			}
		}
		pending, pendingMark = nil, -1
		msgs, steered, origin = nil, nil, nil
		// Everything the call sent is stored and its reply is not yet, which
		// is the moment Recorder promises.
		a.record(ctx, s, runID, sent, resp.usage)
		reply, stop := resp.message, resp.stop
		if gen.context != (event.Usage{}) {
			reply.Metrics = &provider.MessageMetrics{
				RunID:         runID,
				Usage:         providerUsage(gen.usage),
				Context:       providerUsage(gen.context),
				GenerationMS:  gen.elapsed.Milliseconds(),
				ContextWindow: a.opts.ContextWindow,
				Timings:       gen.reported(),
			}
		}
		appendReply := a.append
		if len(reply.ToolCalls) != 0 {
			appendReply = a.appendTerminal
		}
		// A cut-off response can leave nothing behind: a tool call that ran
		// out of room before it produced any prose. An empty assistant turn
		// tells the model nothing and some endpoints refuse to be sent one,
		// so the turn ends on the stop reason alone.
		if !empty(reply) {
			if err := appendReply(ctx, s, reply); err != nil {
				return err
			}
		}

		if len(reply.ToolCalls) == 0 {
			a.emit(ctx, s, event.TypeTurnEnd, event.TurnEnd{
				RunID:         runID,
				StopReason:    stop,
				Usage:         gen.usage,
				Context:       gen.context,
				GenerationMS:  gen.elapsed.Milliseconds(),
				ContextWindow: a.opts.ContextWindow,
				Timings:       gen.reported(),
			})
			a.opts.Logger.Info("turn finished",
				"session_id", s.ID, "run_id", runID, "total_tokens", gen.usage.TotalTokens)
			return nil
		}
		for i, call := range reply.ToolCalls {
			if err := ctx.Err(); err != nil {
				err = fmt.Errorf("run turn: %w", err)
				err = errors.Join(err, a.abandonToolCalls(ctx, s, runID, reply.ToolCalls[i:], err))
				return a.fail(ctx, s, runID, err)
			}
			result, err := a.callTool(ctx, s, runID, call)
			if err != nil {
				err = errors.Join(err, a.abandonToolCalls(ctx, s, runID, reply.ToolCalls[i:], err))
				return a.fail(ctx, s, runID, err)
			}
			if err := a.appendTerminal(ctx, s, result); err != nil {
				err = errors.Join(err, a.abandonToolCalls(ctx, s, runID, reply.ToolCalls[i:], err))
				return a.fail(ctx, s, runID, err)
			}
		}
	}
}

// abandonToolCalls answers the calls a failed turn never ran. Every tool call
// in an assistant message needs a result, or the next request to the model is
// malformed and the session cannot be resumed.
func (a *Agent) abandonToolCalls(ctx context.Context, s *Session, runID string, calls []provider.ToolCall, cause error) error {
	terminalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
	defer cancel()
	var joined error
	for _, c := range calls {
		content := fmt.Sprintf("the run stopped before this tool call finished: %v", cause)
		a.emit(terminalCtx, s, event.TypeToolResult, event.ToolResult{
			RunID:   runID,
			CallID:  c.ID,
			Name:    c.Name,
			Content: content,
			IsError: true,
		})
		if err := a.append(terminalCtx, s, provider.ToolResultMessage(c.ID, content, true)); err != nil {
			a.opts.Logger.Error("store abandoned tool result",
				"session_id", s.ID, "run_id", runID, "call_id", c.ID, "error", err)
			joined = errors.Join(joined, err)
		}
	}
	return joined
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
	if a.opts.Executor == nil && tool.NeedsWorkspace(t) {
		return tool.Errorf("this session has no workspace, so %s cannot run", c.Name), nil
	}
	return t.Call(ctx, tool.CallContext{
		Exec:        a.opts.Executor,
		Emit:        a.opts.Emitter,
		WorkspaceID: s.WorkspaceID,
		SessionID:   s.ID,
		RunID:       runID,
		CallID:      c.ID,
	}, json.RawMessage(c.Arguments))
}

// appendTerminal records a terminal tool result on a context that survives a
// run cancellation. A stored assistant tool call must always have one result.
func (a *Agent) appendTerminal(ctx context.Context, s *Session, m provider.Message) error {
	terminalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
	defer cancel()
	return a.append(terminalCtx, s, m)
}

// drainSteering atomically takes the steering accepted before this check.
func (a *Agent) drainSteering() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.steering.drain()
}

// nextQueued selects the next user turn. Steering accepted while the final
// model response streamed becomes a new turn. If both queues are empty, this
// closes acceptance atomically with the final check.
func (a *Agent) nextQueued() ([]string, *queue) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if msgs := a.steering.drain(); len(msgs) != 0 {
		return msgs, &a.steering
	}
	count := 1
	if a.opts.Queue == QueueAll {
		count = 0
	}
	if msgs := a.followUps.take(count); len(msgs) != 0 {
		return msgs, &a.followUps
	}
	a.accepting = false
	return nil, nil
}

// closeQueues rejects new messages before Run returns on an error path.
func (a *Agent) closeQueues() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.accepting = false
}

// append records a message in the conversation and in the store.
func (a *Agent) append(ctx context.Context, s *Session, m provider.Message) error {
	if err := a.store(ctx, s, m); err != nil {
		return err
	}
	s.Conversation.Append(m)
	return nil
}

// store durably records one conversation message.
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
