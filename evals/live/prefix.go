package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/tool"
)

// storedEntries keeps what a loop stores, as JSON, the way a session's entries
// are, and rebuilds a conversation from them. It is what a restarted loop reads
// its session from, so nothing of the first loop's memory reaches the second.
type storedEntries struct {
	mu      sync.Mutex
	entries []storedEntry
}

// storedEntry is a message or a compaction.
type storedEntry struct {
	message    json.RawMessage
	compaction *agent.Compaction
}

func (s *storedEntries) Append(_ context.Context, _ string, m provider.Message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, storedEntry{message: raw})
	return nil
}

func (s *storedEntries) AppendCompaction(_ context.Context, _ string, c agent.Compaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, storedEntry{compaction: &c})
	return nil
}

// session reads the conversation the entries stand for.
func (s *storedEntries) session() (*agent.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var msgs []provider.Message
	for _, e := range s.entries {
		if e.compaction != nil {
			msgs = agent.Compacted(msgs, *e.compaction)
			continue
		}
		var m provider.Message
		if err := json.Unmarshal(e.message, &m); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return &agent.Session{ID: "live", Conversation: agent.NewConversation(msgs...)}, nil
}

// PrefixAcrossCompaction is the scenario that shows on the real server that
// the prefix survives what the loop does to it: five turns on one session with
// a restart of the loop's state before the third turn and another with the
// compaction between the third and the fourth. A restart is a new agent, a new
// token counter and a conversation read back from what was stored, and each
// loop has a different output limit, which fit also moves and no server may
// read as part of the prompt. Every call must be predicted exactly with no
// drift, and every call must find at least prefixHold of the previous call's
// prompt in the server's cache, except the first after the compaction, which
// the compaction replaced the prefix of by design: it is reported, and the call
// after it must hold again.
func PrefixAcrossCompaction(nonce string) Scenario {
	return Scenario{
		Name: "prefix_across_compaction",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			ctx, cancel := context.WithTimeout(ctx, agentLoopTimeout)
			defer cancel()
			system := fmt.Sprintf("Session %s.\nYou are Eika. Answer every question in two short sentences.", nonce)
			stored := &storedEntries{}
			records := &callRecords{}
			// build starts a loop with nothing in memory, and the output limit
			// it is given.
			build := func(maxOutput int, c *counter.Counter) (*agent.Agent, error) {
				tools, err := tool.NewRegistry(lookUp{})
				if err != nil {
					return nil, err
				}
				sampling := quiet()
				sampling.MaxOutput = &maxOutput
				return agent.New(t.Provider, tools, agent.Options{
					Model:            t.Model,
					ContextWindow:    32768,
					BasePrompt:       &system,
					Sampling:         sampling,
					ThinkingSwitch:   provider.SwitchTemplate,
					PreserveThinking: true,
					Store:            stored,
					Recorder:         records,
					Counter:          c,
					Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
					Compaction:       agent.CompactionSettings{KeepRecentTokens: 150},
				}), nil
			}
			ask := func(a *agent.Agent, text string) error {
				s, err := stored.session()
				if err != nil {
					return err
				}
				if err := a.Run(ctx, s, provider.UserMessage(text)); err != nil {
					return fmt.Errorf("run %q: %w", text, err)
				}
				return nil
			}

			first, err := build(120, t.Counter)
			if err != nil {
				return nil, err
			}
			var runErr error
			for _, text := range []string{
				"Why does a server keep the start of a prompt it has already read?",
				"What would make it throw that start away?",
			} {
				if runErr = ask(first, text); runErr != nil {
					break
				}
			}
			compactedAt := -1
			if runErr == nil {
				second, err := build(400, counter.New(t.Source))
				if err != nil {
					return nil, err
				}
				if runErr = ask(second, "Which of those two matters more to a person using a chat?"); runErr == nil {
					s, err := stored.session()
					if err != nil {
						return nil, err
					}
					compactedAt = len(records.all())
					if runErr = second.Compact(ctx, s, ""); runErr != nil {
						runErr = fmt.Errorf("compact: %w", runErr)
					}
				}
			}
			if runErr == nil {
				third, err := build(200, counter.New(t.Source))
				if err != nil {
					return nil, err
				}
				for _, text := range []string{
					"Remind me, in your own words, what we settled on.",
					"And what should I check first?",
				} {
					if runErr = ask(third, text); runErr != nil {
						break
					}
				}
			}

			calls := callsOf(records.all())
			if runErr != nil {
				return calls, runErr
			}
			if len(calls) != 5 || compactedAt != 3 {
				return calls, fmt.Errorf("the loop recorded %d calls with the compaction after %d, want 5 calls and the compaction after the third", len(calls), compactedAt)
			}
			for i, c := range calls {
				if err := requireExact(c); err != nil {
					return calls, err
				}
				if c.Drift == nil || *c.Drift != 0 {
					return calls, fmt.Errorf("%s: drift %s, want 0", c.Label, c.drift())
				}
				if i == 0 {
					continue
				}
				prev, cached := calls[i-1].PromptTokens, c.CachedTokens
				if prev == nil || cached == nil || c.PromptTokens == nil {
					return calls, fmt.Errorf("%s: the server stated no prompt or cached count", c.Label)
				}
				if i == compactedAt {
					t.Note("%s: the first after the compaction found %d of its %d prompt tokens cached (the previous call's was %d): the compaction replaced the prefix",
						c.Label, *cached, *c.PromptTokens, *prev)
					continue
				}
				if float64(*cached) < prefixHold*float64(*prev) {
					return calls, fmt.Errorf("%s: %d tokens cached of the previous call's %d, want at least %.0f%%: the prefix changed between calls",
						c.Label, *cached, *prev, prefixHold*100)
				}
			}
			return calls, nil
		},
	}
}

// callsOf turns what a loop recorded into the calls of a scenario.
func callsOf(recorded []agent.ModelCall) []Call {
	calls := make([]Call, 0, len(recorded))
	for i, r := range recorded {
		c := newCall(fmt.Sprintf("loop call %d", i+1), r.Usage, provider.Timings{}, "", 0)
		c.ReasoningTokens, c.ReasoningSource = r.Reasoning.Ptr(), r.ReasoningSource
		if p := r.Prediction; p.Method != "" {
			tokens := p.Tokens
			c.Predicted, c.Method, c.Drift = &tokens, string(p.Method), r.Drift
		}
		calls = append(calls, c)
	}
	return calls
}
