// Package eikad implements the sandbox daemon that runs inside every
// workspace container.
//
// It is the only process the harness talks to when an agent runs a command,
// reads or writes a file, opens a terminal, watches for changes, or starts a
// stdio MCP server, whose standard streams /process carries. Every path
// it accepts is confined to the workspace root: traversal and symlinks that
// leave the root are rejected. Every route but /healthz requires the bearer
// token the harness passes as EIKAD_TOKEN at container start.
//
// The entry points are New and Daemon.Handler. The wire types in api.go are
// the contract with internal/executor/sandbox and are documented in
// docs/api/eikad.md. It depends on internal/server for the listener lifecycle.
package eikad
