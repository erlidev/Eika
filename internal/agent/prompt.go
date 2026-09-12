package agent

import (
	"context"
	"strings"

	"github.com/erlidev/eika/internal/contextfile"
)

// basePrompt is the role and the working rules every Eika agent gets. It stays
// short on purpose: workspace instructions and the user say the rest.
const basePrompt = `You are Eika, a coding agent working inside a sandboxed workspace.

Rules:
- Every file and command action goes through your tools. The workspace root is your working directory and paths are relative to it.
- Read a file before editing it. Prefer edit over write for existing files, and make old_string unique.
- Run the project's own build and test commands to check your work.
- Work in small, complete steps: change the code, run the tests, report what happened.
- Be concise. Answer in plain sentences, without preamble or summaries of what you are about to do.
- If a request is ambiguous, state the interpretation you are using, or ask when you cannot proceed without an answer.`

// systemPrompt assembles the prompt for one run: the base rules, the
// workspace's context files, and the extra instructions the caller configured.
func (a *Agent) systemPrompt(ctx context.Context) (string, error) {
	parts := []string{basePrompt}
	if a.opts.Executor != nil {
		files, err := contextfile.Discover(ctx, a.opts.Executor, contextfile.Options{Dir: a.opts.ContextDir})
		if err != nil {
			return "", err
		}
		if section := contextfile.Section(files); section != "" {
			parts = append(parts, section)
		}
	}
	if extra := strings.TrimSpace(a.opts.SystemPrompt); extra != "" {
		parts = append(parts, extra)
	}
	return strings.Join(parts, "\n\n"), nil
}
