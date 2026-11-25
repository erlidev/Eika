// Package agentdef defines the built-in agents and their embedded prompts.
// It does no I/O at runtime. The eika agent has compaction prompts of its
// own; the subagents take the loop's built-in ones (agent.SummaryPrompt and
// its siblings) with one closing line naming what their summaries keep.
// Builtins and Lookup return independent definitions; Override applies the
// user-editable class and system prompt. The server and store use these values.
package agentdef
