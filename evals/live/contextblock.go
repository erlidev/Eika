package live

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// prefixHold is the share of the previous call's prompt the next call must
// find in the server's cache. The prompt of a call ends with the template's
// opening of the reply, which the next call's history reads differently, so
// the whole of it is not promised.
const prefixHold = 0.9

// ContextBlockPrefix is the scenario that proves the time written into each
// user message leaves the prefix alone on the real server. The loop's clock
// moves seven minutes every time it is read, so every message states a time of
// its own and a block computed at send time would change the bytes of every
// earlier message on every call. Three turns on one session: every call must
// be predicted exactly with no drift (the blocks are counted), and every call
// after the first must find the previous call's prompt in the server's cache.
func ContextBlockPrefix(nonce string) Scenario {
	return Scenario{
		Name: "context_block_prefix",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			var mu sync.Mutex
			now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
			clock := func() time.Time {
				mu.Lock()
				defer mu.Unlock()
				now = now.Add(7 * time.Minute)
				return now
			}
			sampling := quiet()
			calls, recorded, err := playLoop(ctx, t, loopOptions{
				system:   fmt.Sprintf("Session %s.\nYou are Eika. Answer in one short sentence.", nonce),
				sampling: sampling,
				asks:     []string{"What is a cache?", "Why does a prefix matter?", "And what is a lease?"},
				clock:    clock,
			})
			if err != nil {
				return calls, err
			}
			if len(calls) != 3 {
				return calls, fmt.Errorf("the loop recorded %d calls, want one for each of the three turns", len(calls))
			}
			for i, c := range calls {
				if err := requireExact(c); err != nil {
					return calls, err
				}
				if c.Drift == nil || *c.Drift != 0 {
					return calls, fmt.Errorf("%s: drift %s, want 0: the block each message ends with must be counted", c.Label, c.drift())
				}
				if i == 0 {
					continue
				}
				prev, cached := calls[i-1].PromptTokens, c.CachedTokens
				if prev == nil || cached == nil {
					return calls, fmt.Errorf("%s: the server stated no prompt or cached count", c.Label)
				}
				if float64(*cached) < prefixHold*float64(*prev) {
					return calls, fmt.Errorf("%s: %d tokens cached of the previous call's %d, want at least %.0f%%: the prefix changed between calls",
						c.Label, *cached, *prev, prefixHold*100)
				}
			}
			for i, r := range recorded {
				if r.Context.Messages[0].Context == "" {
					return calls, fmt.Errorf("loop call %d: the first message carries no context block", i+1)
				}
			}
			return calls, nil
		},
	}
}
