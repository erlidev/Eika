package live

import (
	"context"
	"fmt"

	"github.com/erlidev/eika/internal/provider"
)

// cacheReuseMaxOutput bounds each reply of the cache_reuse scenario.
const cacheReuseMaxOutput = 64

// cacheReuseMinHitRate is the share of the second turn's prompt the server
// must have kept from the first. The two prompts share the system prompt and
// the first exchange, so nearly all of the second is a prefix of the first
// plus its reply; half is a floor a working cache clears with room to spare.
const cacheReuseMinHitRate = 0.5

// cacheReuseSystem is the fixed part of the scenario's system prompt: long
// enough that prefilling it is measurable and reusing it shows.
const cacheReuseSystem = `You are Eika, a personal assistant that runs on the user's own machine.
Answer in plain sentences, in at most two sentences, and never use lists or headings.
You work for one person and you keep what they tell you to yourself.
When a question has a short answer, give the short answer first and stop.
When you are not sure, say what you would check, not what you would guess.
You may be asked about caching, queues, scheduling, memory, and files on a computer.
A cache keeps the result of earlier work so that later work can skip it.
A queue holds work until something is free to do it, in a stated order.
A scheduler decides when work runs, and a lease says how long a resource is held.
A prefix is the start of a text that two texts have in common, character for character.
Prefix caching reuses the model's work on a prefix: only the tokens after it are new.
Because the model reads every token of a prompt once, a long prompt costs seconds.
A tool is something you can call to read or change the world; you call one only when needed.
You never invent file names, numbers or quotations that you were not given.
Keep a calm, direct tone. Do not apologise, and do not thank the user for asking.
`

// CacheReuse is the scenario that sends a two-turn conversation and reports
// how much of the second turn's prompt the server took from its cache. nonce
// starts the system prompt, so the first turn is not already cached by an
// earlier run and its prefill time is a real cold figure.
func CacheReuse(nonce string) Scenario {
	return Scenario{
		Name: "cache_reuse",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			maxOutput := cacheReuseMaxOutput
			temperature := 0.0
			effort := provider.EffortNone
			req := provider.Request{
				System:         fmt.Sprintf("Session %s.\n%s", nonce, cacheReuseSystem),
				ThinkingSwitch: provider.SwitchTemplate,
				Sampling: provider.Sampling{
					MaxOutput:       &maxOutput,
					Temperature:     &temperature,
					ReasoningEffort: &effort,
				},
				Messages: []provider.Message{provider.UserMessage("What is a cache hit?")},
			}
			first, reply, err := t.Complete(ctx, "turn 1", req)
			calls := []Call{first}
			if err != nil {
				return calls, err
			}

			req.Messages = append(req.Messages,
				provider.AssistantMessage(reply, nil),
				provider.UserMessage("Give me an example with a file."))
			second, _, err := t.Complete(ctx, "turn 2", req)
			calls = append(calls, second)
			if err != nil {
				return calls, err
			}

			if second.CacheHitRate == nil {
				return calls, fmt.Errorf("turn 2: the server reported no prompt timings, so the cache hit cannot be measured")
			}
			if *second.CacheHitRate < cacheReuseMinHitRate {
				return calls, fmt.Errorf("turn 2 took %.0f%% of its prompt from the cache, want at least %.0f%%: the second request did not reuse the first's prefix",
					*second.CacheHitRate*100, cacheReuseMinHitRate*100)
			}
			return calls, nil
		},
	}
}
