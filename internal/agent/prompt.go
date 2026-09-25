package agent

import (
	"context"
	"strings"

	"github.com/erlidev/eika/internal/contextfile"
)

// WorkspacePrompt is the built-in base prompt of an agent with a workspace:
// the role and the working rules. It stays short on purpose: workspace
// instructions and the user say the rest. Options.BasePrompt replaces it.
const WorkspacePrompt = `You are Eika, a coding agent working inside a sandboxed workspace.

Rules:
- Every file and command action goes through your tools. The workspace root is your working directory and paths are relative to it.
- Run the project's own build and test commands to check your work.
- Work in small, complete steps: change the code, run the tests, report what happened.
- Be concise. Answer in plain sentences, without preamble or summaries of what you are about to do.
- If a request is ambiguous, state the interpretation you are using, or ask when you cannot proceed without an answer.`

// ChatPrompt is the built-in base prompt of an agent with no workspace. A
// model told it works in a sandbox would offer to read files and run
// commands it has no tools for. Options.BasePrompt replaces it.
const ChatPrompt = `You are Eika, an assistant in a chat with the user.

Rules:
- This chat has no workspace: there are no files to read or change and no commands to run. Use only the tools you are given; there may be none.
- When you use the web, say where an answer came from and link the pages you relied on.
- Be concise. Answer in plain sentences, without preamble or summaries of what you are about to do.
- If a request is ambiguous, state the interpretation you are using, or ask when you cannot proceed without an answer.`

// prompt assembles the system prompt sections of one run: the base prompt,
// the workspace's context files, and the extra instructions, each present
// only when it has text. An agent with no workspace reads no context files.
func (a *Agent) prompt(ctx context.Context) ([]Section, error) {
	base := ChatPrompt
	if a.opts.Executor != nil {
		base = WorkspacePrompt
	}
	if a.opts.BasePrompt != nil {
		base = *a.opts.BasePrompt
	}
	var sections []Section
	if text := strings.TrimSpace(base); text != "" {
		sections = append(sections, newSection(SectionBase, text))
	}
	if a.opts.Executor != nil && !a.opts.SkipContextFiles {
		files, err := contextfile.Discover(ctx, a.opts.Executor, contextfile.Options{Dir: a.opts.ContextDir})
		if err != nil {
			return nil, err
		}
		if text := contextfile.Section(files); text != "" {
			section := newSection(SectionContextFiles, text)
			for _, f := range files {
				section.Files = append(section.Files, ContextFile{
					Path:   f.Path,
					Text:   f.Content,
					Tokens: EstimateTokens(f.Content),
				})
			}
			sections = append(sections, section)
		}
	}
	if text := strings.TrimSpace(a.opts.Instructions); text != "" {
		sections = append(sections, newSection(SectionInstructions, text))
	}
	return sections, nil
}

// newSection returns a section of the system prompt carrying text.
func newSection(kind SectionKind, text string) Section {
	return Section{Kind: kind, Text: text, Tokens: EstimateTokens(text)}
}
