// Package mcptest holds the fakes the MCP client is tested against: Server,
// a scripted MCP server that speaks the modern revision, an initialize-based
// one over Streamable HTTP, or the 2024-11-05 HTTP+SSE transport, over HTTP
// or a stdio pipe; and Authorization, an OAuth authorization server that
// registers clients, approves at once, and checks PKCE and the resource.
// The server's handler tests use them too.
package mcptest
