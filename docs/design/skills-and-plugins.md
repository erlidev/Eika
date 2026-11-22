# Skills and plugins

Skills are instructions and resources the user installs. Plugins are MCP servers
the user connects. Both are listed compactly in the preamble and reached through a
fixed set of generic tools. Installing one never adds a tool to a running
session, which would break its prefix cache, since chat templates render tools at
the very start of the prompt.

## Skills

### Format

Skills follow the open Agent Skills specification (agentskills.io) exactly:

```
<name>/
  SKILL.md        required: YAML frontmatter + Markdown body
  scripts/        optional
  references/     optional
  assets/         optional
```

Frontmatter takes the six spec fields only. Eika does not add its own, so a skill
works in other clients unchanged:

| Field | Rule |
|---|---|
| `name` | Required. 1 to 64 characters, lowercase letters, digits and hyphens, no leading, trailing or double hyphen; matches the directory name |
| `description` | Required. 1 to 1,024 characters, saying what the skill does and when to use it |
| `license` | Optional |
| `compatibility` | Optional. Up to 500 characters of environment requirements |
| `metadata` | Optional. A string-to-string map |
| `allowed-tools` | Optional and experimental in the spec. Stored and shown. It grants nothing in the first version (see deferred) |

Eika's own settings for a skill live in the `skills` row, not in the file:
enabled, and when it was installed.

Parsing is lenient, the way the spec's client guide advises. A name and directory
mismatch is a warning, and an unquoted colon in a description is retried quoted.
A missing name falls back to the directory name with a warning. A skill is skipped
when its description is missing, its YAML is unparseable, or its name is invalid
(it becomes a directory path, so it must be 1 to 64 lowercase letters, digits and
single hyphens), and the reason shows in the UI.

### Storage and scope

```
/var/lib/eika/skills/<name>/...         volume eika-skills, read-only to every computer
core.skills: name, enabled, sha, installed_at, warnings
```

### Installing

This happens in the UI, and no agent tool installs or edits a skill. Drop a `.zip`
onto the Skills page, or pick one.

Every install shows the full `SKILL.md` and the file list, and the user enables the
skill explicitly. Skills from the internet are untrusted until the user reads them.
This is the trust gate the spec's client guide recommends.

### How agents see them

- **Catalog in the preamble**, frozen per session and sorted by name:

  ```
  ## Skills (skill_use <name> to load one)
  - pdf-forms: Fill and flatten PDF forms. Use when the user sends a PDF form or asks to fill one.
  - trip-planner: ...
  ```

  Each line costs about 50 to 100 tokens.
- **`skill_use {name}`** returns the body, with frontmatter stripped, wrapped as
  `<skill name="..." dir="/skills/<name>">...</skill>`, followed by the list of
  resource files without their contents (tier 3 of progressive disclosure).
  `skill_use {query}` searches names and descriptions.
- Resource files are read, and scripts run, on the computer from
  `/skills/<name>/`, so the agent needs a computer to use them.
- The tool takes no enum of skill names. An enum would put the catalog inside the
  tool schema, and installing a skill would then change every session's tools.
  Names are checked at call time instead.
- A skill installed mid-session enters the catalog at the next compaction.

### Deferred

`DEFERRED.md` lists what was trimmed. Two ideas predate the cut:

- Honouring `allowed-tools` as a task-scoped grant while the skill is loaded. Skills
  are user-installed, so this is the user's grant, but loading a skill must not
  become a way for a model to gain permissions. It waits for real use cases.
- Skill proposals: an agent drafts a skill in a project directory and the Inbox
  offers to install it. Agents still never install skills themselves.

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
