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
enabled, scope, which agents list it, and where it came from.

Parsing is lenient, the way the spec's client guide advises. A name and directory
mismatch is a warning, and an unquoted colon in a description is retried quoted.
A skill is skipped only when its description is missing or its YAML is
unparseable, and the reason shows in the UI.

### Storage and scope

```
/var/lib/eika/skills/<name>/...         Core volume, read-only to every computer
core.skills: name, scope (global | project:<slug>), enabled, agents[], source
             (upload | git:<url>@<commit> | path), sha, installed_at, warnings
```

A project-scope skill overrides a global one of the same name inside that
project's sessions.

### Installing

All of these happen in the UI. No agent tool installs or edits a skill.

- Drop a folder or a `.zip` onto the Skills page, or pick one.
- Paste a `SKILL.md`.
- A git URL (fetched by Sentry's proxy, pinned to the commit). Updating shows the
  diff before it applies.
- Import from a path on the `home` computer, so a skill Eika drafted in a project
  directory can be installed by the user after reading it.

Every install shows the full `SKILL.md`, the file list and Sentry's injection screen
over the instructions, and the user enables the skill explicitly. Skills from the
internet are untrusted until the user reads them. This is the trust gate the
spec's client guide recommends.

### How agents see them

- **Catalog in the preamble**, frozen per session and sorted by name:

  ```
  ## Skills (skill_use <name> to load one)
  - pdf-forms: Fill and flatten PDF forms. Use when the user sends a PDF form or asks to fill one.
  - trip-planner: ...
  ```

  Each line costs about 50 to 100 tokens. Above 40 enabled skills, the catalog
  lists names only and the descriptions are found through `skill_use` with
  `query`.
- **`skill_use {name}`** returns the body, with frontmatter stripped, wrapped as
  `<skill name="..." dir="/skills/<name>">...</skill>`, followed by the list of
  resource files without their contents (tier 3 of progressive disclosure). A
  repeated activation in the same session returns a short "already loaded" note.
  `skill_use {query}` searches names and descriptions.
- **`skill_file {name, path}`** reads one resource file. Scripts run on the
  computer from `/skills/<name>/scripts/...` with `bash`, so the agent needs a
  computer to run them.
- The tool takes no enum of skill names. An enum would put the catalog inside the
  tool schema, and installing a skill would then change every session's tools.
  Names are checked at call time instead.
- A skill installed mid-session appears as a tail notice ("new skill available:
  <name>, <description>"). It enters the catalog at the next compaction or session.
- **Compaction keeps loaded skills.** The rebuilt preamble includes the bodies of
  skills the session loaded, up to 8,000 tokens in total, newest first, the way
  Claude Code re-attaches skills after compaction.
- **Invocation by the user.** `/name` in the composer loads the skill into the
  next turn, as a skill block in the `<context>`.

### Deferred

- Honouring `allowed-tools` as a task-scoped Sentry grant while the skill is
  loaded. Skills are user-installed, so this is the user's grant, but loading a
  skill must not become a way for a model to gain permissions. It waits for real
  use cases.
- Skill proposals: an agent drafts a skill in a project directory and the Inbox
  offers to install it. Agents still never install skills themselves.

## Plugins

### Model

A plugin is one MCP server, either remote (Streamable HTTP or HTTP+SSE) or stdio.
`internal/mcp` carries over unchanged: transports, eras, OAuth 2.1, elicitation,
catalog listing and change notifications. What moves is where it runs:

| | Today | Now |
|---|---|---|
| Remote connections | Core's `mcp.Pool` | Sentry's plugin host, so OAuth tokens and headers live in the vault |
| Stdio servers | A process in the session's workspace, with its environment visible to the agent | A process in a plugin cell (a computer labelled `eika.role=plugin`) that no agent can execute in. Its environment's secrets are surrogates resolved by Sentry where possible, otherwise the real values, which stay in the cell |
| Tool exposure | One `tool.Tool` per MCP tool | Catalog plus `plugin_find` and `plugin_call` (below) |
| Permission | Static tool choices per profile | Sentry rules per tool, with defaults from MCP annotations |

One plugin cell runs per stdio plugin, from the sandbox image or a user-chosen
image. Its egress goes through Sentry's proxy with its own principal, so a plugin
cannot reach hosts the user did not allow.

### How agents see them

- **Catalog in the preamble**, frozen per session, one block per enabled plugin
  the agent's definition lists:

  ```
  ## Plugins (plugin_find to see a tool's arguments, plugin_call to use it)
  - calendar: Your Google Calendar.  tools: list_events, create_event, delete_event
  - github: GitHub as erlidev.  tools: search_issues, get_issue, create_issue (+14)
  ```

  Tool names go in, descriptions do not. A plugin with more than 8 tools lists the
  8 most-used and a count.
- **`plugin_find {plugin?, query?}`** returns matching tools with their descriptions
  and full input schemas, as text in the result. The schema reaches the model
  without entering the tools array.
- **`plugin_call {plugin, tool, arguments}`** with a free-form `arguments` object.
  Core forwards it to Sentry, which validates the arguments against the real
  schema (returning a normal error naming the bad field), decides, attaches
  credentials, calls, filters the result through ingest, and returns it.
- **Pinned plugin tools.** An agent definition may list specific plugin tools as
  native tools (`calendar.list_events`). They are rendered into the tools array like
  built-ins. This suits the few tools an agent uses constantly, at the price of
  prefix churn only when the user edits the definition.
- Resources and prompts: `plugin_find` lists them, and
  `plugin_call {plugin, resource: uri}` reads one. Resource subscriptions feed
  triggers (`scheduler.md`).

### Permissions

The defaults come from MCP tool annotations, which `sentry.md` 3 maps to effects.
The Plugins page shows, per tool, the effect and the current verdict, with
allow, ask, deny and hand-off controls. These are Sentry rules underneath.
Marking a plugin **private** makes its reads set label B. Marking it **trusted**
skips the injection screen for its results; use this only for servers you run.

### Result filtering

Every result goes through Sentry ingest (`sentry.md` 6): secret redaction,
one-time-code removal (especially mail and messaging plugins), the injection
screen and wrapping. Images and audio in results keep today's handling: kept in
details, replaced in the model's text. Images can now reach a vision model
through the screenshot path (`runtime.md` 5).

### Elicitation and OAuth

Elicitation requests from a plugin become Inbox items, using today's form and
URL-mode UI, moved to the Inbox. Requests the plugin makes while a task runs
park the task. OAuth sign-in happens from the Plugins page. The redirect lands on
Core's UI route and posts to Sentry through `/v1/user/*`, and tokens are sealed
in the vault.

### Health

Plugin state (`connecting`, `ready`, `auth_required`, `failed`) comes from Sentry's
event stream to the UI, the way `mcp.server` events work today. A plugin that
fails during a session gives a normal tool error. The catalog line stays, since
the prefix must not change. The error says the plugin is unavailable.
