// Package hub keeps Eika's bare git repositories and serves them to
// workspaces over HTTP.
//
// The hub is the single point of exchange between workspaces, subagents, and
// the user: every workspace clones from it and pushes to it, and the harness
// mirrors a project to and from its upstream remote. One bare repository per
// project lives at <root>/<project>.git.
//
// The harness runs the git binary itself here, which is the one place it may:
// hub maintenance is not an agent action. Remote credentials stay in the
// harness process and are passed to git through the environment, never
// written to disk. Workspaces authenticate to Handler with a per-workspace
// token granted by Grant.
package hub
