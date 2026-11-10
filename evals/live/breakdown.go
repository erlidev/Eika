package live

import (
	"context"
	"fmt"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
)

// Bounds on the template's remainder of a plain request on the bench: the
// markers and role names a chat template wraps each message in, the markup
// of a tool call, and the generation prompt. On the bench (Qwen3.8-27B, b11942)
// the request below left 60 tokens for its six messages and sections; the
// bounds (104 there) leave room for a wordier template and still fail a part
// that is counted twice (a negative remainder) or left out (a large one).
const (
	remainderPerMessage = 12
	remainderFixed      = 32
)

// ContextBreakdown is the scenario that checks the Context breakdown against
// the server: the parts of a plain request (a system prompt of two sections,
// two tools, a short conversation with a tool call and replayed reasoning),
// counted one by one with the session's counter, plus the template's
// remainder, which is the server's prompt count less the parts, must equal
// that prompt count (nothing scaled to make it so), and the remainder must be
// small and not negative: the template adds a few tokens around each message,
// and a negative remainder is a part counted twice. Parts counted exactly
// where the server tokenises, so none may be marked approximate. It makes one
// model call.
func ContextBreakdown(nonce string) Scenario {
	return Scenario{
		Name: "context_breakdown",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			schema := func(props string) []byte {
				return []byte(`{"type":"object","properties":{` + props + `},"required":["path"]}`)
			}
			tools := []agent.ToolSchema{
				{ToolDef: provider.ToolDef{Name: "list_dir", Description: "List the entries of a directory.", Schema: schema(`"path":{"type":"string","description":"Directory to list."}`)}},
				{ToolDef: provider.ToolDef{Name: "read_file", Description: "Read a text file and return its content.", Schema: schema(`"path":{"type":"string","description":"File to read."}`)}},
			}
			c := agent.Context{
				Sections: []agent.Section{
					{Kind: agent.SectionBase, Text: fmt.Sprintf("Session %s.\nYou are Eika, a personal assistant. Use the tools to look at files, then answer briefly.", nonce)},
					{Kind: agent.SectionInstructions, Text: "Answer in one short sentence."},
				},
				Tools: tools,
				Messages: []provider.Message{
					provider.UserMessage("What is in /home/eika, and what do my notes say?"),
					provider.AssistantMessageWithReasoning("I will start with the directory.",
						"The user wants the directory first, then the notes. List /home/eika to find the notes file.",
						[]provider.ToolCall{{ID: "call_1", Name: "list_dir", Arguments: `{"path":"/home/eika"}`}}),
					provider.ToolResultMessage("call_1", "notes.md\ntodo.txt\nprojects/", false),
					provider.UserMessage("Now read the notes."),
				},
				Parameters: agent.Parameters{
					Model:            t.Model,
					Sampling:         quiet(),
					ThinkingSwitch:   provider.SwitchTemplate,
					PreserveThinking: true,
				},
			}

			parts := agent.MeasureParts(ctx, t.Counter, c)
			call, _, err := t.Complete(ctx, "breakdown", c.Request())
			calls := []Call{call}
			if err != nil {
				return calls, err
			}
			if call.PromptTokens == nil {
				return calls, fmt.Errorf("the server stated no prompt count, so the remainder has nothing to be stated against")
			}
			// The report states the split, so a template change shows.
			calls[0].Label = fmt.Sprintf("breakdown (%d in parts, %d template and formatting)", parts.Parts, *call.PromptTokens-parts.Parts)
			return calls, checkBreakdown(parts, *call.PromptTokens, len(c.Messages)+len(c.Sections))
		},
	}
}

// checkBreakdown holds a breakdown to the server's prompt count: the parts and
// the remainder add up to it, no part is approximate, and the remainder is not
// negative and not larger than the template can account for in a request of
// messages messages and sections.
func checkBreakdown(parts agent.Breakdown, prompt, messages int) error {
	remainder := prompt - parts.Parts
	if parts.Approximate() {
		return fmt.Errorf("a part of the breakdown is approximate, but the server tokenises and renders every part of this request: %+v", parts)
	}
	if sum := parts.Parts + remainder; sum != prompt {
		return fmt.Errorf("the parts (%d) and the remainder (%d) add up to %d, want the server's %d", parts.Parts, remainder, sum, prompt)
	}
	if remainder < 0 {
		return fmt.Errorf("the remainder is %d: the parts add up to %d, more than the server's %d, so a part is counted twice or wrongly", remainder, parts.Parts, prompt)
	}
	if bound := remainderFixed + remainderPerMessage*messages; remainder > bound {
		return fmt.Errorf("the remainder is %d tokens, more than the %d the template can account for: the parts (%d) leave out part of the %d-token prompt", remainder, bound, parts.Parts, prompt)
	}
	return nil
}
