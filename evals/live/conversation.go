package live

import (
	"context"
	"fmt"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
)

// CountingConversation counts one history with tools, replayed reasoning,
// pictures and a compaction. Pictures use a measured cost, never exact.
// PrefixAcrossCompaction separately tests the loop that makes the summary.
func CountingConversation(nonce string) Scenario {
	return Scenario{
		Name: "counting_conversation",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			if !t.Server.Flags.Vision {
				return nil, ErrNoVision
			}
			req := toolsAndReasoningRequest(nonce)
			var calls []Call
			send := func(label string, picture, learned bool) error {
				c, reply, err := t.Complete(ctx, label, req)
				calls = append(calls, c)
				if err != nil {
					return err
				}
				if picture {
					if err := requireImageMethod(c, learned); err != nil {
						return err
					}
				} else if err := requireExact(c); err != nil {
					return err
				}
				if !picture || learned {
					if c.Drift == nil || *c.Drift != 0 {
						return fmt.Errorf("%s: drift %s, want 0", label, c.drift())
					}
				}
				req.Messages = append(req.Messages, provider.AssistantMessage(reply, nil))
				return nil
			}
			if err := send("tools and reasoning", false, false); err != nil {
				return calls, err
			}
			picture, err := gradient(56, 56)
			if err != nil {
				return calls, err
			}
			m := provider.UserMessage("Describe this picture briefly.")
			m.Images = []provider.Image{picture}
			req.Messages = append(req.Messages, m)
			if err := send("measure picture", true, false); err != nil {
				return calls, err
			}
			req.Messages = append(req.Messages, provider.UserMessage("What do the notes and the picture have in common?"))
			if err := send("measured picture with tool history", true, true); err != nil {
				return calls, err
			}
			// Use the session's real fold with a fixed summary. This makes
			// the cut deterministic and removes the picture and tool rounds.
			req.Messages = agent.Compacted(req.Messages, agent.Compaction{
				Summary: "The notes say to call the bank on Monday and buy milk. The picture is a gradient.",
				Kept:    2, Reason: "manual",
			})
			req.Messages = append(req.Messages, provider.UserMessage("Which note mentions a day?"))
			if err := send("after compaction", false, false); err != nil {
				return calls, err
			}
			req.Messages = append(req.Messages, provider.UserMessage("Give that day again."))
			if err := send("follow-up after compaction", false, false); err != nil {
				return calls, err
			}
			t.Note("Image calls use measured image cost; /apply-template and /tokenize count only text. Text calls before and after the compaction must be exact.")
			return calls, nil
		},
	}
}

// UsageFields checks whether the reported prompt includes cached tokens.
// Missing usage fields stay null in the calls; no count is filled in here.
func UsageFields(nonce string) Scenario {
	return Scenario{
		Name: "usage_fields",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			calls, err := CacheReuse(nonce).Run(ctx, t)
			if err != nil {
				return calls, err
			}
			for i := range calls {
				c := &calls[i]
				if err := requireExact(*c); err != nil {
					return calls, err
				}
				if err := validateCachedTokens(*c); err != nil {
					return calls, err
				}
				if c.CachedTokens == nil || *c.CachedTokens == 0 {
					continue
				}
				if c.Predicted == nil || c.PromptTokens == nil {
					return calls, fmt.Errorf("%s: missing prompt measurement", c.Label)
				}
				includes := *c.Predicted == *c.PromptTokens
				c.PromptIncludesCached = &includes
				t.Note("%s: prompt_tokens includes cached tokens: %t (rendered %d, reported %d, cached %d)", c.Label, includes, *c.Predicted, *c.PromptTokens, *c.CachedTokens)
				if !includes {
					return calls, fmt.Errorf("%s: prompt count excludes cached tokens or differs from the rendered count", c.Label)
				}
				if c.PrefilledTokens != nil && *c.PrefilledTokens+*c.CachedTokens != *c.PromptTokens {
					return calls, fmt.Errorf("%s: prefilled plus cached tokens differs from prompt tokens", c.Label)
				}
			}
			return calls, nil
		},
	}
}

func validateCachedTokens(c Call) error {
	if c.CachedTokens != nil && (*c.CachedTokens < 0 || (c.PromptTokens != nil && *c.CachedTokens > *c.PromptTokens)) {
		return fmt.Errorf("%s: cached tokens %d outside prompt token count", c.Label, *c.CachedTokens)
	}
	return nil
}

func requireImageMethod(c Call, learned bool) error {
	if learned && c.Method != string(provider.MethodAnchored) {
		return fmt.Errorf("%s: method %s, want anchored", c.Label, c.Method)
	}
	if c.Method == string(provider.MethodExact) {
		return fmt.Errorf("%s: image tokens cannot be called exact", c.Label)
	}
	return nil
}
