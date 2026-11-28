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
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/sentry"
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
	// Channel names the source of user messages in a home conversation.
	Channel string
	// Notices hands the next user-side message the notices its <context>
	// block delivers, and takes them from whoever holds them: each notice is
	// handed out once. It is asked once per message, when the message is
	// first sent. Nil means no notices.
	Notices func() []Notice
	// RefreshMessage updates only the Context of an unstored message just before
	// it is delivered. It must be safe to call again. Nil leaves it unchanged.
	RefreshMessage func(provider.Message) provider.Message
	// Model overrides the provider's configured model name.
	Model string
	// ContextWindow bounds the input and requested output of one model call.
	// Zero disables the bound and automatic compaction.
	ContextWindow int
	// Compaction says when and how the conversation is compacted.
	Compaction CompactionSettings
	// Sampling holds the sampling parameters every model call sends; a nil
	// field is left to the endpoint.
	Sampling provider.Sampling
	// ThinkingSwitch is the request field that carries the effort "none".
	ThinkingSwitch provider.ThinkingSwitch
	// PreserveThinking keeps compatible Chat Completions reasoning data in
	// assistant messages and replays it on later model calls.
	PreserveThinking bool
	// ImageInput says the model reads images. Without it, the images in the
	// conversation are replaced by a note saying they were left out.
	ImageInput bool
	// BasePrompt replaces the built-in ChatPrompt when it is not nil, the
	// empty string included.
	BasePrompt *string
	// Instructions follow the base prompt in the system prompt.
	Instructions string
	// MaxRetries bounds how often a retryable provider failure is retried.
	MaxRetries int
	// RetryBackoff is the delay before the first retry; it doubles after
	// each attempt.
	RetryBackoff time.Duration
	// Gate decides every tool call before it runs. A deny, an ask (which
	// phase 11 will park; until then it is a deny) or a gate that cannot
	// answer in time makes the call return a Blocked result and not run. The
	// model is never told a gate exists. Nil means no gate, for tests and
	// evals: the server always sets it.
	Gate sentry.Gate
	// Executor is what tools act through. A nil executor means the agent has
	// no computer, as in a chat: tools that need one fail, standalone tools
	// run with a nil executor, no context files are read, and the system
	// prompt says there is no computer.
	Executor executor.Executor
	// Emitter receives the run's events. Nil drops them.
	Emitter event.Emitter
	// Store persists every message. Nil keeps them in the conversation only.
	Store Store
	// RefreshSnapshot builds the session's replacement snapshot, asks
	// record for the compaction measured against it, stores the two
	// together, and returns the snapshot, which the run goes on with. Nil
	// stores the compaction alone, through Store.
	RefreshSnapshot func(ctx context.Context, sessionID string, record func(Snapshot) Compaction) (Snapshot, error)
	// Logger receives one line per finished or failed turn.
	Logger *slog.Logger
	// Recorder is told about every model call that returned a response. Nil
	// records nothing.
	Recorder Recorder
	// Counter predicts the size of every request before it is sent and learns
	// from what the endpoint measured. It belongs to one session and is kept
	// across the runs of that session, which is how the constants it learns
	// survive from one turn to the next. Nil means a counter of the agent's
	// own that estimates by bytes, the way an endpoint with no tokenizer is
	// counted.
	Counter *counter.Counter
	// CountTimeout bounds the token counting around one model call. Zero means
	// ten seconds. A call whose prediction does not finish in time is sent with
	// the counter's estimate.
	CountTimeout time.Duration
	// Inference says how the agent's model calls wait for the GPU. The zero
	// value takes no lease.
	Inference Inference
	// Clock tells the time the <context> block of a user-side message states,
	// and the loop's wall-time limit measures against. Nil means time.Now.
	Clock func() time.Time
	// Location is the zone the block states its time in. Nil means UTC.
	Location *time.Location
	// Limits bound each run: its model calls and its wall time.
	Limits Limits
}

// MemorySnapshot is the memory section at the end of a stored preamble.
type MemorySnapshot struct {
	// Revision identifies the memories the section was built from.
	Revision string `json:"revision"`
	// Text is the section, exactly as it ends the preamble.
	Text string `json:"text"`
	// ChangeSeq is the last memory change the section already includes; later
	// changes are named in the tail of the next message. It is set even when
	// there is no section.
	ChangeSeq int64 `json:"change_seq,omitempty"`
}

// Snapshot is what a session's prefix holds beside its messages and is
// replaced only by a compaction (runtime.md 4): the preamble, and the base
// prompt and tools of the agent definition as they were when it was taken.
type Snapshot struct {
	// Preamble is the stored preamble text, memory section included.
	Preamble string
	// Memory is the memory section the preamble ends with and the revision
	// it was read at; zero when the snapshot has none.
	Memory MemorySnapshot
	// Prompt replaces the base prompt; nil keeps the agent's.
	Prompt *string
	// Tools replaces the tools; nil keeps the agent's.
	Tools *tool.Registry
}

// Agent runs the loop for one session at a time.
type Agent struct {
	provider  provider.Provider
	counter   *counter.Counter
	opts      Options
	steering  queue
	followUps queue

	// snapshotMu guards what a compaction's snapshot replaces: the tools,
	// the base prompt in opts, and the location.
	snapshotMu sync.RWMutex
	tools      *tool.Registry
	location   *time.Location

	mu sync.Mutex
	// accepting is guarded by mu. Closing it in the same critical section
	// that checks both queues prevents a message from being accepted after
	// the loop has made its final queue check.
	accepting bool
}

// New returns an Agent that calls p and may use the tools in r, which may be
// nil when the agent has no tools.
func New(p provider.Provider, r *tool.Registry, opts Options) *Agent {
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
	if opts.CountTimeout <= 0 {
		opts.CountTimeout = DefaultCountTimeout
	}
	if opts.Counter == nil {
		opts.Counter = counter.NewEstimating()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.UTC
	}
	opts.Limits = opts.Limits.withDefaults()
	return &Agent{provider: p, counter: opts.Counter, tools: r, opts: opts, location: opts.Location, accepting: true}
}

// useSnapshot makes a compaction's replacement snapshot the session's and
// the run's: the next model call is built from its preamble, prompt and
// tools, and states times in the preamble's zone.
func (a *Agent) useSnapshot(s *Session, snap Snapshot) {
	s.Preamble, s.Memory = snap.Preamble, snap.Memory
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()
	a.location = PreambleLocation(snap.Preamble)
	if snap.Prompt != nil {
		s.Prompt = snap.Prompt
		a.opts.BasePrompt = snap.Prompt
	}
	if snap.Tools != nil {
		names := []string{}
		for _, t := range snap.Tools.List() {
			names = append(names, t.Name())
		}
		s.Tools = names
		a.tools = snap.Tools
	}
}

// registry is the tools the agent offers now.
func (a *Agent) registry() *tool.Registry {
	a.snapshotMu.RLock()
	defer a.snapshotMu.RUnlock()
	return a.tools
}

// Steer queues a user message to be delivered as soon as the tool call that
// is running finishes, before the next model call.
func (a *Agent) Steer(msg provider.Message) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.accepting {
		return false
	}
	a.steering.push(a.stamp(msg))
	return true
}

// FollowUp queues a user message to be delivered after the current turn
// ends.
func (a *Agent) FollowUp(msg provider.Message) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.accepting {
		return false
	}
	a.followUps.push(a.stamp(msg))
	return true
}

// PendingSteering returns the steering messages that have not been delivered.
func (a *Agent) PendingSteering() []provider.Message { return a.steering.pending() }

// PendingFollowUps returns the follow-up messages that have not been
// delivered.
func (a *Agent) PendingFollowUps() []provider.Message { return a.followUps.pending() }

// Run appends userMessage to the session and drives turns until the model asks
// for no more tools and no follow-up message is queued. An empty userMessage
// sends nothing new and answers the conversation as it stands, unless there
// are notices to deliver: then it is sent with its <context> block alone.
// Cancelling ctx aborts the run and puts the messages it had taken from a
// queue back.
func (a *Agent) Run(ctx context.Context, s *Session, userMessage provider.Message) error {
	if s == nil || s.Conversation == nil {
		return errors.New("run agent: session has no conversation")
	}
	a.mu.Lock()
	a.accepting = true
	a.mu.Unlock()
	defer a.closeQueues()
	a.seedCounter(ctx, s, a.prompt())

	first := a.stamp(userMessage)
	msgs, origin := []provider.Message{first}, (*queue)(nil)
	if first.Empty() && len(first.Notices) == 0 {
		msgs = nil
	}
	g := newGuard(a.opts.Clock())
	for {
		if err := a.turn(ctx, s, g, msgs, origin); err != nil {
			if errors.Is(err, errRunLimit) {
				return nil
			}
			return err
		}
		msgs, origin = a.nextQueued()
		if len(msgs) == 0 {
			return nil
		}
	}
}

// turn runs one assistant turn: deliver msgs, then alternate model calls and
// tool calls until the model stops asking for tools. g follows the run
// against its limits. origin is the queue msgs came from, so that an aborted
// turn can put them back. A turn that finds its run at a limit before a
// model call ends with errRunLimit, after the turn.end event and with
// everything it did stored and the messages it had not delivered back in
// their queues.
func (a *Agent) turn(ctx context.Context, s *Session, g *guard, msgs []provider.Message, origin *queue) error {
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
	texts := make([]string, 0, len(msgs))
	var images []provider.Image
	for i, m := range msgs {
		if a.opts.RefreshMessage != nil {
			m = a.opts.RefreshMessage(m)
			msgs[i] = m
		}
		addPending(m)
		if m.Content != "" {
			texts = append(texts, m.Content)
		}
		images = append(images, m.Images...)
	}
	a.emit(ctx, s, event.TypeTurnStart, event.TurnStart{
		RunID:      runID,
		SessionID:  s.ID,
		ComputerID: s.ComputerID,
		Message:    strings.Join(texts, "\n\n"),
		Images:     images,
	})

	var steered []provider.Message
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
		// The limits are checked before steering is taken from its queue, so a
		// message that arrived while the last tool ran stays queued for the run
		// that continues, and not half-delivered.
		if reason := g.stop(a.opts.Clock(), a.opts.Limits); reason != "" {
			restoreFrom(0)
			a.endTurn(ctx, s, runID, reason, &gen)
			a.opts.Logger.Info("run stopped at its limit",
				"session_id", s.ID, "run_id", runID, "reason", reason, "model_calls", g.calls)
			return errRunLimit
		}
		for _, m := range a.drainSteering() {
			if a.opts.RefreshMessage != nil {
				m = a.opts.RefreshMessage(m)
			}
			addPending(m)
			steered = append(steered, m)
		}
		if err := ctx.Err(); err != nil {
			restoreFrom(0)
			return a.fail(ctx, s, runID, fmt.Errorf("run turn: %w", err))
		}

		g.calls++
		resp, sent, count, err := a.call(ctx, s, runID, &gen, &pendingMark)
		if err != nil {
			restoreFrom(0)
			return a.fail(ctx, s, runID, err)
		}
		// The final delivery check can update unstored message contexts while
		// the call waits for its inference lease. Store exactly what was sent.
		if pendingMark >= 0 {
			messages := s.Conversation.Messages()
			copy(pending, messages[pendingMark:])
		}
		// The model has seen the pending messages; they are delivered for
		// good and can be persisted.
		for i, m := range pending {
			if err := a.store(ctx, s, runID, m); err != nil {
				restoreFrom(i)
				return a.fail(ctx, s, runID, err)
			}
		}
		pending, pendingMark = nil, -1
		msgs, steered, origin = nil, nil, nil
		// Everything the call sent is stored and its reply is not yet, which
		// is the moment Recorder promises.
		a.record(ctx, s, runID, sent, resp, count)
		reply, stop := resp.message, resp.stop
		if gen.context.Reported() {
			reply.Metrics = &provider.MessageMetrics{
				RunID:         runID,
				Usage:         gen.usage(),
				Context:       gen.context,
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
			if err := appendReply(ctx, s, runID, reply); err != nil {
				return err
			}
		}

		if len(reply.ToolCalls) == 0 {
			a.endTurn(ctx, s, runID, stop, &gen)
			a.opts.Logger.Info("turn finished",
				"session_id", s.ID, "run_id", runID, "total_tokens", gen.usage().TotalTokens)
			return nil
		}
		for i, call := range reply.ToolCalls {
			if err := ctx.Err(); err != nil {
				err = fmt.Errorf("run turn: %w", err)
				err = errors.Join(err, a.abandonToolCalls(ctx, s, runID, reply.ToolCalls[i:], err))
				return a.fail(ctx, s, runID, err)
			}
			result, err := a.callTool(ctx, s, runID, g, call)
			if err != nil {
				err = errors.Join(err, a.abandonToolCalls(ctx, s, runID, reply.ToolCalls[i:], err))
				return a.fail(ctx, s, runID, err)
			}
			if err := a.appendTerminal(ctx, s, runID, result); err != nil {
				err = errors.Join(err, a.abandonToolCalls(ctx, s, runID, reply.ToolCalls[i:], err))
				return a.fail(ctx, s, runID, err)
			}
		}
	}
}

// endTurn reports a turn that ended, and why, as turn.end.
func (a *Agent) endTurn(ctx context.Context, s *Session, runID, stop string, gen *generation) {
	a.emit(ctx, s, event.TypeTurnEnd, event.TurnEnd{
		RunID:         runID,
		StopReason:    stop,
		Usage:         gen.usage(),
		Context:       gen.context,
		GenerationMS:  gen.elapsed.Milliseconds(),
		ContextWindow: a.opts.ContextWindow,
		Timings:       gen.reported(),
	})
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
		if err := a.append(terminalCtx, s, runID, provider.ToolResultMessage(c.ID, content, true)); err != nil {
			a.opts.Logger.Error("store abandoned tool result",
				"session_id", s.ID, "run_id", runID, "call_id", c.ID, "error", err)
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

// callTool runs one tool call and reports it as tool.call and tool.result
// events. A tool that fails for a reason the model can act on returns a result
// with IsError set; an error here is the harness's own failure. A call the run
// has now made three or more times in a row, with the same arguments, has a
// warning added to its result: the model reads it, the call is not stopped and
// nor is the run.
func (a *Agent) callTool(ctx context.Context, s *Session, runID string, g *guard, c provider.ToolCall) (provider.Message, error) {
	a.emit(ctx, s, event.TypeToolCall, event.ToolCall{
		RunID:     runID,
		CallID:    c.ID,
		Name:      c.Name,
		Arguments: c.Arguments,
	})

	start := time.Now()
	result, ran, err := a.dispatch(ctx, s, runID, c)
	if err != nil {
		return provider.Message{}, fmt.Errorf("run tool %s: %w", c.Name, err)
	}
	if err := a.ingest(ctx, s, runID, c, ran, &result); err != nil {
		return provider.Message{}, err
	}
	a.notePrivate(ctx, s, c, ran, result)
	if n := g.repeated(c); n >= repeatWarnAt {
		result.Content += repeatWarning(c.Name, n)
	}
	a.emit(ctx, s, event.TypeToolResult, event.ToolResult{
		RunID:      runID,
		CallID:     c.ID,
		Name:       c.Name,
		Content:    result.Content,
		IsError:    result.IsError,
		Details:    result.Details,
		DurationMS: time.Since(start).Milliseconds(),
		Removed:    result.Removed,
	})
	msg := provider.ToolResultMessage(c.ID, result.Content, result.IsError)
	msg.Removed = result.Removed
	return msg, nil
}

// ingest passes the result of a tool whose output is untrusted through the
// gate, in place. It runs before the result is emitted or stored, so the
// session, a replay and a compaction only ever hold the ingested text. A
// web_search result is ingested one part per search result, in one call to the
// gate. An error result is ingested too, as one segment: web_fetch errors
// list the page's headings or return filtered page text. A result that never ran
// the tool (a block, an unknown tool) is the harness's own and is left alone. A gate that cannot ingest withholds the content.
func (a *Agent) ingest(ctx context.Context, s *Session, runID string, c provider.ToolCall, ran bool, r *tool.Result) error {
	if a.opts.Gate == nil || !ran || (c.Name != "web_fetch" && c.Name != "web_search") {
		return nil
	}
	src := sentry.Source{Session: s.ID, Run: runID, Key: s.ID + ":" + c.ID}
	var segs []sentry.Segment
	var at []int // the parts that are untrusted, as indexes into r.Parts
	switch {
	case c.Name == "web_fetch":
		src.Kind, src.Name = sentry.SourceFetch, r.SourceURL
		segs = []sentry.Segment{{Source: src, Text: r.Content}}
	case r.IsError:
		// An error carries text the backend echoed, so it is one segment.
		src.Kind = sentry.SourceSearch
		segs = []sentry.Segment{{Source: src, Text: r.Content}}
	default:
		src.Kind = sentry.SourceSearch
		for i, p := range r.Parts {
			if p.Source != "" {
				seg := src
				seg.Name = p.Source
				segs = append(segs, sentry.Segment{Source: seg, Text: p.Text})
				at = append(at, i)
			}
		}
		if len(segs) == 0 {
			return nil
		}
	}
	got, err := a.opts.Gate.Ingest(ctx, segs)
	if err != nil {
		a.opts.Logger.Error("ingest failed",
			"session_id", s.ID, "run_id", runID, "call_id", c.ID, "tool", c.Name, "error", err)
		if errors.Is(err, sentry.ErrScreenRequired) {
			*r = tool.Errorf("Blocked: screening is required but the guard is unavailable. Try again when the guard is back.")
		} else {
			*r = tool.Errorf("Blocked: this is not available right now")
		}
		return nil
	}
	if c.Name == "web_fetch" || r.IsError {
		r.Content = got[0].Text
		r.Removed = got[0].Removed
		return nil
	}
	texts := make([]string, len(r.Parts))
	for i, p := range r.Parts {
		texts[i] = p.Text
	}
	for j, i := range at {
		texts[i] = got[j].Text
		r.Removed += got[j].Removed
	}
	r.Content = strings.Join(texts, "\n\n")
	return nil
}

// notePrivate tells the gate what private text a call read: a file's content
// or a memory's body. The gate keeps fingerprints of it, never the text.
func (a *Agent) notePrivate(ctx context.Context, s *Session, c provider.ToolCall, ran bool, r tool.Result) {
	if a.opts.Gate == nil || !ran || r.IsError {
		return
	}
	text := r.Content
	switch c.Name {
	case "file_read":
	case "memory_read":
		var m struct {
			Body string `json:"body"`
		}
		if json.Unmarshal([]byte(r.Content), &m) != nil {
			return
		}
		text = m.Body
	default:
		return
	}
	a.opts.Gate.Observe(ctx, sentry.Observation{Kind: sentry.ObservePrivateRead, Session: s.ID, Text: text})
}

// dispatch finds the tool and runs it; ran is false when it did not. An
// unknown tool is the model's mistake, not the harness's, so it comes back as
// a failed result.
func (a *Agent) dispatch(ctx context.Context, s *Session, runID string, c provider.ToolCall) (tool.Result, bool, error) {
	tools := a.registry()
	if tools == nil {
		return tool.Errorf("no tools are available in this session"), false, nil
	}
	t, ok := tools.Get(c.Name)
	if !ok {
		return tool.Errorf("unknown tool %q", c.Name), false, nil
	}
	if blocked, ok := a.gate(ctx, s, runID, c, t); !ok {
		return blocked, false, nil
	}
	if a.opts.Executor == nil && tool.NeedsComputer(t) {
		return tool.Errorf("this session has no computer, so %s cannot run", c.Name), false, nil
	}
	res, err := t.Call(ctx, tool.CallContext{
		Exec:       a.opts.Executor,
		Emit:       a.opts.Emitter,
		ComputerID: s.ComputerID,
		SessionID:  s.ID,
		RunID:      runID,
		CallID:     c.ID,
	}, json.RawMessage(c.Arguments))
	return res, true, err
}

// gateTimeout bounds how long a call waits for the gate.
const gateTimeout = 5 * time.Second

// gate asks the gate whether the call may run. It returns false with the
// result to use when it may not. A gate that errors or times out blocks a
// side-effecting tool with the generic result and lets a read-only one run.
func (a *Agent) gate(ctx context.Context, s *Session, runID string, c provider.ToolCall, t tool.Tool) (tool.Result, bool) {
	if a.opts.Gate == nil {
		return tool.Result{}, true
	}
	gctx, cancel := context.WithTimeout(ctx, gateTimeout)
	defer cancel()
	v, err := a.opts.Gate.Decide(gctx, sentry.Action{
		Key:       s.ID + ":" + c.ID,
		Principal: sentry.Principal{Session: s.ID, Run: runID, Computer: s.ComputerID},
		Kind:      sentry.KindTool,
		Name:      c.Name,
		Args:      json.RawMessage(c.Arguments),
	})
	if err != nil {
		a.opts.Logger.Error("gate could not answer",
			"session_id", s.ID, "run_id", runID, "call_id", c.ID, "tool", c.Name, "error", err)
		if tool.SideEffecting(t) {
			return tool.Errorf("Blocked: this is not available right now"), false
		}
		return tool.Result{}, true
	}
	switch v.Decision {
	case sentry.Allow:
		return tool.Result{}, true
	default: // deny, and ask until phase 11 parks it
		reason := v.Reason
		if reason == "" {
			reason = "this is not available right now"
		}
		return tool.Errorf("Blocked: %s", reason), false
	}
}

// appendTerminal records a terminal tool result on a context that survives a
// run cancellation. A stored assistant tool call must always have one result.
func (a *Agent) appendTerminal(ctx context.Context, s *Session, runID string, m provider.Message) error {
	terminalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
	defer cancel()
	return a.append(terminalCtx, s, runID, m)
}

// drainSteering atomically takes the steering accepted before this check.
func (a *Agent) drainSteering() []provider.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.steering.drain()
}

// nextQueued selects the next user turn. Steering accepted while the final
// model response streamed becomes a new turn. If both queues are empty, this
// closes acceptance atomically with the final check.
func (a *Agent) nextQueued() ([]provider.Message, *queue) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if msgs := a.steering.drain(); len(msgs) != 0 {
		return msgs, &a.steering
	}
	// Follow-ups start one turn each, oldest first.
	if msgs := a.followUps.shift(); len(msgs) != 0 {
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
func (a *Agent) append(ctx context.Context, s *Session, runID string, m provider.Message) error {
	if err := a.store(ctx, s, runID, m); err != nil {
		return err
	}
	s.Conversation.Append(m)
	return nil
}

// store durably records one conversation message.
func (a *Agent) store(ctx context.Context, s *Session, runID string, m provider.Message) error {
	if a.opts.Store == nil {
		return nil
	}
	if err := a.opts.Store.Append(ctx, s.ID, runID, m); err != nil {
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

// mintCall gives a call the backend named its own id. Local servers reuse ids
// (call_0 in every response, none at all, a counter per response), so the
// backend's id keys nothing: it is kept as the call's WireID, for the request
// that sends the call back, and the id every entry, event and approval uses is
// minted here. A call that already has a WireID was minted before and is left
// alone.
func mintCall(c provider.ToolCall) provider.ToolCall {
	if c.WireID != nil {
		return c
	}
	wire := c.ID
	c.WireID = &wire
	c.ID = newCallID()
	return c
}

// newCallID returns a unique id for one tool call. It looks like the ids
// OpenAI mints, since it is what a request sends for a call whose backend gave
// none.
func newCallID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("call_%d", time.Now().UnixNano())
	}
	return "call_" + hex.EncodeToString(b[:])
}

// newRunID returns an identifier for one turn.
func newRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(b[:])
}
