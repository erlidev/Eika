// Package contextfile discovers the AGENTS.md files that tell an agent how to
// work in a workspace and turns them into a system prompt section.
//
// Discovery follows Pi: a global file first, then every directory from the
// workspace root down to the working directory, so that the most specific
// instructions come last and win. A directory's AGENTS.override.md replaces
// that directory's AGENTS.md or CLAUDE.md entirely.
//
// Every file is read through an executor.Executor, so discovery sees the
// workspace and never the harness filesystem. The entry points are Discover
// and Section.
package contextfile
