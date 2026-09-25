# mcp

The Model Context Protocol servers the user configured, as the UI shows
them. The harness connects to them and offers their tools to runs; this
feature only configures, inspects, and signs in to them.

- `MCPSettings` is the MCP tab of the settings dialog: the list of servers,
  the add form (`MCPServerForm`, whose rules are `form.ts`), and each
  server's page (`MCPServerView`): its state, connect and sign-in, its tools
  with a switch each, its resources and prompts to try, its log, and what
  the connection learned.
- `MCPCallbackScreen` is the `/mcp/callback` page an OAuth authorization
  server returns the browser to; it posts the answer to the harness and
  opens the server's settings again.
- `ContentBlocks` draws what a server sends (text, images, audio,
  resources); the session's MCP tool card uses it too.
- `useMCPEvents` subscribes to `mcp.server` on the global topic and
  refreshes the servers and the tool list, so nothing polls.

Test: `npm test -- src/features/mcp` for the rules, and
`e2e/specs/mcp.spec.ts` against the mock harness's `mcp` scenario.
