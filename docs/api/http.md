# HTTP API

The harness serves one JSON API under `/api`. Bodies are JSON with
`snake_case` fields, times are RFC 3339 in UTC, and identifiers are the 20
character base32 strings `store.NewID` produces. The Go wire types live in
`internal/server`, one file per resource; the TypeScript mirrors live in
`web/src/api/`.

## Authentication

Every route under `/api` requires the deployment's bearer token:

```
Authorization: Bearer <auth_token>
```

The exceptions are `GET /healthz`, `GET /api/healthz`, and the git hub under
`/git/`, which authenticates workspaces itself with per-workspace basic auth.
`GET /api/events` also accepts the token as the `token` query parameter,
because a browser cannot set a header on a WebSocket handshake; no other route
does, so a token never has to appear in an ordinary URL.

A wrong or missing token is `401` with a `WWW-Authenticate: Bearer` header.

## Errors

Every failure has one shape:

```json
{"error": {"code": "not_found", "message": "read project p1: not found"}}
```

| Code | Status | Meaning |
|---|---|---|
| `invalid_request` | 400 | The request was malformed, missing a field, or named something the API will not accept. An unknown JSON field is malformed. |
| `unauthorized` | 401 | The bearer token was missing or wrong. |
| `not_found` | 404 | The addressed project, workspace, session, entry, run, or question does not exist. |
| `conflict` | 409 | The request collides with the current state: a duplicate name, a second run on a session, or a workspace that is not running. |
| `internal` | 500 | The harness failed. The message is always `internal error`; the detail is in the harness log. |

## Health

| Method | Path | Response |
|---|---|---|
| GET | `/healthz` | `{"status":"ok"}`. The container health check. |
| GET | `/api/healthz` | The same, under the prefix the frontend uses. |

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
| `remote_username_env` | string | Optional for `remote`. The `EIKA_*` environment variable that holds the upstream username. Set it with `remote_password_env`. |
| `remote_password_env` | string | Optional for `remote`. The `EIKA_*` environment variable that holds the upstream password or token. Set it with `remote_username_env`. |
| `host_path` | string | Required for `local`. An absolute path on the Docker host, bind-mounted at the workspace root. |
| `default_branch` | string | The branch a workspace uses when it names none. Defaults to `main`. |

`remote_url` must not contain userinfo, a query string, or a fragment. These
URL parts can expose credentials in stored data, API responses, logs, and Git
arguments. For a private HTTPS remote, put the username and password or token
in the harness environment and send only their variable names. Public remotes
omit both fields.

When Eika upgrades an old project whose remote URL has one of these parts, it
removes the unsafe part and sets the references to `EIKA_GIT_USERNAME` and
`EIKA_GIT_PASSWORD`. Set these variables in the harness environment before
the next fetch or push. Public old URLs without these parts do not change.

`201` with the `Project`. `400` for a missing or malformed field, an incomplete
credential pair, and a remote the hub cannot mirror; `409` when the name is
taken.

### `GET /api/projects/{id}`

`200` with the `Project`, `404` when there is none.

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
| `remote_username_env` | string, optional | The name of the upstream username variable. This is not the variable's value. |
| `remote_password_env` | string, optional | The name of the upstream password variable. This is not the variable's value. |
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
| `image` | string | The container image. Defaults to the configured `sandbox_image`. Ignored when `build_context` is set. |
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
workspace through its executor.

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
continues from.

### `GET /api/sessions`

`?workspace_id=<id>` narrows the list. `200` with `{"sessions": [Session]}`.

### `POST /api/sessions`

`{"workspace_id": string, "title": string}`, both required. `201` with the
`Session`.

### `GET /api/sessions/{id}`

`200` with `{"session": Session, "head": Entry | absent}`.

### `DELETE /api/sessions/{id}`

Aborts the session's run, then deletes it and its entries. `204`.

### `GET /api/sessions/{id}/outline`

Every entry of the tree, without payloads: what the session tree panel draws.

`200` with `{"session_id": string, "head_entry_id": string, "nodes": [Node]}`,
where a `Node` is `{id, parent_id?, kind, preview, commit?, created_at}`.

### `GET /api/sessions/{id}/path`

The branch from the root to the head, which is the conversation the model
sees.

`200` with `{"session_id": string, "entries": [Entry], "messages": [Message]}`.
`messages` holds the provider messages of the entries that carry one, in the
same order.

### `POST /api/sessions/{id}/head`

`{"entry_id": string}`. Moves the head to one of the session's own entries, so
the next run continues from there and the tree branches in place. `200` with
the `Session`. `409` while a run is going; `404` when the entry is not the
session's.

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

`201` with the new `Session`. `400` when the entry recorded no commit, so
there is nothing to clone; `409` when the source workspace is not running.

### Session

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The session id. |
| `workspace_id` | string | Where its runs act. |
| `title` | string | What the user calls it. |
| `head_entry_id` | string, optional | The entry the next run continues from. |
| `parent_session_id` | string, optional | The session it was forked from. |
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

A provider message can include `reasoning` on an assistant entry when the
configured endpoint preserves thinking. This value is opaque provider data
that Eika replays; it is not assistant-visible content. A tool call's
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
| `model` | string | The model this run uses. Empty uses the `default_model` setting, or the first configured model. |

- `run` starts a run from the session's head. `409` when one is already going.
- `steer` joins the run in progress as soon as the running tool call
  finishes, before the next model call.
- `follow_up` waits until the current turn ends and then starts the next one.
- `steer` and `follow_up` are `409` when the session has no run in progress.

`202` with the `Run`. `400` for empty text, an unknown mode, or a model the
deployment did not configure; `409` when the session's workspace is not
running.

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

### Run

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The run id. It is not the `run_id` on the stream: one run has one turn per model exchange, and each turn has its own `run_id`. |
| `session_id` | string | The session it works on. |
| `state` | string | `running`, `done`, `error`, or `aborted`. |
| `started_at` | time | When it began. |
| `finished_at` | time, optional | When it ended. |
| `error` | string, optional | Why an `error` run failed. |

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

`subagents.max_depth` and `subagents.max_children` in the configuration bound
how deep and how wide the tree may grow; the defaults are 2 and 4.

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

## Settings and models

### `GET /api/settings`

`200` with `{"settings": {key: value}}`, the settings table as one object.
Values are whatever JSON was stored.

### `PUT /api/settings`

A JSON object of the keys to write. Keys the body does not name are left
alone. `200` with the whole table as it now stands.

`default_model` is the one key the harness reads itself: a JSON string naming
the model a run uses when the request names none.

### `GET /api/models`

`200` with `{"models": [{name, context_window, max_output}], "default": string}`.
The models are the ones the deployment configured, in configuration order;
`default` is the `default_model` setting, empty when the user has not chosen
one.

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
