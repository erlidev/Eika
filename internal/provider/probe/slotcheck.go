package probe

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
)

// SlotAffinity is what the slot affinity check found out. Naming a slot only
// helps if the server keeps a prefix in the slot it was run on, and whether it
// does depends on how the server was started, which it does not state, so the
// check measures it. It is stored in the provider's capabilities with the build
// of the server and the model it ran against, as the Responses check is.
type SlotAffinity struct {
	// Verdict is the outcome. Only Passed lets the harness name slots.
	Verdict Verdict `json:"verdict"`
	// Reason says why the check failed or was skipped, and is empty when it
	// passed.
	Reason string `json:"reason,omitempty"`
	// Model is the model the check asked, and Build the server's build when it
	// did. A change of either makes the check stale.
	Model string `json:"model,omitempty"`
	Build string `json:"build,omitempty"`
	// CheckedAt is when the check ran.
	CheckedAt time.Time `json:"checked_at"`
	// Prompt is the prompt tokens of the first request, and Cached how many of
	// them the last request, which named the same slot after a request on
	// another, found already in the server's cache.
	Prompt int `json:"prompt_tokens,omitempty"`
	Cached int `json:"cached_tokens,omitempty"`
	// Busy is set on a Skipped result that is skipped because the check could
	// not get a lease on the inference pool in time. It is not stored.
	Busy bool `json:"-"`
}

// Current reports whether the check was made against the same build and the
// same model, so that running it again would learn nothing.
func (c SlotAffinity) Current(build, model string) bool {
	return current(c.Verdict, c.Build, c.Model, build, model)
}

// The bounds and the bar of the check. Its requests are real model calls with a
// prompt of about slotCheckTokens tokens, so each has a small output limit.
const (
	// slotCheckMaxOutput bounds the reply of each request: the check reads the
	// cache, not the answer.
	slotCheckMaxOutput = 16
	// slotCheckPromptTokens bounds the prompt of each request, for the room the
	// check asks the queue for.
	slotCheckPromptTokens = 1024
	// slotCheckSentences is how many times the first request's sentence is
	// repeated: about 750 tokens, enough that a prefix found in the cache is
	// not mistaken for the few tokens any two prompts share.
	slotCheckSentences = 30
	// slotCheckMinReuse is the share of the first prompt the last request must
	// find cached. The rest is the tail the template renders again once the
	// first reply is part of the history.
	slotCheckMinReuse = 0.8
)

// SlotCheck is what CheckSlotAffinity needs.
type SlotCheck struct {
	// Provider reaches the endpoint under test and sends the slot a request
	// names, whatever the harness would decide; the check is how it decides.
	Provider provider.Provider
	// Model is the model to ask, and Build the server's build, both recorded
	// with the result.
	Model, Build string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Queue is the inference queue each request takes a lease from, and Need
	// says whose requests they are, as for ResponsesCheck. Nil takes no lease.
	Queue *inference.Queue
	Need  inference.Need
}

// CheckSlotAffinity asks the endpoint three small requests and reports whether
// a request that names a slot finds its prefix there after a request ran on
// the other (docs/design/runtime.md section 3). The first names slot 0 and
// reads a prompt of about 750 tokens; the second names slot 1 and reads
// something else; the third names slot 0 and continues the first's
// conversation, and must find nearly all of the first prompt cached. That is
// what an alternating pair of sessions needs from their slots, and a server
// that clears an idle slot when another starts a task, as llama.cpp does with a
// unified KV cache and --cache-idle-slots, fails it: the request that names a
// slot gets it empty, and the host cache that would have restored its prefix is
// not consulted for a named slot. It sends real model requests, bounded in
// tokens and in time, and never fails: a problem is the verdict.
func CheckSlotAffinity(ctx context.Context, c SlotCheck) SlotAffinity {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	out := SlotAffinity{Model: c.Model, Build: c.Build, CheckedAt: now().UTC()}
	fail := func(format string, args ...any) SlotAffinity {
		out.Verdict, out.Reason = Failed, fmt.Sprintf(format, args...)
		return out
	}

	temperature := 0.0
	// The nonce starts the prompt, so that no earlier run's cache holds it.
	system := "Slot check " + strconv.FormatInt(out.CheckedAt.UnixNano(), 36) + ".\n" +
		strings.Repeat("The quick brown fox jumps over the lazy dog while the river runs past the old stone mill. ", slotCheckSentences)
	request := func(slot int, system string, messages ...provider.Message) provider.Request {
		maxOutput := slotCheckMaxOutput
		return provider.Request{
			Model: c.Model, System: system, Messages: messages,
			Sampling: provider.Sampling{MaxOutput: &maxOutput, Temperature: &temperature},
			SlotHint: &slot,
		}
	}
	busy := func(err error) (SlotAffinity, bool) {
		reason, ok := busyReason(err)
		return SlotAffinity{Verdict: Skipped, Reason: reason, CheckedAt: out.CheckedAt, Busy: true}, ok
	}
	model := asker{provider: c.Provider, queue: c.Queue, need: c.Need, promptTokens: slotCheckPromptTokens}

	ask := provider.UserMessage("Reply with the word ok.")
	first, err := model.ask(ctx, request(0, system, ask))
	if skipped, ok := busy(err); ok {
		return skipped
	}
	prompt, stated := first.usage.InputTokens.Value()
	switch {
	case err != nil:
		return fail("the first request failed: %v", err)
	case !stated:
		return fail("the first response stated no token usage")
	}
	out.Prompt = prompt

	if _, err := model.ask(ctx, request(1, "A different prompt.", provider.UserMessage("Reply with the word no."))); err != nil {
		if skipped, ok := busy(err); ok {
			return skipped
		}
		return fail("the request on the other slot failed: %v", err)
	}

	last, err := model.ask(ctx, request(0, system, ask, provider.AssistantMessage("ok", nil), provider.UserMessage("Reply with the word yes.")))
	if skipped, ok := busy(err); ok {
		return skipped
	}
	cached, stated := last.usage.CachedTokens.Value()
	switch {
	case err != nil:
		return fail("the last request failed: %v", err)
	case !stated:
		return fail("the last response did not say how many prompt tokens were cached, so a prefix kept in a slot cannot be seen")
	}
	out.Cached = cached
	if float64(cached) < slotCheckMinReuse*float64(prompt) {
		return fail("a request naming slot 0 found %d of the %d prompt tokens of its conversation cached after a request on slot 1: "+
			"the server does not keep a prefix in a named slot, which llama.cpp with a unified KV cache does when it saves and clears idle slots "+
			"(--cache-idle-slots, on by default; --no-cache-idle-slots keeps them)", cached, prompt)
	}
	out.Verdict = Passed
	return out
}
