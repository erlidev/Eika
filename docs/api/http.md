# HTTP API

The harness serves one JSON API under `/api`. Bodies are JSON with
`snake_case` fields, times are RFC 3339 in UTC, and identifiers are the 20
character base32 strings `store.NewID` produces. The Go wire types live in
`internal/server`, one file per resource; the TypeScript mirrors live in
`web/src/api/`.

## Authentication

Every route under `/api` requires a bearer token:

```
Authorization: Bearer <token>
```

The token is a sign-in session's, from the setup, sign-in, or password
routes below, or the deployment's optional fixed API token, `EIKA_AUTH_TOKEN`.
The exceptions are `GET /healthz`, `GET /api/healthz`, the three routes that
hand out a session (`GET /api/auth/status`, `POST /api/auth/setup`,
`POST /api/auth/login`), `GET /oauth/client-metadata.json`, which an OAuth
authorization server fetches, and the git hub under `/git/`, which
authenticates workspaces itself with per-workspace basic auth. The two WebSocket routes,
`GET /api/events` and `GET /api/workspaces/{id}/terminal`, also accept the
token as the `token` query parameter, because a browser cannot set a header on
a WebSocket handshake; no other route does, so a token never has to appear in
an ordinary URL.

A wrong, missing, or expired token is `401` with a `WWW-Authenticate: Bearer`
header.

## Errors

Every failure has one shape:

```json
{"error": {"code": "not_found", "message": "read project p1: not found"}}
```

| Code | Status | Meaning |
|---|---|---|
| `invalid_request` | 400 | The request was malformed, missing a field, or named something the API will not accept. An unknown JSON field is malformed. |
| `unauthorized` | 401 | The bearer token was missing, wrong, or expired, or a sign-in password was wrong. |
| `forbidden` | 403 | A workspace path is outside the workspace, directly or through a symlink, or the sandbox may not open it. |
| `not_found` | 404 | The addressed project, workspace, session, entry, run, question, provider, model, MCP server, elicitation, pending authorization, or workspace file does not exist. |
| `conflict` | 409 | The request collides with the current state: a duplicate name, a second run on a session, a workspace that is not running, nothing to commit, a harness already set up, no model to run on, or a stored credential the harness can no longer open. |
| `too_large` | 413 | A file saved through the API is over its 2 MiB bound. |
| `internal` | 500 | The harness failed. The message is always `internal error`; the detail is in the harness log. |

## Health

| Method | Path | Response |
|---|---|---|
| GET | `/healthz` | `{"status":"ok"}`. The container health check. |
| GET | `/api/healthz` | The same, under the prefix the frontend uses. |

## Sign-in

Eika has one user and one password, chosen in the guided setup. Signing in
exchanges it for a session token that lasts 30 days.

### `GET /api/auth/status`

No token needed. `200` with `{"password_set": boolean}`: false until setup has
chosen the password, which is how the UI decides between setup and sign-in.

### `POST /api/auth/setup`

No token needed. `{"password": string}`, at least 8 characters. Sets the
password of a harness that has none and signs in. `201` with a `SignIn`. `409`
once a password exists, so setup can be claimed once.

### `POST /api/auth/login`

No token needed. `{"password": string}`. `200` with a `SignIn`; `401` for a
wrong password; `409` before setup. Attempts are checked one at a time.

### `POST /api/auth/logout`

Ends the session whose token the request carries. The API token is not a
session and stays valid. `204`.

### `PUT /api/auth/password`

`{"current_password": string, "new_password": string}`. Replaces the password,
ends every session, and signs this browser in again: `200` with a `SignIn`.
`400` when the current password is wrong or the new one is too short.

### SignIn

| Field | Type | Meaning |
|---|---|---|
| `token` | string | The bearer token to send from now on. |
| `expires_at` | time | When it stops working. |

## Projects

A project is a git repository Eika knows. Every project gets a bare repository
in the hub; a `remote` project is mirrored from its remote, and a `local`
project also has a host directory its workspaces bind-mount.

### `GET /api/projects`

`200` with `{"projects": [Project]}`, newest first.

### `POST /api/projects`

| Field | Type | Meaning |
|---|---|---|
| `name` | string, required | The project name. It is also the hub repository's directory, so it may not contain a slash or `..`. |
| `kind` | `remote` or `local`, required | Where the code comes from. |
| `remote_url` | string | Required for `remote`. The upstream the hub mirrors. |
| `remote_username` | string | Optional for `remote`. The user the hub authenticates to the upstream as; for a token, any placeholder such as `x-access-token`. Set it with `remote_password`. |
| `remote_password` | string | Optional for `remote`. The upstream password or access token. It is encrypted at rest and never returned. Set it with `remote_username`. |
| `host_path` | string | Required for `local`. An absolute path on the Docker host, bind-mounted at the workspace root. |
| `default_branch` | string | The branch a workspace uses when it names none. Defaults to `main`. |

`remote_url` must not contain userinfo, a query string, or a fragment. These
URL parts can expose credentials in stored data, API responses, logs, and Git
arguments. A private HTTPS remote sends its username and password or token in
their own fields; a public remote omits both.

`201` with the `Project`. `400` for a missing or malformed field, an incomplete
credential pair, and a remote the hub cannot mirror; `409` when the name is
taken.

### `GET /api/projects/{id}`

`200` with the `Project`, `404` when there is none.

### `PATCH /api/projects/{id}`

| Field | Type | Meaning |
|---|---|---|
| `remote_username` | string | Replaces the upstream username. |
| `remote_password` | string | Replaces the upstream password; the empty string removes the credentials, username included. |
| `default_branch` | string | Replaces the branch new workspaces use. |

An absent field is left alone. Where the code comes from does not change.
New credentials fetch from the remote before they are stored, so a token the
remote refuses is `400` and changes nothing. Credentials on a local project
are `400`. `200` with the `Project`.

### `DELETE /api/projects/{id}`

Destroys every workspace of the project, their containers and volumes, and
deletes the project with its sessions and entries. The hub repository stays:
it holds the branches the workspaces pushed. `204`.

### Project

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The project id. |
| `name` | string | The project name, unique. |
| `kind` | `remote` or `local` | Where the code comes from. |
| `remote_url` | string, optional | The upstream, for a remote project. |
| `remote_username` | string, optional | The upstream username of a private remote. |
| `remote_password_set` | boolean, optional | A password is stored. The password itself is never returned. |
| `host_path` | string, optional | The bind-mounted directory, for a local project. |
| `default_branch` | string | The branch new workspaces use. |
| `created_at` | time | When it was registered. |

## Workspaces

A workspace is one sandbox container holding a checkout of a project.

### `GET /api/workspaces`

`?project_id=<id>` narrows the list to one project. `200` with
`{"workspaces": [Workspace]}`.

### `POST /api/workspaces`

Creates the container, starts it, puts the project's code in it, and records
the commit it starts from. A failure at any step destroys what was created, so
a rejected request leaves no container and writes no row.

| Field | Type | Meaning |
|---|---|---|
| `project_id` | string, required | The project to check out. |
| `name` | string, required | What the user calls this workspace. |
| `branch` | string | The branch to work on. Defaults to the project's `default_branch`. |
| `image` | string | The container image. Defaults to the `sandbox_image` setting, or the deployment's `eika-sandbox:latest`. Ignored when `build_context` is set. |
| `build_context` | string | A directory on the Docker host holding a Dockerfile and its context; the image is built from it first. |
| `dockerfile` | string | The Dockerfile's name within `build_context`. Defaults to `Dockerfile`. |
| `parent_workspace_id` | string | The workspace this one was branched from, recorded for the UI. |

`201` with the `Workspace`.

### `GET /api/workspaces/{id}`

`200` with the `Workspace`.

### `POST /api/workspaces/{id}/start`

Starts a stopped container again. `200` with the `Workspace`.

### `POST /api/workspaces/{id}/stop`

Aborts every run in the workspace, then stops the container and keeps its
files. `200` with the `Workspace`.

### `DELETE /api/workspaces/{id}`

Aborts every run in the workspace, destroys the container and its volume, and
deletes the row with its sessions and entries. A container that is already
gone is not an error. `204`.

### `GET /api/workspaces/{id}/diff`

Runs `git diff <base_commit>` and `git status --porcelain` inside the
workspace through its executor. `?path=<path>` narrows both to one file or
directory; a path outside the workspace is `403`.

`200` with:

| Field | Type | Meaning |
|---|---|---|
| `workspace_id` | string | The workspace. |
| `base_commit` | string, optional | The commit the workspace started from. |
| `diff` | string | `git diff` against that commit. Empty when nothing tracked changed. |
| `status` | string | `git status --porcelain`, which is what shows untracked files. |

`409` when the workspace is not running; `400` when git itself failed, with
git's message.

### `POST /api/workspaces/{id}/merge`

Brings another workspace's branch into this one, which is how a parent takes
a subagent's work. The branch travels through the hub: a running source
workspace pushes first, the target fetches, and git merges or rebases inside
the target workspace through its executor.

| Field | Type | Meaning |
|---|---|---|
| `source_workspace_id` | string | The workspace whose branch to bring in. When it is running it pushes its own branch to the hub first. |
| `branch` | string | The branch in the hub to merge, which overrides the source's own. Required when there is no source workspace. |
| `strategy` | `merge` or `rebase` | Empty means `merge`. |

One of `source_workspace_id` and `branch` is required.

`200` with:

| Field | Type | Meaning |
|---|---|---|
| `workspace_id` | string | The target workspace. |
| `branch` | string | The branch that was brought in. |
| `strategy` | string | `merge` or `rebase`. |
| `merged` | boolean | False means git stopped on conflicts. |
| `commit` | string, optional | The target's HEAD after a successful merge. |
| `conflicts` | string array | The paths git could not resolve. Empty on success. |
| `message` | string | git's own output. |

A conflict is a `200`, not an error: the target's tree is left conflicted on
purpose, so the user or the agent working in it resolves it there. `409` when
the target workspace is not running; `400` when the branch is not in the hub
or the source belongs to another project.

### `POST /api/workspaces/{id}/commit`

Stages the workspace's changes with `git add -A` and commits them, inside the
workspace through its executor. A repository without an identity of its own
commits as `Eika Agent <agent@eika.local>`.

| Field | Type | Meaning |
|---|---|---|
| `message` | string, required | The commit message. |
| `paths` | string array | Commit only these workspace paths. Absent or empty commits every change. |

`200` with `{"commit": string}`, the new commit. `400` for an empty message or
when git itself failed, with git's message; `403` for a path outside the
workspace; `409` with the message `nothing to commit` when nothing is staged,
and when the workspace is not running. Publishes `workspace.state`.

### `POST /api/workspaces/{id}/push`

Pushes the workspace's HEAD to its branch in the hub and, when asked, the
hub's branch on to the project's remote with the project's credentials. The
push never forces.

| Field | Type | Meaning |
|---|---|---|
| `upstream` | boolean | Also push the branch from the hub to the project's `remote_url`. |

`200` with:

| Field | Type | Meaning |
|---|---|---|
| `branch` | string | The workspace's branch, which is the branch pushed. |
| `commit` | string | The commit pushed. |
| `upstream_pushed` | boolean | Whether the branch also went to the remote. |

`400` when the hub or the remote refused the push, with git's message and no
credentials, and when `upstream` is set for a project without a remote; `409`
when the workspace is not running. Publishes `workspace.state`. A client
builds a link to open a pull request from the project's `remote_url` and
`branch`; the harness has no forge API.

### `GET /api/workspaces/{id}/files`

Lists one directory of the workspace: `?path=<path>`, relative to the
workspace root, and absent or empty for the root. Directories come first,
then files, each in byte order of their names.

`200` with `{"entries": [FileEntry]}`. `400` when the path is a file; `403`
when it is outside the workspace; `404` when it does not exist; `409` when the
workspace is not running.

### `GET /api/workspaces/{id}/file`

Reads one file for the editor. `?path=<path>` is required.

`200` with:

| Field | Type | Meaning |
|---|---|---|
| `path` | string | The file, relative to the workspace root. |
| `size` | number | Its size in bytes. |
| `binary` | boolean | A NUL byte is in its first 8 KiB. `content` is then empty. |
| `too_large` | boolean | It is over 2 MiB. `content` is then empty. |
| `content` | string | The file's text. |

`400` when the path is missing or a directory; `403`, `404`, and `409` as for
the listing.

### `PUT /api/workspaces/{id}/file`

Saves the raw request body as one file, `?path=<path>`, creating its parent
directories. The body is at most 2 MiB.

`200` with the saved file's `FileEntry`. `400` when the path is missing or a
directory; `403` when it is outside the workspace; `409` when the workspace is
not running; `413` when the body is over the bound. Publishes
`workspace.state`.

### `GET /api/workspaces/{id}/terminal`

Upgrades to a WebSocket carrying an interactive shell in the workspace.
`?rows=` and `?cols=` set the starting size; absent means 24 by 80. The
harness dials the sandbox's `/pty` and relays messages both ways unchanged, so
they are exactly the PTY messages in `eikad.md`: the client sends
`{"type":"input","data":<base64>}` and `{"type":"resize","rows":n,"cols":n}`,
and receives `{"type":"output","data":<base64>}` and, when the shell exits,
`{"type":"exit","exit_code":n}` (absent `exit_code` means 0) followed by a
normal close. A close from either side is passed on with its status and
reason; a lost connection closes the other side with `1011`.

The token may be sent as `?token=`, and the handshake is accepted from the
same origins as the event stream. Before the upgrade, `400` for a size that is
not a number from 0 to 65535, `404` for a missing workspace, and `409` for a
workspace that is not running.

### FileEntry

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The base name. |
| `path` | string | Relative to the workspace root, slash-separated. |
| `size` | number | Size in bytes. |
| `mode` | number | Go file mode bits. |
| `mod_time` | time | Last modification. |
| `is_dir` | boolean | Whether it is a directory. |

### Workspace

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The workspace id. It names the container and the volume as well. |
| `project_id` | string | The project it holds. |
| `name` | string | What the user calls it. |
| `branch` | string | The branch it works on. |
| `base_commit` | string, optional | The commit it started from. A diff and a fork are measured against it. |
| `image` | string | The image the container runs. |
| `state` | string | `creating`, `running`, `stopped`, or `gone`. |
| `container_id` | string, optional | The Docker container id. |
| `parent_workspace_id` | string, optional | The workspace it was branched from. |
| `created_at`, `updated_at` | time | When it was made and last changed. |

## Sessions

A session is a tree of entries in one workspace, with a head the next run
continues from. A chat is a session with no workspace: its runs have no files
or commands, and offer the model only the tools that need no workspace.

### `GET /api/sessions`

`?workspace_id=<id>` narrows the list. `?descendants=true` adds the forks and
child agents those sessions led to, wherever they run: a fork with a workspace
and a subagent each live in a workspace of their own, and both belong under
the session they came from. It is ignored without `workspace_id`, which
already returns every session, chats included. `?chats=true` returns the
chats alone, forks of chats included; with `workspace_id` it is `400`.

`200` with `{"sessions": [Session]}`, oldest first. The rows are flat; a
client hangs each one under its `parent_session_id`.

### `POST /api/sessions`

| Field | Type | Meaning |
|---|---|---|
| `workspace_id` | string | The workspace the session's runs act in. Required unless `chat` is set. |
| `chat` | boolean | Open a chat, which has no workspace. |
| `title` | string, required | What to call it. |

`201` with the `Session`. `400` for an empty title or for `chat` with a
`workspace_id`; `404` when the workspace does not exist, which is also what
leaving both out gives: a chat is asked for, never a fallback.

### `GET /api/sessions/{id}`

`200` with `{"session": Session, "head": Entry | absent}`.

### `DELETE /api/sessions/{id}`

Aborts the session's run, then deletes it and its entries. `204`.

### `GET /api/sessions/{id}/outline`

Every entry of the tree, without payloads: what the session tree panel draws.

`200` with `{"session_id": string, "head_entry_id": string, "nodes": [Node]}`,
where a `Node` is
`{id, parent_id?, kind, preview, commit?, resumable, created_at}`.

`resumable` is whether a run can continue from that entry: the path down to it
leaves no tool call unanswered. It is false for an assistant entry whose tool
calls are still out and for a tool result that is not the last of its set. The
head and fork routes refuse those entries, so a client offers no branch from
a node that is not resumable.

### `GET /api/sessions/{id}/path`

The branch from the root to the head, which is the conversation the model
sees.

`200` with `{"session_id": string, "entries": [Entry], "messages": [Message]}`.
`messages` holds the provider messages of the entries that carry one, in the
same order.

### `POST /api/sessions/{id}/head`

`{"entry_id": string}`. Moves the head to one of the session's own entries, so
the next run continues from there and the tree branches in place. An empty
`entry_id` clears the head, so the session starts again from its root.

`200` with the `Session`. `409` while a run is going; `404` when the entry is
not the session's; `400` when the entry is not `resumable`, because a path
that stops with tool calls unanswered is a conversation no endpoint accepts.

### `POST /api/sessions/{id}/fork`

| Field | Type | Meaning |
|---|---|---|
| `entry_id` | string, required | The entry to fork at. |
| `title` | string | What to call the fork. |
| `with_workspace` | boolean | Give the fork a workspace of its own, rewound to the entry's commit. |

Copies the path from the root down to the entry into a session of its own,
sharing no rows. Without `with_workspace` the fork stays in the same
workspace, so only the conversation rewinds.

With `with_workspace` the source workspace pushes its branch to the hub, a new
workspace in the same project is created and cloned from the hub at the
commit that entry recorded, on branch `<source branch>-fork-<8 chars of the
new id>`, and the fork points at it. The files rewind with the conversation.

`201` with the new `Session`, whose `kind` is `fork` and whose
`parent_session_id` is the source.

A fork keeps its source's tools, and a fork of a chat is a chat.

`400` when `entry_id` is missing, when the entry is not `resumable`, or, with
`with_workspace`, when the source is a chat or the entry recorded no commit
and there is nothing to clone; `409` when the source workspace is not
running. The resumability check
runs before anything is created, so a refused fork leaves no workspace
behind.

### `PUT /api/sessions/{id}/tools`

`{"tools": [string] | null}`, required; an empty list turns every tool off,
and `null` clears the session's own choice, so that its profile's applies.
Chooses the tools the session's next run offers the model; a run already
going keeps the ones it started with. An entry is a tool name or
`mcp__<server>__*`, every tool that MCP server offers, including one it adds
later. The entries are stored sorted, once each.

`200` with the `Session`. `400` when `tools` is missing or not a list, names
a tool or an MCP server that does not exist, or, for a chat, names a tool or
a stdio server that needs a workspace.

### `GET /api/sessions/{id}/configuration`

What the session sets for itself over its profile, and what its next run
resolves to. `200` with:

| Field | Type | Meaning |
|---|---|---|
| `session_id` | string | The session. |
| `profile_id` | string, optional | The profile the session chose. Absent, it runs with whichever profile is the default. |
| `overrides` | ProfileSettings | What the session sets over its profile. |
| `tools` | string array or null | The session's own tool choice, null when it has made none. |
| `resolved` | Configuration | What the next run uses when the message names no model. |
| `inherited` | Configuration | What the session's overrides fall through to: the configuration as it would be if the session set nothing itself, except that a model the session chose still supplies the model row below its unset values. |

An editor asks with what it has chosen and not saved: `?profile_id=` and
`?model_id=` stand in for the session's profile (empty: the default one)
and its own model (empty: none) in `inherited`, so the editor shows the
values they bring as they are chosen. Everything else in the answer is
what is saved. `400` for a profile or a model that does not exist.

### `PUT /api/sessions/{id}/profile`

`{"profile_id": string}`; empty goes back to the default profile. The
session's overrides stay. `200` with the body of
`GET /api/sessions/{id}/configuration`; `400` for a profile that does not
exist.

### `PUT /api/sessions/{id}/overrides`

A ProfileSettings object, which replaces everything the session sets over
its profile; one that sets nothing clears them. `200` with the body of
`GET /api/sessions/{id}/configuration`; `400` for a model that does not
exist, a sampling parameter out of range, or a prompt over 64 KiB.

### `GET /api/sessions/{id}/context`

The session's next model request, assembled by the same code a run uses:
the request a run started now would send, with the run's new message still
to be appended. `?model=<name>` names the model the run would request, as a
message does. The MCP tools are those the servers last listed; a preview
connects nothing. The context files are read in the workspace as a run
reads them; when it is not running, the preview has everything else and
`context_files_unread` says why the files are missing, since a run would
start the workspace and read them. `200` with a ModelContext. `400` for a
model that does not exist.

### `GET /api/sessions/{id}/requests`

The records of the session's model calls: one per call a run made that
returned a response. `200` with `{"session_id": string, "requests":
[ModelRequest]}`, oldest first. The records go with their session.

### `GET /api/sessions/{id}/requests/{request_id}`

One recorded call, in the shape of the preview: its sections, tool schemas,
and parameters as it sent them, and its messages rebuilt from the session's
path down to the entry the call's conversation ended at. `200` with a
ModelContext that has `request`; `404` for a record of another session.

### ModelContext

| Field | Type | Meaning |
|---|---|---|
| `sections` | array | The system prompt by section, in order: `{kind, text, tokens, files}`. `kind` is `base`, `context_files`, or `instructions`; `files` lists a `context_files` section's files as `{path, text, tokens}`. The system prompt is the sections' texts joined by a blank line. |
| `tools` | array | The tool schemas sent: `{name, description, schema, source, tokens}`, where `source` is `builtin` or `mcp`. |
| `messages` | array | The conversation as the provider receives it: no `metrics`, and `reasoning` only when the model preserves thinking. |
| `message_tokens` | number | The estimated size of the messages. |
| `parameters` | object | `{model, sampling, thinking_switch, preserve_thinking}`: the endpoint's identifier of the model, the sampling parameters sent (a Sampling), and the model's switches. |
| `sources` | object | The layer each parameter came from, keyed `model`, `thinking_switch`, `preserve_thinking`, and `sampling.<parameter>`. |
| `dropped_effort` | string, optional | A reasoning effort the configuration chose that the model does not offer, which is not sent. |
| `context_files_unread` | string, optional | In a preview, why the context files a run would read are missing: the workspace is not running. |
| `request` | ModelRequest, optional | The record this is; absent for the next request. |
| `calibration` | object, optional | `{request_id, input_tokens, estimated_tokens}`: a call the endpoint measured, beside the estimate of what it sent, so the estimates can be scaled to it. The record itself, or for the next request the session's last measured call. |

Every `tokens` figure is an estimate, four bytes to a token; only a record's
`input_tokens` is measured.

### ModelRequest

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The record id. |
| `session_id` | string | The session. |
| `run_id` | string | The run that made the call: a `Run`'s id. |
| `entry_id` | string, optional | The session entry the call's conversation ended at. |
| `model_id` | string, optional | The model, absent once it is deleted. |
| `model` | string | The model's name when the call was made. |
| `message_tokens` | number | The estimated size of the messages sent. |
| `input_tokens`, `output_tokens`, `total_tokens` | number | What the endpoint measured for the call, 0 when it reported nothing. |
| `created_at` | time | When the call returned. |

### Session

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The session id. |
| `workspace_id` | string, optional | Where its runs act. Absent for a chat. |
| `title` | string | What the user calls it. |
| `kind` | string | `user`, `fork`, or `agent`: who opened it. |
| `head_entry_id` | string, optional | The entry the next run continues from. |
| `parent_session_id` | string, optional | The session it was forked from, or the one whose run spawned it. |
| `tools` | string array | The tools the next run offers the model, sorted: what the session's tool choice, or its profile's, takes, every tool the session can run when neither chose, and never one that needs a workspace in a chat. |
| `profile_id` | string, optional | The profile the session chose; absent, it runs with the default profile. A fork and a child agent start with their parent's. |
| `overridden` | boolean | The session sets something of its own over its profile: overrides or a tool choice. |
| `created_at`, `updated_at` | time | When it was made and last changed. |

### Entry

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The entry id. |
| `parent_id` | string, optional | The entry it follows. |
| `seq` | number | Write order within the session. |
| `kind` | string | `user`, `assistant`, `tool_call`, `tool_result`, `system`, or `event`. |
| `commit` | string, optional | The workspace HEAD commit the entry was produced at. |
| `created_at` | time | When it was written. |
| `message` | object | The provider message the entry holds. Empty for kinds that hold no message. |

A provider message can include `reasoning` on an assistant entry whenever the
endpoint streamed it. The session view shows it apart from the answer; Eika
replays it to the provider only when `preserve_thinking` is on. An assistant
message can also include `metrics` with `run_id`, turn `usage`, last-call
`context`, `generation_ms`, `context_window`, and an optional `timings`
(`docs/api/events.md`). This UI metadata restores the context view and the
answer's generation speed after replay, and is never sent to the provider. A tool call's
`arguments` is normally an object. If a model returns malformed JSON, it is a
string with the exact malformed text, and the tool call has
`arguments_malformed: true`. The marker is absent for valid JSON, including a
valid top-level JSON string.

## Runs, queues, and questions

One session runs at most one agent run at a time. A run outlives the request
that starts it: the response says it began and everything after that arrives
on the event stream.

### `POST /api/sessions/{id}/messages`

| Field | Type | Meaning |
|---|---|---|
| `text` | string, required | The message. |
| `mode` | `run`, `steer`, or `follow_up` | What to do with it. Empty means `run`. |
| `model` | string | The name of the model this run uses. Empty uses the model the session's configuration resolves to: its overrides', its profile's, or the `default_model` setting's, or the first model. |

- `run` starts a run from the session's head. `409` when one is already going.
- `steer` joins the run in progress as soon as the running tool call
  finishes, before the next model call.
- `follow_up` waits until the current turn ends and then starts the next one.
- `steer` and `follow_up` are `409` when the session has no run in progress.

`202` with the `Run`. `400` for empty text, an unknown mode, or a model that
does not exist; `409` when the session's workspace is not running or no model
is configured. A chat has no workspace to be stopped, so it runs whenever a
model is configured.

### `GET /api/sessions/{id}/run`

What the session is doing and what is waiting for it.

| Field | Type | Meaning |
|---|---|---|
| `session_id` | string | The session. |
| `active` | boolean | A run is going right now. When false, `run` is the last one that finished, if there was one. |
| `run` | Run, optional | The run. |
| `pending_steering` | string array | Steering messages the run has not delivered yet, oldest first. |
| `pending_follow_ups` | string array | Follow-up messages waiting for the turn to end. |
| `questions` | Question array | Questions of this session that a run is blocked on. |
| `elicitations` | Elicitation array | What MCP servers asked the user during this session's tool calls, which wait on an answer, oldest first. |

Accepted queue messages remain in these arrays after a run aborts or fails.
The next run on the session receives them.

### `POST /api/runs/{id}/abort`

Cancels the run and waits for its goroutine to stop. `200` with the final
`Run`. A run row left `running` by a harness restart is recorded as aborted;
a run that already finished is `409`; an unknown run is `404`.

### `POST /api/questions/{id}/answer`

`{"answer": string}`. Delivers the answer to the `ask_user` call that is
waiting for it; the run continues at once. `204`.

`404` when no run waits on that question, which is also what an already
answered or abandoned question gives. `400` when the answer is empty, or is
not one of the question's options and the question does not allow free text.

### `GET /api/tools`

Every tool a run can offer the model, sorted by name: what a session's tool
choice picks from.

`200` with `{"tools": [Tool]}`, where a `Tool` is:

| Field | Type | Meaning |
|---|---|---|
| `name` | string | What the model calls it, and what `PUT /api/sessions/{id}/tools` takes. |
| `description` | string | What the model is told it does. |
| `needs_workspace` | boolean | It acts on files or processes, so a chat never offers it. A `stdio` MCP server's tools do. |
| `server` | string, optional | The MCP server whose tool it is. Absent for a built-in tool and for `mcp_list_resources` and `mcp_read_resource`, which reach every server of a run. |

The list includes the tools each enabled MCP server offered when it was last
listed, without connecting to any, and the two resource tools when one of
them has resources. A run offers the tools of the servers it reaches when it
starts: remote ones, and the stdio ones in its workspace, which it starts. A
server not connected yet has 15 seconds; one that cannot be reached leaves
its tools out of that run.

### Run

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The run id. It is not the `run_id` on the stream: one run has one turn per model exchange, and each turn has its own `run_id`. |
| `session_id` | string | The session it works on. |
| `state` | string | `running`, `done`, `error`, or `aborted`. |
| `started_at` | time | When it began. |
| `finished_at` | time, optional | When it ended. |
| `error` | string, optional | Why an `error` run failed. |

### Elicitation

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The elicitation id, which the answer route takes. |
| `session_id`, `run_id`, `call_id` | string | The tool call that waits. |
| `server` | string | The name of the MCP server that asks. |
| `mode` | string | `form`, for fields to fill in, or `url`, for a page to open. |
| `message` | string | What the server says it wants. |
| `requested_schema` | object, optional | In `form` mode, a flat JSON Schema object: its `properties` are `string` (perhaps with `enum`, `format`, `minLength`, `maxLength`), `number`, `integer`, `boolean`, or `array` of strings, and `required` names the ones that must be given. |
| `url` | string, optional | In `url` mode, the http or https page to send the user to; accept once they have been. |
| `asked_at` | time | When the server asked. |

### Question

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The question id, which the answer route takes. |
| `session_id`, `run_id`, `call_id` | string | Where it came from. |
| `question` | string | What to ask the user. |
| `options` | string array, optional | The answers to choose from. |
| `allow_free_text` | boolean | An answer outside the options is accepted. |
| `asked_at` | time | When the run asked. |

## Subagents

A subagent is a child agent run: its own workspace, cloned from its parent's
at the commit the parent stood on and running its parent's image, its own
session, and its own branch, `<parent branch>-<name>-<6 characters of the
subagent id>`. The `spawn_agent`, `wait_agents`, and `list_agents` tools are
how a run makes and waits for them; these routes are how the UI watches and
stops them. The lifecycle is on the stream as `subagent.started` and
`subagent.finished`.

The `subagent_max_depth` and `subagent_max_children` settings bound how deep
and how wide the tree may grow; the defaults are 2 and 4, and a change applies
to the next spawn.

### `GET /api/sessions/{id}/agents`

The children this session spawned, and below each of them the children they
spawned in turn, oldest first.

`200` with `{"session_id": string, "agents": [Agent]}`.

### `POST /api/subagents/{id}/abort`

Stops a running child and waits for it to record how it ended. The child still
commits and pushes what it left in its tree, so aborted work is not lost, and
its workspace is stopped rather than destroyed.

`200` with the `Agent`. `409` when the child is already finished; `404` when
there is no such subagent.

### Agent

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The subagent id, which the abort route takes. |
| `parent_session_id` | string | The session that spawned it. |
| `session_id` | string | The child's session. Open it to watch the child work. |
| `workspace_id` | string | The child's workspace. |
| `name` | string | What the parent called it. |
| `branch` | string | The branch it works on and pushes to the hub. |
| `state` | string | `running`, `done`, `error`, or `aborted`. |
| `created_at` | time | When it was spawned. |
| `finished_at` | time, optional | When it ended. |
| `result` | `AgentResult`, optional | What it reported. Absent while it is running. |
| `agents` | `[Agent]` | The children it spawned in turn. Empty for a leaf. |

### AgentResult

The same object the parent's model receives as the `spawn_agent` tool result.

| Field | Type | Meaning |
|---|---|---|
| `id`, `session_id`, `workspace_id` | string | The child. |
| `name` | string | What the parent called it. |
| `branch` | string | The branch it pushed to the hub. |
| `state` | string | `running`, `done`, `error`, or `aborted`. |
| `commit` | string, optional | Its head commit after it committed its tree. |
| `summary` | string, optional | Its final assistant message. |
| `diff_stat` | string, optional | `git diff --stat` from the parent's base commit to that head. |
| `error` | string, optional | Why a child that did not finish cleanly stopped. |

## Providers

A provider is an OpenAI-compatible endpoint and the key it takes. Its
models are listed separately.

### `GET /api/providers`

`200` with `{"providers": [Provider], "kinds": [string]}`, oldest first.
`kinds` are the provider kinds this harness can talk to: `openai` today.

### `POST /api/providers`

| Field | Type | Meaning |
|---|---|---|
| `name` | string, required | What the user calls it; unique. |
| `kind` | string | One of `kinds`. Defaults to `openai`. |
| `base_url` | string, required | An http or https API root, such as `https://api.openai.com/v1`. No credentials, query string, or fragment. |
| `api_key` | string | The key. Encrypted at rest and never returned. Empty for an endpoint that asks for none. |

`201` with the `Provider`; `400` for a bad field; `409` when the name is taken.

### `PATCH /api/providers/{id}`

`name`, `base_url`, and `api_key`, each optional; an absent field is left
alone and an empty `api_key` removes the key. A stored key belongs to the
base URL it was entered for: a `base_url` that differs from the stored one,
without an `api_key`, clears the stored key, so it is never sent to the new
endpoint. `200` with the `Provider`; `400` for a bad field; `409` when the
name is taken.

### `DELETE /api/providers/{id}`

Removes the provider and its models. A run already going keeps the client it
built. `204`.

### `POST /api/providers/probe`

Asks an endpoint which models it serves: the setup screens' connection test
and model list.

| Field | Type | Meaning |
|---|---|---|
| `provider_id` | string | A stored provider to probe with its stored key. |
| `kind` | string | Without `provider_id`, the kind of the endpoint. Defaults to `openai`. |
| `base_url` | string | Overrides or, without `provider_id`, names the endpoint. A stored key is used only with the stored base URL; with another one, the probe carries `api_key` or no key. |
| `api_key` | string | Overrides or, without `provider_id`, gives the key. |

`200` with `{"models": [ModelInfo]}`, sorted by id. `400` with what the
endpoint answered, the key removed, when it could not be used; a connection
to a loopback address that fails says to use `host.docker.internal`, because
the harness runs in a container. `400` too for a kind that cannot list.

### Provider

| Field | Type | Meaning |
|---|---|---|
| `id`, `name`, `kind`, `base_url` | string | The provider. |
| `api_key_set` | boolean | A key is stored. |
| `api_key_hint` | string, optional | The last four characters of a key of 16 or more. |
| `created_at`, `updated_at` | time | When it was made and last changed. |

### ModelInfo

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The endpoint's identifier for the model. |
| `context_window` | number, optional | The context size the endpoint reports. |
| `max_output` | number, optional | The output limit the endpoint reports. |

## Models

A model is one model of a provider that runs may use.

### `GET /api/models`

`200` with `{"models": [Model], "default": string}`, oldest first. `default`
is the model a run uses when it names none: the `default_model` setting when
it names a model that exists, the first model otherwise, absent when there are
none.

### `POST /api/models`

| Field | Type | Meaning |
|---|---|---|
| `provider_id` | string, required | The provider it belongs to. |
| `model` | string, required | The endpoint's identifier, such as `gpt-5`. |
| `name` | string | What Eika, its users, and `spawn_agent` call it; unique across providers. Defaults to `model`. |
| `context_window` | number, required | The total token budget. A run refuses a request that cannot fit. |
| `max_output` | number, required | The most tokens one response may have; at most `context_window`. |
| `reasoning_effort` | string | The `reasoning_effort` in force. Compatible endpoints disagree on the vocabulary, so any word of at most 32 letters, digits, hyphens, and underscores is accepted; empty leaves it to the endpoint. |
| `reasoning_efforts` | string array | The efforts this model offers, in the order the UI cycles through them. At most 12, each a non-empty value of the shape above. |
| `thinking_switch` | string | The request field that turns thinking off when the effort is `none`, and the only one sent: `reasoning_effort` (the default) sends `"reasoning_effort": "none"`; `chat_template_kwargs` sends `"chat_template_kwargs": {"enable_thinking": false, "thinking": false}` for servers that render the model's chat template (vLLM, SGLang, llama.cpp); `thinking` sends `"thinking": {"type": "disabled"}` (DeepSeek, Z.ai, Moonshot, Anthropic). Any other effort goes in `reasoning_effort` whatever this says. |
| `preserve_thinking` | boolean | Ask a compatible endpoint for `reasoning_content` and replay it on later turns. Off by default; the official OpenAI API rejects it. Reasoning is streamed to clients either way. |

`201` with the `Model`; `400` for a bad field or an unknown provider; `409`
when the name is taken.

### `PATCH /api/models/{id}`

Any field of `POST` but `provider_id`; an absent field is left alone.
Renaming the default model keeps it the default. `200` with the `Model`.

### `DELETE /api/models/{id}`

`204`. When it was the default, the first remaining model is the default until
the user picks another.

### `POST /api/models/test`

`{"provider_id": string, "model": string, "reasoning_effort": string,
"thinking_switch": string, "preserve_thinking": boolean}`: a model on a stored provider, saved or not.
Sends one short request with no tools and answers `200` with
`{"reply": string, "stop_reason": string, "latency_ms": number}`. A reasoning
model that spends its budget thinking replies with nothing, which still shows
the model is there. `400` with what the endpoint answered when it failed.

### Model

| Field | Type | Meaning |
|---|---|---|
| `id`, `provider_id`, `name`, `model` | string | The model, its provider, Eika's name for it, and the endpoint's. |
| `context_window`, `max_output` | number | Its limits. |
| `reasoning_effort` | string, optional | As on `POST`. |
| `reasoning_efforts` | string array | As on `POST`; always present, empty when the model offers no choices. |
| `thinking_switch` | string | As on `POST`; always present. |
| `preserve_thinking` | boolean | As on `POST`. |
| `created_at`, `updated_at` | time | When it was made and last changed. |

## Profiles

A profile is a named configuration of what a run sends: the model, the base
prompts, extra instructions, whether context files are read, the tools, and
the sampling parameters. Every setting is optional, and one that is not set
falls through. A run's configuration is resolved one value at a time, from
the first layer that sets it:

```
the message's model -> the session's overrides -> the profile -> the model row -> the default
```

The model row supplies `max_output` and `reasoning_effort`; the default is
the `default_model` setting, the built-in prompts, context files read, every
tool, and for any other sampling parameter the endpoint's own default. A
reasoning effort chosen above the model row that the model does not offer is
not sent: the model row's own is, and the configuration names the dropped
one. The harness creates one profile, `Default`, that sets nothing.

### `GET /api/profiles`

`200` with `{"profiles": [Profile], "default": string, "prompts":
{"workspace": string, "chat": string}}`: every profile, oldest first, the id
of the default one, and the built-in base prompts of a workspace session and
of a chat.

### `GET /api/profiles/inherited`

`?model_id=` optional. `200` with the Configuration a profile that chooses
that model (absent or empty: none) falls through to: the model's own values
and the defaults. Nothing else a profile sets changes it, so a profile
editor asks with the model it has chosen before the profile is saved, and a
new profile asks before it exists. `400` for a model that does not exist.

### `POST /api/profiles`

| Field | Type | Meaning |
|---|---|---|
| `name` | string, required | Unique, 1 to 64 characters. |
| `description` | string | At most 500 characters. |
| ProfileSettings fields | | What the profile sets. |
| `tools` | string array or null | The tool choice, in the shape `PUT /api/sessions/{id}/tools` takes; null is every tool, an empty list none. A profile may choose a tool that needs a workspace: a chat's runs leave it out. |

`201` with the `Profile`. `400` for a bad field; `409` when the name is taken.

### `PUT /api/profiles/{id}`

The same body as `POST`, which replaces the whole profile. `200` with the
`Profile`; `404` for an unknown profile. Runs already going keep the
configuration they started with.

### `DELETE /api/profiles/{id}`

`204`. The sessions that chose it run with the default profile, and when it
was the default, the first profile is. `409` for the last profile: a run
always needs one.

### ProfileSettings

What a profile sets, and what a session overrides of it. A field that is
null, an absent `model_id`, and a sampling parameter left out are not set.

| Field | Type | Meaning |
|---|---|---|
| `model_id` | string, optional | The model, by id. |
| `workspace_prompt` | string or null | Replaces the built-in base prompt of a workspace session, the empty string included. At most 64 KiB. |
| `chat_prompt` | string or null | Replaces the built-in base prompt of a chat, likewise. |
| `instructions` | string or null | Follows the base prompt and the context files. At most 64 KiB. |
| `context_files` | boolean or null | Whether the workspace's AGENTS.md files are read. |
| `sampling` | Sampling | The sampling parameters set here. |

### Sampling

Every field is optional; one left out is not set.

| Field | Type | Meaning |
|---|---|---|
| `temperature` | number, 0 to 2 | |
| `top_p` | number, 0 to 1 | |
| `top_k` | number, at least 1 | Not a Chat Completions field; vLLM, llama.cpp, SGLang, LM Studio, and OpenRouter read it. Sent only when set. |
| `min_p` | number, 0 to 1 | Like `top_k`. |
| `frequency_penalty`, `presence_penalty` | number, -2 to 2 | |
| `seed` | number | |
| `stop` | string array | At most 16 sequences of 1 to 256 bytes. An empty list sends none, which clears a lower layer's. |
| `max_output` | number, at least 1 | The most tokens one response may generate. |
| `reasoning_effort` | string | A word the model offers; the empty string leaves it to the endpoint. |

### Profile

| Field | Type | Meaning |
|---|---|---|
| `id`, `name`, `description` | string | The profile. |
| ProfileSettings fields | | What it sets. |
| `tools` | string array or null | Its tool choice. |
| `inherited` | Configuration | What its unset values fall through to: the model row of the model it resolves to, and the defaults. |
| `created_at`, `updated_at` | time | When it was made and last changed. |

### Configuration

A resolved configuration, with the layer each value came from.

| Field | Type | Meaning |
|---|---|---|
| `profile_id`, `profile_name` | string | The profile it starts from. |
| `model_id`, `model` | string | The model's id and name, empty when no model is configured. |
| `workspace_prompt`, `chat_prompt` | string | The base prompts. |
| `instructions` | string | The extra instructions, empty for none. |
| `context_files` | boolean | Whether context files are read. |
| `tools` | string array or null | The tool choice; null is every tool the session can run. |
| `sampling` | Sampling | The sampling parameters sent. |
| `dropped_effort` | string, optional | An effort chosen above the model that the model does not offer. |
| `sources` | object | The layer of each value: `request`, `session`, `profile`, `model`, or `default`, keyed `profile`, `model`, `workspace_prompt`, `chat_prompt`, `instructions`, `context_files`, `tools`, and `sampling.<parameter>` for each parameter sent. `profile` is `session` when the session chose it. |

## Settings

### `GET /api/settings`

`200` with `{"settings": {key: value}, "defaults": Defaults}`: the settings
table as one object, with values as they were stored, and the values the
harness uses for its own keys while the table does not name them.

### `PUT /api/settings`

A JSON object of the keys to write. Keys the body does not name are left
alone; JSON `null` stores null, which means "use the default". `200` with the
same body as `GET`. Keys are 1 to 64 characters. The harness reads and
validates these keys; any other key is the UI's own and is stored as it
comes. One invalid value is `400` and writes nothing. The keys are written
in one transaction, so a write the database refuses is a `500` that also
writes nothing.

| Key | Value | Meaning |
|---|---|---|
| `default_model` | string | The name of the model a run uses when neither the request nor the session's configuration names one. It must name a model. |
| `default_profile` | string | The id of the profile a session that chose none runs with. It must name a profile; when that profile is deleted, the first profile is the default. |
| `sandbox_image` | string | The image a new workspace runs when it names none. |
| `subagent_max_depth` | number, 1 to 8 | How many levels of children a session may have. |
| `subagent_max_children` | number, 1 to 16 | How many children of one session may run at a time. |
| `setup_complete` | boolean | The user finished or skipped the guided setup. |
| `search_order` | array of strings | The web search providers, most preferred first, each a registered provider at most once. A provider left out is never queried; an empty list turns web search off. |
| `search_limits` | object | Quotas by bucket, `{"exa": {"month": 500}, "marginalia": {"day": 50}}`. Each bucket must exist; `day` and `month` are whole numbers from 1 to 10 000 000, and 0 or an absent field is unlimited. Anything else is `400` naming the bucket and the range. A bucket not named keeps its default. |

### Defaults

| Field | Type | Meaning |
|---|---|---|
| `sandbox_image` | string | The deployment's sandbox image, `eika-sandbox:latest` in the compose stack. |
| `subagent_max_depth` | number | 2. |
| `subagent_max_children` | number | 4. |
| `search_order` | array of strings | Every web provider in its default order: `searxng`, `exa`, `tavily`, `brave`, `marginalia`. |
| `search_limits` | object | Every quota bucket's default, as `search_limits` takes it: Exa 900 a month, Tavily 1000, Brave 2000, Marginalia 100 a day, SearXNG and GitHub unlimited. |

## Search

The engine behind `web_search` and the health of its backends. The keys are
sealed like provider keys and never returned: a key is reported as set, and a
key of 16 characters or more by its last four.

### `GET /api/search/status`

`200` with:

| Field | Type | Meaning |
|---|---|---|
| `order` | array of strings | The web provider order in force. |
| `backends` | array of Backend | Every backend, web providers first, in registration order. |
| `keys` | array of SearchKey | Every key a backend uses, stored or not. |
| `cached_searches` | number | Searches the result cache holds. |
| `cached_pages` | number | Pages the web_fetch cache holds. |
| `searxng_url` | string | Where the deployment looks for SearXNG. |

Backend:

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The provider or source name. |
| `web` | boolean | A provider in the web chain; otherwise a source web_search names. |
| `key`, `key_required`, `key_set` | string, boolean, boolean | The key it is sent, whether it is skipped without it, and whether it is stored. |
| `bucket` | string | The quota bucket it counts against; absent when it is not tracked. |
| `state` | string | `ready`, `no API key`, `cooling down 12m`, `daily quota spent`, or `monthly quota spent`. |
| `usage` | object | `day`, `day_used`, `month`, `month_used`, `cooldown_until`, `fail_streak`. |
| `limit` | object | `day` and `month`; absent or 0 is unlimited. |
| `probe` | string | For SearXNG: `up`, `HTTP 403`, `connection refused`, and the like. |

SearchKey: `name`, `set`, `hint` (the last four characters), `updated_at`.

### `PUT /api/search/keys/{name}`

`{"key": string}` stores the key under `name`, one of `exa`, `tavily`,
`brave`, `github`; an empty key removes it. `200` with `{"keys": [SearchKey]}`.
An unknown name is `404`, a key over 4096 bytes `400`.

### `POST /api/search`

Runs one search exactly as `web_search` does, spending quota. The body is
`{"query": string, "source": string, "count": number}`: `source` is `web`
(the default), `wikipedia`, `arxiv`, `github_code`, `github_repos`, or
`github_issues`; `count` is 1 to 25, default 10; the query is at most 500
characters. `200` with:

| Field | Type | Meaning |
|---|---|---|
| `text` | string | What the model would read. |
| `is_error` | boolean | The search failed in a way the model would be told about: an empty query, no provider answering, a source refusing the query. |
| `details` | object | `source`, `query`, `count`, `providers` (the one that answered), `attempts` (`{provider, error}` skipped on the way), `cached`, `pool`, `results` (`{title, url, description}`), `ms`. These are also web_search's tool details. |

## MCP servers

An MCP server is a Model Context Protocol server whose tools runs offer
beside the built-in ones. An `http` server is remote: the harness connects to
it, and its tools reach chats too. A `stdio` server is a command the harness
starts in each workspace whose session uses it, through eikad's `/process`;
its tools need a workspace. A server's tool is named
`mcp__<server>__<tool>`, cut to 64 characters with a hash when longer. The
state of every server is on the `global` topic as `mcp.server` events.

Header and environment values and the OAuth client secret are sealed like
provider keys and never returned; their names are. So are the OAuth tokens,
of which the API reports only whether they exist. A stdio server's
environment is visible to the agent in the workspace it runs in.

### `GET /api/mcp/servers`

`200` with `{"servers": [MCPServer]}`, by name.

### `POST /api/mcp/servers`

| Field | Type | Meaning |
|---|---|---|
| `name` | string, required | What the user calls it; unique, at most 32 characters of letters, digits, hyphens, and single underscores between them, since it is part of every tool name. |
| `kind` | `http` or `stdio`, required | How the server is reached. It never changes. |
| `url` | string | An `http` server's http or https endpoint. No credentials or fragment. |
| `headers` | object of strings | Headers every request to an `http` server carries, such as an API key. At most 64; not one the client sets itself (`Accept`, `Content-Type`, `Mcp-*`, `MCP-Protocol-Version`, `Last-Event-ID`, ...). A configured `Authorization` header replaces OAuth. |
| `command` | string | A `stdio` server's program, such as `npx`. |
| `args` | string array | Its arguments. At most 64. |
| `env` | object of strings | Variables its process gets on top of the workspace's. At most 64. |
| `enabled` | boolean | Defaults to true. A server that is off is not connected and offers nothing. |
| `disabled_tools` | string array | Server tool names (not `mcp__` names) left out of every run. |
| `oauth_client_id` | string | An `http` server's client registered by hand with its authorization server. |
| `oauth_client_secret` | string | That client's secret, if it has one. |

`201` with the `MCPServer`. An enabled `http` server starts connecting at
once. `400` for a bad field or a field of the other kind; `409` when the name
is taken.

### `GET /api/mcp/servers/{id}`

Everything the harness knows of one server. `200` with `MCPServerDetails`;
`404` for an unknown server, `409` when its secrets cannot be opened any more.

### `PATCH /api/mcp/servers/{id}`

`name`, `url`, `headers`, `command`, `args`, `env`, `enabled`,
`disabled_tools`, `oauth_client_id`, and `oauth_client_secret`, each
optional; an absent field is left alone. `headers` and `env` replace the
whole set, and a `null` value keeps the value stored under that name, so a
form can change one entry without the others' values. An empty
`oauth_client_secret` removes it, and so does clearing `oauth_client_id`.
A `url` that differs from the stored one drops the stored headers, unless
the request gives `headers`, and the OAuth tokens, which were issued for the
old URL; the registered client is kept. A new `name` is carried into the
tool choices of profiles and sessions, so `mcp__<old>__*` and
`mcp__<old>__<tool>` entries become `mcp__<name>__…`. The server's
connections end and an enabled `http` server connects again. `200` with the `MCPServer`; `400` for
a bad field, a field of the other kind, or a `null` for a name with no
stored value; `409` when the name is taken.

### `DELETE /api/mcp/servers/{id}`

Removes the server, its credentials, and its connections, and emits
`mcp.server` with state `removed`. `204`.

### `POST /api/mcp/servers/{id}/connect`

`{"workspace_id": string}`, where the workspace is where a `stdio` server
runs and is ignored for an `http` one; send `{}` for an `http` server. Drops
the server's connection there and connects again, waiting for the attempt.
`200` with `MCPServerDetails`, whatever the attempt came to: a server that
did not connect says why in `server.state`, `server.error`, and `logs`.
`400` for a `stdio` server without a workspace, `404` for an unknown server
or workspace, `409` for a server that is off or a workspace that is not
running.

### `POST /api/mcp/servers/{id}/authorize`

Starts authorizing an `http` server with OAuth 2.1 as the MCP authorization
spec describes it: `{"redirect_uri": string}`, which must be the web UI's
`/mcp/callback` page, with no query or fragment, on the host the request
came to, a host `allowed_origins` names, or the host of `public_url`. The
frontend sends `<window.location.origin>/mcp/callback`.

`200` with `{"authorization_url": string}`: send the browser there. The
authorization server sends it back to `redirect_uri` with `code`, `state`,
and perhaps `iss`, or `error` and `error_description`; the page posts them
to `POST /api/mcp/oauth/callback`. The authorization waits ten minutes.

The harness discovers the authorization server from the server's
`WWW-Authenticate` challenge or its protected resource metadata, and picks a
client: `oauth_client_id` when set, then its own Client ID Metadata Document
when the authorization server supports one and `public_url` is https, then
dynamic registration. `400` with what went wrong when there is no way to get
a client (the message names the redirect URI to register one by hand with),
the authorization server does not offer PKCE, a discovery document is
missing or refers elsewhere, or the redirect URI is not the UI's; `400` for a
`stdio` server.

### `POST /api/mcp/oauth/callback`

| Field | Type | Meaning |
|---|---|---|
| `state` | string | The `state` query parameter the browser came back with. |
| `code` | string | The `code` parameter. |
| `iss` | string or absent | The `iss` parameter. Send it exactly when the redirect carried one; an absent `iss` and an empty one are different answers. |
| `error`, `error_description` | string | The error parameters, when the authorization server refused. |

Checks that the answer came from the authorization server the authorization
started with, redeems the code, stores the tokens sealed, and connects the
server with them. `200` with `{"server_id": string}`. `404` when no
authorization waits for that `state` (it finished, expired, or was answered
once already); `400` with the reason when the issuer is wrong, the
authorization server refused, or it would not redeem the code.

### `DELETE /api/mcp/servers/{id}/authorization`

Signs out: asks the authorization server to revoke the refresh token and
the access token, forgets both, and ends the server's connections. The
registered client is kept for the next authorization. `204`.

### `POST /api/mcp/servers/{id}/resources/read`

`{"uri": string, "workspace_id": string}`, where `workspace_id` is only for
a `stdio` server. Reads one resource. `200` with
`{"contents": [ContentDetail]}`, each of type `resource`. `400` with the
server's message when it refuses; an `http` server that is not connected is
connected first.

### `POST /api/mcp/servers/{id}/prompts/get`

`{"name": string, "arguments": {string: string}, "workspace_id": string}`.
Renders a prompt. `200` with `{"description": string, "messages":
[{"role": "user" or "assistant", "content": ContentDetail}]}`; `400` with
the server's message when it refuses.

### `POST /api/elicitations/{id}/answer`

An MCP server asked the user for input during a tool call, as an
`mcp.elicitation` event and an entry of the run state's `elicitations`:

| Field | Type | Meaning |
|---|---|---|
| `action` | string, required | `accept`, `decline`, or `cancel`. |
| `content` | object | For an accepted `form`: the values by field name, each of the type the field's schema says. Absent otherwise. |

Delivers the answer to the waiting call. `204`. `404` when nothing waits on
that elicitation (it was answered, or its run ended); `400` for another
action, content that does not fit the form, or content with anything but an
accepted form.

### `GET /oauth/client-metadata.json`

Public, outside `/api`: the harness's OAuth Client ID Metadata Document,
which an authorization server fetches to learn who Eika is. Served only when
`public_url` is https, where the document's `client_id` is
`<public_url>/oauth/client-metadata.json` and its one redirect URI
`<public_url>/mcp/callback`; `404` otherwise.

### MCPServer

| Field | Type | Meaning |
|---|---|---|
| `id`, `name` | string | The server. |
| `kind` | string | `http` or `stdio`. |
| `url` | string | An `http` server's endpoint; empty for `stdio`. |
| `header_names` | string array | The headers every request carries, sorted. |
| `command`, `args` | string, string array | A `stdio` server's command line. |
| `env_names` | string array | The variables its process gets, sorted. |
| `enabled` | boolean | The server is on. |
| `disabled_tools` | string array | Server tool names left out of every run, sorted. |
| `oauth_client_id` | string | The client registered by hand, if any. |
| `oauth_client_secret_set` | boolean | That client has a secret stored. |
| `state` | string | `disabled`, `idle` (not connected, nothing failed), `connecting`, `connected`, `unauthorized` (it needs OAuth authorization), or `error`. A `stdio` server is `connected` while it runs in any workspace. |
| `error` | string, optional | Why it is `unauthorized` or in `error`. |
| `workspaces` | string array | The workspaces a `stdio` server runs in now. |
| `created_at`, `updated_at` | time | When it was made and last changed. |

### MCPServerDetails

| Field | Type | Meaning |
|---|---|---|
| `server` | MCPServer | The server and its state. |
| `connection` | object, optional | What the latest successful connection learned; absent before one. `era` (`modern` for the 2026-07-28 revision, `legacy` for an initialize-based one), `transport` (`streamable_http`, `sse`, or `stdio`), `protocol_version`, `supported_versions` (a modern server's list), `server_info` (`name`, `title`, `version`, `description`, `website_url`), `capabilities` (booleans `tools`, `tools_list_changed`, `resources`, `resources_subscribe`, `resources_list_changed`, `prompts`, `prompts_list_changed`, `logging`, `completions`, and the string arrays `experimental` and `extensions`), and `instructions`. |
| `tools` | array | Every tool the server lists: `name` (the server's), `exposed_name` (what the model calls it), `title`, `description`, `input_schema`, `output_schema`, `annotations` (`title` and the nullable booleans `read_only`, `destructive`, `idempotent`, `open_world`, which are the server's own claims), and `enabled` (false for one in `disabled_tools`). |
| `excluded_tools` | array | Tools listed but never offered, with `name` and `reason`: a modern server's tool whose `x-mcp-header` annotations break the rules. |
| `resources` | array | `uri`, `name`, `title`, `description`, `mime_type`, `size`. |
| `resource_templates` | array | `uri_template`, `name`, `title`, `description`, `mime_type`. |
| `prompts` | array | `name`, `title`, `description`, `arguments` (`name`, `title`, `description`, `required`). |
| `list_errors` | object of strings | The lists the server could not give, by method, such as `prompts/list`. |
| `fetched_at` | time, optional | When the lists were read. They are kept after the connection ends. |
| `logs` | array | The last 200 lines of the server's log, oldest first: `time`, `source` (`stderr` for a stdio server's own output, `server` for a log notification, `eika` for what the harness did), `level`, and `text`. |
| `auth` | object | `challenged` (the server asked for authorization) with `challenge` (`resource_metadata`, `scope`, `error`, `error_description`), `authorized` (an access token is stored), `has_refresh_token`, `issuer`, `resource`, `resource_metadata_url`, `scope`, `expires_at`, `client_id`, `registration` (`preregistered`, `metadata_document`, or `dynamic`), and `updated_at`. |

### ContentDetail

One block of an MCP tool result, a read resource, or a rendered prompt, as
the UI shows it. Images and audio are the UI's alone: the model reads a line
saying they were shown to the user.

| Field | Type | Meaning |
|---|---|---|
| `type` | string | `text`, `image`, `audio`, `resource_link`, or `resource`. |
| `text` | string, optional | The text of a `text` block or a text resource, cut in the middle past 48 KiB. |
| `mime_type`, `uri`, `name`, `description` | string, optional | What the block says of itself. |
| `data` | base64 string, optional | An image, audio, or binary resource. Absent when the result's media were over 4 MiB. |
| `size` | number, optional | The decoded size of `data`, or of what was left out. |
| `omitted` | boolean, optional | The media were left out for the budget. |

### MCP tool calls

An MCP tool's call is an ordinary `tool.call` and `tool.result`, named
`mcp__<server>__<tool>`; progress the server reports arrives as `tool.output`
lines, `progress 3/10: message`. The `tool.result` event's `details` is:

| Field | Type | Meaning |
|---|---|---|
| `server` | string | The server's name. |
| `tool` | string | The tool's name on the server. |
| `content` | ContentDetail array | Every block the server sent, in order. |
| `structured_content` | JSON value, optional | The tool's structured result. |
| `is_error` | boolean, optional | The server said the tool failed. |

`mcp_list_resources` and `mcp_read_resource` read the resources of every
server a run reaches; the second's details are the same shape, with `tool`
`mcp_read_resource`. Details are not stored with the session, so a replayed
tool result carries its `content` alone.

## System

### `GET /api/system`

What the setup screens check before the first workspace. `200` with:

| Field | Type | Meaning |
|---|---|---|
| `docker` | `{"reachable": boolean, "error": string}` | Whether the harness can use the Docker socket, and the client's reason when it cannot. |
| `sandbox_image` | `{"name": string, "present": boolean}` | The image new workspaces run and whether the Docker host has it. |
| `providers`, `models`, `projects` | number | How many of each exist. |

## Event stream

`GET /api/events` upgrades to a WebSocket. The protocol, the subscription
requests, and the replay are documented in `events.md`. A replay runs
alongside the live stream, so an entry written while it is in flight can
arrive both ways; clients deduplicate by `entry_id`.

The handshake is accepted from the harness's own origin and from any origin
the deployment lists in `allowed_origins`. Any other `Origin` header is
refused before the upgrade.

## Git hub

`/git/<project>.git/...` speaks git's Smart HTTP protocol for workspaces. It
is mounted outside the bearer token and authenticates with HTTP basic auth:
the workspace id as the user and its per-workspace hub token as the password,
scoped to one project. Clients other than workspaces have no reason to use it.
