// Package subagent spawns child agents and reports what they did.
//
// A Spawner takes a task from a running parent session and gives it to a
// child agent working in a sandbox of its own: it commits and pushes the
// parent's work to the git hub, clones a child workspace from the hub at that
// commit on a branch of its own, opens a child session, runs the agent loop
// in it through a Runner, and turns what the child left behind into one
// result the parent sees as a tool result.
//
// It depends on internal/store for the rows, internal/workspace for the
// sandboxes and the hub, internal/session for the child's messages, and
// internal/event for the subagent.started and subagent.finished events. The
// agent tools reach it through the builtin.Subagents interface, so no package
// under internal/tool has to know what a workspace is. The server injects it
// and the run manager it drives children with. The depth and width limits are
// read through Options.Limits at every spawn, so the settings can change them
// while the harness runs.
package subagent
