package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
)

// Compaction follows Pi's: when a conversation outgrows the model's context
// window, the oldest part is summarized and the summary takes its place,
// while roughly the newest KeepRecentTokens stay verbatim. What differs is
// the request that makes the summary; summary.go says how. The home session
// cuts at a user-turn boundary instead, keeping a share of the window
// (KeepRecentShare, runtime.md 11).

// The compaction budgets until the user sets them, which are Pi's.
const (
	// DefaultReserveTokens is how much of the window compaction keeps free.
	DefaultReserveTokens = 16384
	// DefaultKeepRecentTokens is roughly how much of the newest
	// conversation a compaction keeps verbatim.
	DefaultKeepRecentTokens = 20000
	// DefaultKeepRecentShare is the share of the window the home session's
	// cut keeps at most (runtime.md 11).
	DefaultKeepRecentShare = 0.15
)

// ErrNothingToCompact reports a conversation with nothing old enough to
// summarize: all of it fits in what a compaction would keep.
var ErrNothingToCompact = errors.New("nothing to compact: the conversation fits in what a compaction keeps")

// CompactionSettings configure how an agent compacts a conversation that
// outgrows the model's context window. The zero value compacts only when
// Agent.Compact asks, with the default budgets and prompts.
type CompactionSettings struct {
	// Auto compacts before a model call whose request would leave less than
	// ReserveTokens of the window free, and once after an endpoint refuses
	// a request as larger than the window, which is then sent again.
	Auto bool
	// ReserveTokens is how much of the window stays free for the response.
	// Zero means DefaultReserveTokens.
	ReserveTokens int
	// KeepRecentTokens is roughly how much of the newest conversation stays
	// verbatim. Zero means DefaultKeepRecentTokens.
	KeepRecentTokens int
	// KeepRecentShare, when above zero, cuts at a user-turn boundary
	// instead, as the home session does (runtime.md 11): the oldest user
	// message from which the newest stored messages fit in this share of
	// the window, keeping at least the newest whole turn. A turn larger
	// than the share is split as Pi splits one. A window of unknown size
	// keeps KeepRecentTokens instead.
	KeepRecentShare float64
	// Prompts replace the built-in prompts where they are set.
	Prompts CompactionPrompts
	// FallbackEffort is the reasoning effort a summary is asked for again
	// with when thinking used up the first attempt's output budget: the
	// effort that turns thinking off, or the lowest the model offers.
	// Empty means no second attempt.
	FallbackEffort string
	// Board, when set, makes the summary the home session's (runtime.md
	// 11): the [Subagents] board it returns, which code builds from rows when
	// the summary is assembled, then the [Conversation] section the model
	// writes and each later compaction updates. Times on the board are in
	// loc, and asOf is the moment the rows were read, which the compaction
	// records. An error fails the compaction, which leaves the session as it
	// was.
	Board func(ctx context.Context, loc *time.Location) (board string, asOf time.Time, err error)
}

// CompactionPrompts are the instructions that ask for a summary. An empty
// field is the built-in prompt of the same name.
type CompactionPrompts struct {
	// Summary asks for the first summary of a conversation: SummaryPrompt.
	Summary string
	// Update asks for a summary that folds newer messages into the one
	// already there: UpdatePrompt.
	Update string
	// TurnPrefix asks for the summary of the start of a turn too large to
	// keep whole: TurnPrefixPrompt.
	TurnPrefix string
}

// Compaction is one compaction as the session records it.
type Compaction struct {
	// Summary is the summary that takes the place of what was compacted.
	Summary string `json:"summary"`
	// Kept is how many messages at the end of the context before the
	// compaction stay after the summary.
	Kept int `json:"kept"`
	// TokensBefore and TokensAfter are the context's predicted size before
	// and after.
	TokensBefore int `json:"tokens_before"`
	TokensAfter  int `json:"tokens_after"`
	// Reason is manual, threshold, or overflow.
	Reason string `json:"reason"`
	// Usage is what making the summary cost.
	Usage provider.Usage `json:"usage"`
	// BoardAsOf is when the home session's board was read from the rows,
	// absent for a summary without one. The next board lists the activity
	// after it, so a change that came between the read and this record being
	// stored is not taken for old.
	BoardAsOf *time.Time `json:"board_as_of,omitempty"`
}

// Compacted returns the context a compaction leaves: its summary, then the
// last c.Kept of before, without the measurements they carried, which
// describe calls made with a larger context.
func Compacted(before []provider.Message, c Compaction) []provider.Message {
	kept := min(max(c.Kept, 0), len(before))
	out := make([]provider.Message, 0, kept+1)
	out = append(out, SummaryMessage(c.Summary))
	for _, m := range before[len(before)-kept:] {
		m.Metrics = nil
		out = append(out, m)
	}
	return out
}

// The text around a summary in the message that carries it, which are Pi's.
const (
	summaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	summarySuffix = "\n</summary>"
)

// SummaryMessage returns the user message that carries a compaction's
// summary at the start of the context.
func SummaryMessage(summary string) provider.Message {
	m := provider.UserMessage(summaryPrefix + summary + summarySuffix)
	m.Summary = true
	return m
}

// reserveTokens is the part of a window of window tokens compaction keeps
// free. A window too small for the setting gets a quarter of itself: after a
// compaction the context holds the summary and the kept messages, and if
// those alone reached the threshold every call would compact again.
func (c CompactionSettings) reserveTokens(window int) int {
	return scaled(c.ReserveTokens, DefaultReserveTokens, window)
}

// keepRecentTokens is how much of the newest conversation stays, bounded
// as reserveTokens is.
func (c CompactionSettings) keepRecentTokens(window int) int {
	return scaled(c.KeepRecentTokens, DefaultKeepRecentTokens, window)
}

// keepBudget is how many tokens of the newest conversation a compaction
// keeps, in a window of window tokens.
func (c CompactionSettings) keepBudget(window int) int {
	if c.KeepRecentShare > 0 && window > 0 {
		return int(c.KeepRecentShare * float64(window))
	}
	return c.keepRecentTokens(window)
}

// plan finds where a compaction cuts msgs: at a user-turn boundary when
// the settings keep a share of the window, else where Pi cuts.
func (c CompactionSettings) plan(msgs []provider.Message, sizes []int, stored, window int) (compactionPlan, bool) {
	keep := c.keepBudget(window)
	if c.KeepRecentShare > 0 && window > 0 {
		return planTurnCut(msgs, sizes, stored, keep)
	}
	return planCompaction(msgs, sizes, stored, keep)
}

// scaled returns setting, or def when it is not set, at most a quarter of a
// window of window tokens.
func scaled(setting, def, window int) int {
	if setting <= 0 {
		setting = def
	}
	if window > 0 {
		setting = min(setting, window/4)
	}
	return setting
}

// compactionPlan is where a compaction cuts a conversation.
type compactionPlan struct {
	// start is the first message the summary covers: 1 when the context
	// already begins with a summary, which the new one updates.
	start int
	// first is the first message kept.
	first int
	// turnStart is the user message that began the turn first falls in when
	// the cut splits that turn, and -1 otherwise.
	turnStart int
	// stored is how many messages at the start of the context are stored.
	// The rest were not delivered yet and are always kept.
	stored int
}

// previous reports whether the context already begins with a summary.
func (p compactionPlan) previous() bool { return p.start == 1 }

// split reports whether the cut falls inside a turn.
func (p compactionPlan) split() bool { return p.turnStart >= 0 }

// historyEnd is the end of the complete turns the summary covers.
func (p compactionPlan) historyEnd() int {
	if p.split() {
		return p.turnStart
	}
	return p.first
}

// planCompaction finds where to cut msgs, whose estimated sizes are sizes,
// so that about keep tokens of the newest stored messages stay. It is Pi's
// findCutPoint on context messages: the cut is at a user or an assistant
// message, never at a tool result, which must follow its call. It reports
// false when the cut leaves nothing to summarize.
func planCompaction(msgs []provider.Message, sizes []int, stored, keep int) (compactionPlan, bool) {
	p := compactionPlan{turnStart: -1, stored: stored}
	if len(msgs) > 0 && msgs[0].Summary {
		p.start = 1
	}
	var cuts []int
	for i := p.start; i < stored; i++ {
		if msgs[i].Role != provider.RoleTool {
			cuts = append(cuts, i)
		}
	}
	if len(cuts) == 0 {
		return p, false
	}
	// Walking back from the newest message, the cut is at the first place
	// that keeps at least keep tokens. A conversation smaller than that
	// keeps everything. When the messages that reach keep are all tool
	// results, the cut is at the newest place there is, which keeps more
	// than keep; Pi keeps everything then, and so compacts nothing.
	p.first = cuts[0]
	total := 0
	for i := stored - 1; i >= p.start; i-- {
		total += sizes[i]
		if total < keep {
			continue
		}
		p.first = cuts[len(cuts)-1]
		for _, c := range cuts {
			if c >= i {
				p.first = c
				break
			}
		}
		break
	}
	if msgs[p.first].Role != provider.RoleUser {
		for i := p.first; i >= p.start; i-- {
			if msgs[i].Role == provider.RoleUser {
				p.turnStart = i
				break
			}
		}
	}
	// A split turn always leaves its start to summarize; otherwise there
	// must be complete turns before the cut.
	return p, p.split() || p.first > p.start
}

// planTurnCut cuts msgs at a user-turn boundary (runtime.md 11): at the
// oldest user message from which the stored messages fit in keep tokens,
// and never after the newest one, so at least one whole turn stays. Every
// user message is a boundary, since a tool result never follows one: a
// steering message waits for the calls of its turn to be answered. When
// the newest turn alone is larger than keep, or no user message is left
// to cut at, the cut is Pi's, which splits the turn.
func planTurnCut(msgs []provider.Message, sizes []int, stored, keep int) (compactionPlan, bool) {
	p := compactionPlan{turnStart: -1, stored: stored, first: -1}
	if len(msgs) > 0 && msgs[0].Summary {
		p.start = 1
	}
	total := 0
	for i := stored - 1; i >= p.start; i-- {
		total += sizes[i]
		if msgs[i].Role != provider.RoleUser {
			continue
		}
		if total > keep {
			break
		}
		p.first = i
	}
	if p.first < 0 {
		return planCompaction(msgs, sizes, stored, keep)
	}
	return p, p.first > p.start
}

// Compactable reports whether Compact would find anything to summarize in
// s's conversation.
func (a *Agent) Compactable(s *Session) bool {
	msgs := s.Conversation.Messages()
	c := a.base(nil).WithMessages(msgs)
	_, ok := a.opts.Compaction.plan(msgs, c.MessageSizes, len(msgs), a.opts.ContextWindow)
	return ok
}

// Compact summarizes the oldest part of s's conversation now, whatever its
// size, as Pi's /compact does. instructions, when set, say what the summary
// should focus on. A conversation with nothing old enough to summarize is
// ErrNothingToCompact.
func (a *Agent) Compact(ctx context.Context, s *Session, instructions string) error {
	if s == nil || s.Conversation == nil {
		return errors.New("compact: session has no conversation")
	}
	runID := newRunID()
	// compact reports its own failure as compaction.end, so there is no
	// run.error to add.
	_, err := a.compact(ctx, s, runID, a.prompt(), s.Conversation.Len(), event.CompactManual, instructions)
	return err
}

// compact summarizes the oldest part of s's conversation and replaces it with
// the summary, in the conversation and in the store. The first stored
// messages of the conversation are durably stored; the rest are waiting for
// a model call to deliver them and are always kept. It returns how many
// messages at the start of the new conversation are stored.
func (a *Agent) compact(ctx context.Context, s *Session, runID string, prompt []Section, stored int, reason, instructions string) (int, error) {
	msgs := s.Conversation.Messages()
	base := a.sessionBase(s, prompt)
	sent := base.WithMessages(msgs)
	plan, ok := a.opts.Compaction.plan(msgs, sent.MessageSizes, stored, a.opts.ContextWindow)
	if !ok {
		return stored, ErrNothingToCompact
	}
	record := Compaction{TokensBefore: a.predict(ctx, sent).Tokens, Reason: reason}
	a.emit(ctx, s, event.TypeCompactionStart, event.CompactionStart{
		RunID:        runID,
		Reason:       reason,
		TokensBefore: record.TokensBefore,
	})
	end := event.CompactionEnd{RunID: runID, Reason: reason, TokensBefore: record.TokensBefore}
	failed := func(err error) (int, error) {
		err = fmt.Errorf("compact conversation: %w", err)
		end.Error = err.Error()
		a.opts.Logger.Warn("compaction failed", "session_id", s.ID, "run_id", runID, "reason", reason, "error", err)
		a.emit(context.WithoutCancel(ctx), s, event.TypeCompactionEnd, end)
		return stored, err
	}

	summary, usage, err := a.summarize(ctx, s, base, msgs, plan, instructions)
	record.Usage = usage
	end.Usage = usage
	if err != nil {
		return failed(err)
	}
	if board := a.opts.Compaction.Board; board != nil {
		loc := a.currentLocation()
		text, asOf, err := board(ctx, loc)
		if err != nil {
			return failed(fmt.Errorf("build subagent board: %w", err))
		}
		summary = homeSummary(asOf, loc, text, summary)
		asOf = asOf.UTC()
		record.BoardAsOf = &asOf
	}
	record.Summary = summary
	record.Kept = stored - plan.first
	next := append(Compacted(msgs[:stored], record), msgs[stored:]...)
	record.TokensAfter = a.predict(ctx, base.WithMessages(next)).Tokens
	if a.opts.RefreshSnapshot != nil {
		snap, err := a.opts.RefreshSnapshot(ctx, s.ID, record)
		if err != nil {
			return failed(fmt.Errorf("store compaction: %w", err))
		}
		a.useSnapshot(s, snap)
	} else if a.opts.Store != nil {
		if err := a.opts.Store.AppendCompaction(ctx, s.ID, record); err != nil {
			return failed(fmt.Errorf("store compaction: %w", err))
		}
	}
	s.Conversation.Replace(next)

	end.TokensAfter, end.Summary, end.Kept = record.TokensAfter, record.Summary, record.Kept
	a.emit(ctx, s, event.TypeCompactionEnd, end)
	a.opts.Logger.Info("conversation compacted", "session_id", s.ID, "run_id", runID, "reason", reason,
		"tokens_before", record.TokensBefore, "tokens_after", record.TokensAfter, "kept", record.Kept)
	return record.Kept + 1, nil
}

// shouldCompact reports whether a request of the predicted size leaves less of
// the window free than the reserve, so compaction runs first. A prediction that
// was only estimated may be off, so the reserve grows by the error estimates
// have shown; an exact or anchored one is trusted to its number.
func (a *Agent) shouldCompact(pred provider.Prediction) bool {
	window := a.opts.ContextWindow
	c := a.opts.Compaction
	if !c.Auto || window <= 0 {
		return false
	}
	reserve := c.reserveTokens(window)
	if pred.Method == provider.MethodEstimated {
		reserve = widenedReserve(reserve, window, pred.Tokens, a.counter.EstimateError())
	}
	return pred.Tokens > window-reserve
}

// lastMeasured returns the context the newest measured model call filled and
// the index of the message it produced, or -1 when no message carries a
// measurement. A compaction clears the measurements of what it keeps, so one
// found is always of the context as it is now.
func lastMeasured(msgs []provider.Message) (int, int) {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i].Metrics
		if msgs[i].Role != provider.RoleAssistant || m == nil {
			continue
		}
		// The fill is the total the endpoint stated, or its prompt and
		// response counts together when it stated both. A call that stated
		// less is not a measurement of the fill, and an older one that did is
		// used instead, with everything after it estimated.
		tokens, ok := m.Context.TotalTokens.Value()
		if !ok || tokens == 0 {
			in, haveIn := m.Context.InputTokens.Value()
			out, haveOut := m.Context.OutputTokens.Value()
			tokens, ok = in+out, haveIn && haveOut
		}
		if ok && tokens > 0 {
			return tokens, i
		}
	}
	return 0, -1
}

// fit applies the context window to a request of about tokens tokens: one
// that cannot fit fails before it is sent, and one that fits with less room
// than its max output asks for gets max output lowered to the room there
// is, since an endpoint refuses input and output that exceed its window.
func fit(c Context, tokens, window int) (Context, error) {
	if window <= 0 {
		return c, nil
	}
	if tokens >= window {
		return Context{}, fmt.Errorf("call model: the request needs about %d tokens, which does not fit the configured window of %d", tokens, window)
	}
	if m := c.Parameters.Sampling.MaxOutput; m != nil && tokens+*m > window {
		c.Parameters.Sampling.MaxOutput = new(window - tokens)
	}
	return c, nil
}

// errSilentOverflow is an endpoint that cut a request down to its window
// instead of refusing it, and so had no room left to answer.
var errSilentOverflow = errors.New("call model: the endpoint filled its context window with the request and produced nothing")

// silentOverflow reports whether a response is an endpoint that truncated
// the request to its window and then had no room to answer, as Xiaomi MiMo
// does; Pi's third overflow case.
func silentOverflow(r response, window int) bool {
	// The empty response is the evidence that nothing was produced; the
	// output count only rules out a response that produced something unseen,
	// so a count the endpoint did not state does not.
	out, stated := r.usage.OutputTokens.Value()
	in, haveIn := r.usage.InputTokens.Value()
	return window > 0 && cutOff(r.stop) && (!stated || out == 0) && empty(r.message) &&
		haveIn && float64(in) >= 0.99*float64(window)
}
