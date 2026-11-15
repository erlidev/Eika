package agent

import "strings"

// ChatPrompt is the built-in base prompt of every agent. It makes no claim
// about files or commands: a model told it works in a sandbox would offer to
// read files and run commands it has no tools for, so it says to use only the
// tools it is given. Options.BasePrompt replaces it.
const ChatPrompt = `You are Eika, an assistant in a chat with the user.

Rules:
- Use only the tools you are given; there may be none. Do not offer to read files or run commands unless you have tools for them.
- When you use files or commands, they go through your tools, and paths are relative to your working directory.
- When you use the web, say where an answer came from and link the pages you relied on.
- Be concise. Answer in plain sentences, without preamble or summaries of what you are about to do.
- If a request is ambiguous, state the interpretation you are using, or ask when you cannot proceed without an answer.`

// prompt assembles the system prompt sections of one run: the base prompt
// and the extra instructions, each present only when it has text.
func (a *Agent) prompt() []Section {
	a.snapshotMu.RLock()
	base := a.opts.BasePrompt
	a.snapshotMu.RUnlock()
	return a.promptWith(base)
}

// promptWith assembles the system prompt sections with base as the base
// prompt, or the built-in ChatPrompt when it is nil.
func (a *Agent) promptWith(basePrompt *string) []Section {
	base := ChatPrompt
	if basePrompt != nil {
		base = *basePrompt
	}
	var sections []Section
	if text := strings.TrimSpace(base); text != "" {
		sections = append(sections, newSection(SectionBase, text))
	}
	if text := strings.TrimSpace(a.opts.Instructions); text != "" {
		sections = append(sections, newSection(SectionInstructions, text))
	}
	return sections
}

// newSection returns a section of the system prompt carrying text.
func newSection(kind SectionKind, text string) Section {
	return Section{Kind: kind, Text: text, Tokens: EstimateTokens(text)}
}
