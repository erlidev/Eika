// Package mcp is Eika's Model Context Protocol client: it connects to the
// servers the user configured and offers their tools to runs.
//
// A Client is one connection. It speaks the 2026-07-28 revision, whose
// requests are stateless and carry their version in _meta, and falls back to
// the initialize-based revisions from 2025-11-25 to 2024-11-05 for a server
// that does not answer server/discover as a modern one; over HTTP it falls
// back further to the deprecated HTTP+SSE transport. Streamable HTTP
// (http.go), HTTP+SSE (legacysse.go), and stdio (stdio.go) are the
// transports. A request whose server needs input, by a multi round-trip
// result or an older server's own request, is answered through an
// ElicitFunc.
//
// The Pool is the entry point for the harness. It keeps one connection to
// each remote server and one to each stdio server in each workspace that
// runs it, starting the process through a Launcher, reads the servers'
// configuration and credentials through a Store, and reports every change
// as an mcp.server event. Tools turns what the servers offer into tool.Tool
// values named mcp__<server>__<tool>. BeginAuthorization and
// CompleteAuthorization run the OAuth flow of internal/mcp/oauth, and
// Elicitations holds what servers ask the user while a call waits.
//
// Every bound the client applies is in limits.go. The package depends on
// internal/event, internal/tool, and internal/mcp/oauth, and on nothing that
// knows a workspace: the harness hands it a Store and a Launcher.
package mcp
