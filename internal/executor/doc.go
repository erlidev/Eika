// Package executor is the only way an agent tool touches a workspace.
//
// Executor abstracts running a command and reading, writing, and listing files
// inside one workspace root. The production implementation talks to the
// sandbox daemon; executor/local runs against a directory on the harness and
// exists for tests only. Tools depend on this package and never on the Docker
// or workspace packages.
//
// Every path an agent supplies is workspace-relative and must be resolved with
// Resolve, which rejects traversal outside the root. The package depends only
// on the standard library.
package executor
