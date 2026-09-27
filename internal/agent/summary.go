package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/provider"
)

// A summary is asked for in one of two forms.
//
// The cached form is the run's own request, exactly as the last model call
// sent it up to the cut, with the summary prompt appended as a user message.
// The endpoint has that prefix in its prompt cache, so it reads only the
// prompt. It is the form tried first.
//
// The transcript form is Pi's: the conversation serialized as text, with
// long tool results cut, under a system prompt of its own and no tools. It
// is read uncached, so it is the fallback, for a prefix that no longer fits
// the window and for a model that answers the cached form with tool calls.

// SummaryPrompt asks for the first summary of a conversation. It is Pi's,
// told that it arrives inside the conversation it summarizes.
const SummaryPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work. Do not continue the conversation, answer questions in it, or call tools: reply with the summary alone, and write it directly rather than deliberating at length.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// UpdatePrompt asks for a summary that folds newer messages into the one the
// context begins with. It is Pi's, which reads the earlier summary from the
// same <summary> tags the context carries it in.
const UpdatePrompt = `The conversation above begins with an existing summary of earlier work, in <summary> tags; the messages after it are NEW. Update the existing summary with the new messages. Do not continue the conversation, answer questions in it, or call tools: reply with the updated summary alone, and write it directly rather than deliberating at length.

RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// TurnPrefixPrompt asks for the summary of the start of a turn too large to
// keep whole. It is Pi's.
const TurnPrefixPrompt = `The most recent user request above began a turn that is too large to keep whole. The rest of that turn (the recent work) is retained; the part above, from that request on, is the PREFIX of the turn. Do not continue the conversation or call tools: reply with a summary of the prefix alone, as context for the retained suffix:

## Original Request
[What did the user ask for in this turn?]

## Early Progress
- [Key decisions and work done in the prefix]

## Context for Suffix
- [Information needed to understand the retained recent work]

Be concise. Focus on what's needed to understand the kept suffix.`

// TranscriptSystemPrompt is the system prompt of a summary asked for in the
// transcript form. It is Pi's.
const TranscriptSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`

// Bounds on the summary requests.
const (
	// minSummaryBudget is the fewest output tokens a summary is asked for
	// with, or its whole budget when that is smaller. A form that leaves
	// less room than this is not tried.
	minSummaryBudget = 2048
	// transcriptToolResultRunes is how much of a tool result the
	// transcript form keeps: the results are most of a context, and a
	// summary needs what they were about rather than all they said.
	transcriptToolResultRunes = 2000
)

// splitTurnSeparator joins the summary of the history before a split turn to
// the summary of that turn's start, as Pi does.
const splitTurnSeparator = "\n\n---\n\n**Turn Context (split turn):**\n\n"

// prompts returns the prompts, the built-in ones where none is set.
func (c CompactionSettings) prompts() CompactionPrompts {
	p := c.Prompts
	if strings.TrimSpace(p.Summary) == "" {
		p.Summary = SummaryPrompt
	}
	if strings.TrimSpace(p.Update) == "" {
		p.Update = UpdatePrompt
	}
	if strings.TrimSpace(p.TurnPrefix) == "" {
		p.TurnPrefix = TurnPrefixPrompt
	}
	return p
}

// summarize makes the summary that replaces msgs before plan.first. base is
// the run's request without messages. The complete turns before the cut are
// summarized, updating the summary the context already begins with; a turn
// the cut splits has its start summarized apart and appended.
func (a *Agent) summarize(ctx context.Context, s *Session, base Context, msgs []provider.Message, plan compactionPlan, instructions string) (string, provider.Usage, error) {
	prompts := a.opts.Compaction.prompts()
	reserve := a.opts.Compaction.reserveTokens(a.opts.ContextWindow)
	var usage provider.Usage
	var history string
	switch {
	case plan.historyEnd() > plan.start:
		prompt := prompts.Summary
		if plan.previous() {
			prompt = prompts.Update
		}
		if instructions = strings.TrimSpace(instructions); instructions != "" {
			prompt += "\n\nAdditional focus: " + instructions
		}
		text, u, err := a.summary(ctx, s, base, msgs, summaryRequest{end: plan.historyEnd(), prompt: prompt, budget: reserve * 8 / 10})
		usage = addUsage(usage, u)
		if err != nil {
			return "", usage, err
		}
		history = text
	case plan.previous():
		// Nothing complete was added since the last summary, which stays
		// as it is. Pi drops it here and keeps only the turn's summary.
		history = summaryText(msgs[0])
	}
	if !plan.split() {
		return history, usage, nil
	}
	prefix, u, err := a.summary(ctx, s, base, msgs, summaryRequest{
		end:    plan.first,
		from:   plan.turnStart,
		prompt: prompts.TurnPrefix,
		budget: reserve / 2,
	})
	usage = addUsage(usage, u)
	if err != nil {
		return "", usage, err
	}
	if history == "" {
		history = "No prior history."
	}
	return history + splitTurnSeparator + prefix, usage, nil
}

// summaryRequest is one summary to ask for.
type summaryRequest struct {
	// end is where the messages the summary covers end: the cached form
	// sends msgs[:end] before the prompt.
	end int
	// from is where the transcript form starts: it sends msgs[from:end].
	from int
	// prompt is the instruction that asks for the summary.
	prompt string
	// budget is the most output tokens the summary may take, thinking
	// included.
	budget int
}

// summary asks for one summary: in the cached form first, then with thinking
// off if thinking used up the budget, and in the transcript form if the
// cached form does not fit or is answered with tool calls. A summary that is
// still cut off on the last attempt is used as far as it goes.
func (a *Agent) summary(ctx context.Context, s *Session, base Context, msgs []provider.Message, r summaryRequest) (string, provider.Usage, error) {
	window := a.opts.ContextWindow
	budget := r.budget
	if m := base.Parameters.Sampling.MaxOutput; m != nil {
		budget = min(budget, *m)
	}
	efforts := []*string{base.Parameters.Sampling.ReasoningEffort}
	if f := a.opts.Compaction.FallbackEffort; f != "" && (efforts[0] == nil || *efforts[0] != f) {
		efforts = append(efforts, new(f))
	}
	need := min(budget, minSummaryBudget)
	var usage provider.Usage
	partial := ""
	// ask tries every effort in one form. It reports whether the form
	// cannot work at all, so the next one should be tried.
	ask := func(c Context, budget int) (string, bool, error) {
		for _, effort := range efforts {
			c.Parameters.Sampling.MaxOutput = new(budget)
			c.Parameters.Sampling.ReasoningEffort = effort
			resp, err := a.retrying(ctx, s, "", false, func() (response, error) {
				return a.stream(ctx, s, "", c.Request(), nil)
			})
			usage = addUsage(usage, resp.usage)
			if err != nil {
				return "", provider.ContextOverflow(err), err
			}
			text := strings.TrimSpace(provider.StripReasoning(resp.message.Content))
			switch {
			case text != "" && !cutOff(resp.stop):
				return text, false, nil
			case text == "" && len(resp.message.ToolCalls) > 0:
				return "", true, fmt.Errorf("the model called a tool instead of summarizing")
			case len(text) > len(partial):
				partial = text
			}
		}
		return "", false, nil
	}

	sent := append(slices.Clone(msgs[:r.end]), provider.UserMessage(r.prompt))
	cached := base.WithMessages(sent)
	room := budget
	if window > 0 {
		room = min(budget, window-estimateContext(cached, sent))
	}
	if room >= need {
		text, next, err := ask(cached, room)
		switch {
		case text != "":
			return text, usage, nil
		case err != nil && !next:
			return "", usage, err
		case !next:
			return a.partialSummary(partial, budget, usage)
		}
	}

	transcript := transcriptContext(base, msgs[r.from:r.end], r.prompt)
	room = budget
	if window > 0 {
		room = min(budget, window-estimateContext(transcript, transcript.Messages))
	}
	if room < need {
		return "", usage, fmt.Errorf("the conversation to summarize does not fit the configured window of %d with room for a summary", window)
	}
	text, _, err := ask(transcript, room)
	switch {
	case text != "":
		return text, usage, nil
	case err != nil:
		return "", usage, err
	}
	return a.partialSummary(partial, budget, usage)
}

// partialSummary returns the longest summary an attempt produced before it
// was cut off, which beats failing the run for want of a complete one, or
// an error when there is none.
func (a *Agent) partialSummary(partial string, budget int, usage provider.Usage) (string, provider.Usage, error) {
	if partial == "" {
		return "", usage, fmt.Errorf("the model produced no summary within %d output tokens", budget)
	}
	a.opts.Logger.Warn("compaction summary was cut off; using it as far as it goes", "output_budget", budget)
	return partial, usage, nil
}

// transcriptContext returns the transcript form of a summary request: msgs
// serialized as text inside the one user message, followed by prompt, under
// TranscriptSystemPrompt, with the run's parameters and no tools.
func transcriptContext(base Context, msgs []provider.Message, prompt string) Context {
	c := Context{
		Sections:   []Section{newSection(SectionBase, TranscriptSystemPrompt)},
		Tools:      []ToolSchema{},
		Parameters: base.Parameters,
	}
	text := "<conversation>\n" + transcript(msgs) + "\n</conversation>\n\n" + prompt
	return c.WithMessages([]provider.Message{provider.UserMessage(text)})
}

// transcript serializes messages as text for the transcript form, as Pi's
// serializeConversation does, so that the model reads a record to summarize
// rather than a conversation to continue.
func transcript(msgs []provider.Message) string {
	var parts []string
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleUser:
			if m.Content != "" {
				parts = append(parts, "[User]: "+m.Content)
			}
		case provider.RoleAssistant:
			if m.Reasoning != "" {
				parts = append(parts, "[Assistant thinking]: "+m.Reasoning)
			}
			if m.Content != "" {
				parts = append(parts, "[Assistant]: "+m.Content)
			}
			if len(m.ToolCalls) > 0 {
				calls := make([]string, 0, len(m.ToolCalls))
				for _, c := range m.ToolCalls {
					calls = append(calls, callText(c))
				}
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(calls, "; "))
			}
		case provider.RoleTool:
			if m.Content != "" {
				parts = append(parts, "[Tool result]: "+truncateRunes(m.Content, transcriptToolResultRunes))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// callText renders a tool call as name(key=value, ...), each value as
// compact JSON in the order the model wrote the keys. Arguments that are not
// a JSON object are written as they are.
func callText(c provider.ToolCall) string {
	raw, _ := c.Arguments.JSON()
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return c.Name + "(" + string(raw) + ")"
	}
	var args []string
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return c.Name + "(" + string(raw) + ")"
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return c.Name + "(" + string(raw) + ")"
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, value); err != nil {
			compact.Write(value)
		}
		args = append(args, fmt.Sprintf("%v=%s", key, compact.String()))
	}
	return c.Name + "(" + strings.Join(args, ", ") + ")"
}

// truncateRunes cuts text to n characters and says how many it cut.
func truncateRunes(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	runes := []rune(text)
	return fmt.Sprintf("%s\n\n[... %d more characters truncated]", string(runes[:n]), len(runes)-n)
}

// summaryText returns the summary a summary message carries.
func summaryText(m provider.Message) string {
	return strings.TrimSuffix(strings.TrimPrefix(m.Content, summaryPrefix), summarySuffix)
}

// addUsage returns the sum of two usages.
func addUsage(a, b provider.Usage) provider.Usage {
	return provider.Usage{
		InputTokens:  a.InputTokens + b.InputTokens,
		OutputTokens: a.OutputTokens + b.OutputTokens,
		TotalTokens:  a.TotalTokens + b.TotalTokens,
	}
}
