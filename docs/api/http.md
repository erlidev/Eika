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
`POST /api/auth/login`), and the git hub under `/git/`, which authenticates
workspaces itself with per-workspace basic auth. `GET /api/events` also
accepts the token as the `token` query parameter, because a browser cannot set
a header on a WebSocket handshake; no other route does, so a token never has
to appear in an ordinary URL.

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
| `not_found` | 404 | The addressed project, workspace, session, entry, run, question, provider, or model does not exist. |
| `conflict` | 409 | The request collides with the current state: a duplicate name, a second run on a session, a workspace that is not running, a harness already set up, no model to run on, or a stored credential the harness can no longer open. |
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
| `model` | string | The name of the model this run uses. Empty uses the `default_model` setting, or the first model. |

- `run` starts a run from the session's head. `409` when one is already going.
- `steer` joins the run in progress as soon as the running tool call
  finishes, before the next model call.
- `follow_up` waits until the current turn ends and then starts the next one.
- `steer` and `follow_up` are `409` when the session has no run in progress.

`202` with the `Run`. `400` for empty text, an unknown mode, or a model that
does not exist; `409` when the session's workspace is not running or no model
is configured.

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
| `reasoning_effort` | string | `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, or `max`. Empty leaves it to the endpoint. |
| `preserve_thinking` | boolean | Ask a compatible endpoint for `reasoning_content` and replay it on later turns. Off by default; the official OpenAI API rejects it. |

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
"preserve_thinking": boolean}`: a model on a stored provider, saved or not.
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
| `preserve_thinking` | boolean | As on `POST`. |
| `created_at`, `updated_at` | time | When it was made and last changed. |

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
| `default_model` | string | The name of the model a run uses when the request names none. It must name a model. |
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
