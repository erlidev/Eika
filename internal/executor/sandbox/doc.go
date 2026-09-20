// Package sandbox implements executor.Executor against the eikad daemon.
//
// It is the production executor: every file and process action an agent takes
// travels over HTTP to the daemon inside the workspace container, which
// confines it to the workspace root. Construct one with New, or get one for a
// running workspace from internal/workspace. Client.Terminal opens the
// daemon's interactive shell; it is not part of executor.Executor, so a tool
// can never reach it.
//
// The wire types come from internal/eikad, which owns them; they are
// documented in docs/api/eikad.md.
package sandbox
