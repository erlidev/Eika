// Package builtin holds the tools every Eika agent has: read, write, edit,
// bash, grep, find, and ls; ask_user and the agent tools; and web_search and
// web_fetch, which reach the web through internal/search.
//
// The tools follow the semantics of the Pi coding agent: reads are
// line-numbered and bounded by offset and limit, edits replace a string that
// must occur exactly once, and command output is truncated in the middle so
// that both the start and the end survive. Every action goes through the
// executor in the call context; nothing here touches the harness filesystem.
// A web_fetch filter is the model's JavaScript, so it runs as `eikad filter`
// in the workspace too.
//
// The entry point is Registry, which returns a tool.Registry holding all of
// them. Adding a built-in tool means adding it there. The output limits are
// constants in limits.go.
package builtin
