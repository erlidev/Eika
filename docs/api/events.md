# Event stream

Every event Eika streams uses one envelope. `type` decides the shape of
`payload`; `topic` decides who receives it. The Go types live in
`internal/event` and the TypeScript mirror in `web/src/api/events.ts`. Change
all three in the same commit.

## Transport

`GET /api/events` upgrades to a WebSocket. It takes the bearer token as the
`token` query parameter, because a browser cannot set a header on a handshake;
it is the one route that does.

```
ws://<harness>/api/events?token=<token>&topics=global,session:<id>&since=<entry_id>
```

| Parameter | Meaning |
|---|---|
| `token` | A sign-in session's token or the deployment's API token. Required. |
| `topics` | Comma-separated topics to subscribe to at once. Optional; a client may subscribe after connecting instead. |
| `since` | An entry id. The session that entry belongs to is replayed from the entry after it, before any live event. Optional. |

Every frame the harness sends is one JSON event in the envelope below. Every
frame a client sends is one JSON request:

### `subscribe`

```json
{"type": "subscribe", "topics": ["global", "session:s1", "workspace:w1"]}
```

Replaces the connection's topics with the ones given. At most 64 topics. A
connection with no topics receives nothing but what it asks to have replayed.

### `session.replay`

```json
{"type": "session.replay", "session_id": "s1", "since": "e4"}
```

Sends the session's current path as `session.message` events on this
connection alone, from the root or from the entry after `since`. It is how a
client that opens a session late, or moves the head, gets the conversation
without a second HTTP request. `since` that names no entry on the branch ends
the connection with a policy violation, as does an unknown request type.

Replayed events are written straight to the socket, so a long history is never
dropped the way a slow subscriber's live events are.

A replay reads the session's path while the stream keeps running, so an entry
a run writes during the replay can arrive twice: once as the live event that
produced it and once as a `session.message`. Deduplicate by `entry_id`, and
treat a `session.message` for an entry you already hold as a no-op.

## Delivery

One in-process `event.Bus` fans every event out to the connections subscribed
to its topic. Each subscriber has a buffered channel of its own; a client that
stops reading loses events rather than blocking the run that produced them.
When it reads again it first gets a `bus.dropped` event saying how many it
missed, and then the stream continues. A client that sees one re-requests what
it needs with `session.replay`.

## Envelope

| Field | Type | Meaning |
|---|---|---|
| `type` | string | Event type name, for example `tool.call`. |
| `topic` | string | `global`, `workspace:<id>`, or `session:<id>`. |
| `time` | RFC 3339 timestamp, UTC | When the event was created. |
| `payload` | object, optional | Type-specific body. |

There are three kinds of topic: `global`, `workspace:<id>`, and
`session:<id>`. Agent run events and question and replay events are published
on `session:<id>`; workspace lifecycle events on `workspace:<id>`. A
`bus.dropped` event reaches one connection only and carries the topic
`global`, whatever that connection subscribed to.

## Run events

A turn emits `turn.start`, then `message.delta` for each piece of assistant
text and `reasoning.delta` for each piece of the model's thinking. If that
model attempt fails and is retried, `message.reset` tells the client to
discard both before the next attempt starts. The turn then emits, for every
tool call, `tool.call`, any number of `tool.output`, and `tool.result`. A turn
that asked for tools calls the model again, so these repeat. Whenever the
endpoint reports token usage, the turn emits `turn.progress`. The turn ends
with `turn.end`, or with `run.error` if it failed.

`run_id` identifies one turn and appears on every event of that turn.

### `turn.start`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `session_id` | string | The session the turn runs in. |
| `workspace_id` | string, optional | The workspace the session belongs to. |
| `message` | string | The user message that started the turn. Empty when the turn was started by tools alone. |

### `message.delta`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `text` | string | The next piece of assistant text. Concatenate deltas in arrival order. |

### `reasoning.delta`

The model's own reasoning, which is not part of its answer. Eika streams it
whatever `preserve_thinking` is set to: showing the model think is the
client's business, and replaying the reasoning to the endpoint on later calls
is `preserve_thinking`'s. It is stored on the assistant entry as `reasoning`,
so a replay delivers it too.

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `text` | string | The next piece of reasoning. Concatenate deltas in arrival order. |

### `message.reset`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn whose current text must be discarded. |

### `tool.call`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `call_id` | string | Identifies the call; ties `tool.output` and `tool.result` to it. |
| `name` | string | Tool name, for example `bash`. |
| `arguments` | JSON value | The arguments the model supplied. Malformed text is safely quoted as a string and has the marker below; the tool reports the argument error normally. |
| `arguments_malformed` | boolean, optional | True when `arguments` is safely quoted malformed text. Absent for valid JSON, including a valid top-level string. |

### `tool.output`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `call_id` | string | The call the output belongs to. |
| `text` | string | A chunk of output from a tool that is still running, such as a command's combined stdout and stderr. |

### `tool.result`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `call_id` | string | The call that finished. |
| `name` | string | Tool name. |
| `content` | string | What the model sees. Long output is already truncated. |
| `is_error` | boolean | The call failed in a way the model can act on. |
| `details` | object, optional | Structured data for the interface only, for example a command's exit code. |
| `duration_ms` | number | How long the call took. |

### `turn.progress`

Emitted every time the endpoint reports token usage, which for most endpoints
is once per model call and for an endpoint with continuous usage statistics
(vLLM, llama.cpp) is once per chunk. A client needs two reports from the same
attempt and divides their usage difference by their `generation_ms`
difference. The first report is only a baseline: timing starts when the first
streamed token arrives, whose token count is unknown. A retry clears the
baseline. An endpoint that reports usage once shows context usage but no rate.

`generation_ms` is timed from each response's **first streamed token**, so it
excludes the endpoint's queueing and prefill. A retried model attempt is not
counted.

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `usage` | object | `input_tokens`, `output_tokens`, and `total_tokens`, summed over every model call in the turn so far. |
| `context` | object | The **most recent** model call's own usage, in the same shape. This is what fills the model's context window; `usage` is what the turn costs, which is larger. |
| `generation_ms` | number | The turn's time inside model responses so far. |
| `context_window` | number | The configured window of the model that produced this usage. Zero means it is not configured. |

### `turn.end`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `stop_reason` | string, optional | Why the model stopped, for example `stop`. |
| `usage` | object | `input_tokens`, `output_tokens`, and `total_tokens`, summed over every model call in the turn. |
| `context` | object | The last model call's own usage: how much of the model's context window the conversation now fills. |
| `generation_ms` | number | The turn's total time inside model responses. A rate uses the difference between comparable progress reports, not this total alone. |
| `context_window` | number | The configured window of the model that ran the turn. Zero means it is not configured. |

### `run.error`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `message` | string | What failed. |
| `retryable` | boolean | The failure was of a retryable kind, so the run exhausted its retry budget. |

### `question.asked`

The run called `ask_user` and is blocked until
`POST /api/questions/{id}/answer` delivers an answer or the run is aborted.

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `session_id` | string | The session that is waiting. |
| `call_id` | string | The `ask_user` call the answer completes. |
| `question_id` | string | What to POST the answer to. |
| `question` | string | The question to put to the user. |
| `options` | string array, optional | The answers to choose from. Absent for an open question. |
| `allow_free_text` | boolean | An answer outside `options` is accepted. |

## Subagent events

Both are published on the **parent** session's topic, `session:<parent id>`,
so a client watching a session sees the children it spawns without
subscribing to them. The child's own run events go to `session:<child id>`
like any other run's.

### `subagent.started`

A run called `spawn_agent`. The child's workspace exists and is cloned from
the hub at `base_commit`; its run is about to start.

| Field | Type | Meaning |
|---|---|---|
| `subagent_id` | string | The `subagents` row. `POST /api/subagents/{id}/abort` takes it. |
| `parent_session_id` | string | The session that spawned the child. |
| `child_session_id` | string | The child's session. |
| `child_workspace_id` | string | The child's workspace. |
| `name` | string | What the parent called the child. |
| `branch` | string | The branch the child works on. |
| `base_commit` | string, optional | The parent commit the child was cloned at. Empty for a project with no commits. |
| `task` | string | The child's first user message. |

### `subagent.finished`

The child's run ended, its tree is committed, and its branch is in the hub.
The same fields make up the tool result the parent's model sees.

| Field | Type | Meaning |
|---|---|---|
| `subagent_id` | string | The `subagents` row. |
| `parent_session_id` | string | The session that spawned the child. |
| `child_session_id` | string | The child's session. |
| `child_workspace_id` | string | The child's workspace. It is stopped, not destroyed. |
| `name` | string | What the parent called the child. |
| `branch` | string | The branch the child pushed to the hub. |
| `state` | string | `done`, `error`, or `aborted`. |
| `commit` | string, optional | The child's head commit. Absent when it committed nothing. |
| `summary` | string, optional | The child's final assistant message. |
| `diff_stat` | string, optional | `git diff --stat` from the parent's base commit to the child's head. |
| `error` | string, optional | Why a child that did not finish cleanly stopped. |

## Workspace events

### `workspace.state`

Published on `workspace:<id>` whenever a workspace reaches a new lifecycle
state, including the reconciliation a harness does at startup. It is also
published, with the state unchanged, after a file is saved, a commit, or a
push through the API, so that views of the workspace's files and changes
refresh.

| Field | Type | Meaning |
|---|---|---|
| `workspace_id` | string | The workspace. |
| `project_id` | string, optional | The project it holds. |
| `state` | string | `creating`, `running`, `stopped`, or `gone`. |

## Stream events

These two exist only on the stream: nothing in the agent loop emits them.

### `session.message`

One stored entry, sent in reply to a replay. A client renders it exactly as it
renders a message it watched arrive live.

| Field | Type | Meaning |
|---|---|---|
| `session_id` | string | The session replayed. |
| `entry_id` | string | The entry. Pass the last one you saw as `since` to resume. |
| `parent_id` | string, optional | The entry it follows. |
| `kind` | string | `user`, `assistant`, `tool_call`, `tool_result`, `system`, or `event`. |
| `commit` | string, optional | The workspace HEAD commit the entry was produced at. |
| `created_at` | time | When the entry was written. |
| `message` | object | The entry's stored payload: a provider message for the conversation kinds. An assistant message can include `reasoning` and `metrics`; `metrics` holds `run_id`, turn `usage`, last-call `context`, `generation_ms`, and `context_window`, so replay restores the context view. Metrics are not sent to the provider. |

### `bus.dropped`

The client read too slowly and lost events. It reaches that client alone.

| Field | Type | Meaning |
|---|---|---|
| `dropped` | number | How many events were lost since the last report. |
