package live

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/utility"
)

// maxTitleWords is how long a title may run before the scenario calls it an
// answer: the prompt asks for six words at most.
const maxTitleWords = 12

// SessionTitle is the scenario that runs the session title task, the work of
// the fast class, against the bench's chat model: one request with no tools
// and no history, thinking off through the template switch the bench's server
// takes, as the harness sends it. It requires a short single-line title that
// holds no reasoning, and that a message the model could answer is named, not
// answered.
func SessionTitle() Scenario {
	return Scenario{
		Name: "session_title",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			ctx, cancel := context.WithTimeout(ctx, callTimeout)
			defer cancel()
			model := utility.Model{
				Provider:         t.Provider,
				ID:               t.Model,
				ReasoningEfforts: []string{provider.EffortNone},
				ThinkingSwitch:   provider.SwitchTemplate,
			}
			start := time.Now()
			title, err := utility.Title(ctx, model, "The login page loops forever after I enter my password. Please find out why and fix it.")
			call := Call{Label: "session title", WallSeconds: time.Since(start).Seconds()}
			if err != nil {
				return []Call{call}, fmt.Errorf("make the title: %w", err)
			}
			call.Label = fmt.Sprintf("session title %q", title)
			switch {
			case strings.ContainsAny(title, "\r\n"):
				return []Call{call}, fmt.Errorf("title %q is more than one line", title)
			case len(strings.Fields(title)) > maxTitleWords:
				return []Call{call}, fmt.Errorf("title %q is longer than %d words, so the model answered the message", title, maxTitleWords)
			case strings.Contains(title, "<think>"):
				return []Call{call}, fmt.Errorf("title %q holds reasoning, so thinking was not off", title)
			}
			return []Call{call}, nil
		},
	}
}
