# Skills and plugins

Skills are instructions and resources the user installs. Plugins are MCP servers
the user connects. The skills design is built; the plugin design below is future work.

## Skills

Skills are built. See [Architecture](../ARCHITECTURE.md#skills) and
[Decisions](../DECISIONS.md#skills-installer-computer-p6-02). The deferred
skill work is in [DEFERRED.md](../DEFERRED.md#phase-6), including grants from
`allowed-tools` and skill proposals.

## Plugins

### Model

A plugin is one MCP server, either remote (Streamable HTTP or HTTP+SSE) or stdio.
`internal/mcp` carries over unchanged, and so does Core's pool: transports, eras,
OAuth 2.1, elicitation, catalog listing and change notifications. What changes is how
agents reach it:

| | Today | Now |
|---|---|---|
| Stdio servers | A process in the session's computer, with its environment visible to the agent | A process in one shared plugin computer (labelled `eika.role=plugin`) that no agent can execute in |
| Tool exposure | One `tool.Tool` per MCP tool | Catalog plus `plugin_find` and `plugin_call` (below), for `eika` only |
| Permission | Static tool choices per profile | Sentry tool settings, with defaults from MCP annotations |

### How agents see them

- **Catalog in the preamble**, frozen per session, one block per enabled plugin:

  ```
  ## Plugins (plugin_find to see a tool's arguments, plugin_call to use it)
  - calendar: Your Google Calendar.  tools: list_events, create_event, delete_event
  - github: GitHub as erlidev.  tools: search_issues, get_issue, create_issue (+14)
  ```

  Tool names go in, descriptions do not. A plugin with more than 8 tools lists the
  first 8 and a count.
- **`plugin_find {plugin?, query?}`** returns matching tools with their descriptions
  and full input schemas, as text in the result. The schema reaches the model
  without entering the tools array.
- **`plugin_call {plugin, tool, arguments}`** with a free-form `arguments` object.
  Core decides it through the gate, attaches credentials, calls, puts the result
  through ingest, and returns it. Bad arguments come back as the server's error.

### Permissions

Each tool has a setting: `allow`, `known`, `ask` or `block`, and may have a
destination argument, such as a recipient. The default comes from the tool's
schema and MCP annotations (`sentry.md` 5): `readOnlyHint` gives `allow`, and a
tool with a destination argument gives `known`, which asks only the first time it
reaches a destination. The Plugins page shows the setting per tool and lets the
user change it. An approval card's "always" changes it too, or records the
destination (`sentry.md` 7). A server that marks a sending tool read-only gets it
allowed, which is why the page lists every tool's setting when a plugin is added.

A plugin can be marked trusted: its results are still scored by the screen, and
nothing is removed from them. It suits a plugin that holds only the user's own
notes.

### Result filtering

Every result goes through ingest (`sentry.md` 6): secret redaction, one-time-code
removal (for mail and messaging plugins), the injection screen and wrapping. Audio and images in results
keep today's handling: kept in details, replaced in the model's text.

### Elicitation and OAuth

Elicitation requests from a plugin become Inbox items, using today's form and
URL-mode UI, moved to the Inbox. OAuth sign-in stays as it is today, from the Plugins
page, with tokens sealed in Core.

### Health

Plugin state (`connecting`, `ready`, `auth_required`, `failed`) reaches the UI
through `mcp.server` events, as today. A plugin that
fails during a session gives a normal tool error. The catalog line stays, since
the prefix must not change. The error says the plugin is unavailable.
