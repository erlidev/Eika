# Event stream

Every event Eika streams uses one envelope. `type` decides the shape of
`payload`; `topic` decides who receives it. The Go types live in
`internal/event` and the TypeScript mirror in `web/src/api/events.ts`. Change
all three in the same commit.

The WebSocket transport and topic subscription land in phase 4. Until then the
agent loop emits these events through an `event.Emitter`.

## Envelope

| Field | Type | Meaning |
|---|---|---|
| `type` | string | Event type name, for example `tool.call`. |
| `topic` | string | `global`, `workspace:<id>`, or `session:<id>`. |
| `time` | RFC 3339 timestamp, UTC | When the event was created. |
| `payload` | object, optional | Type-specific body. |

Agent run events are published on `session:<id>`.

## Run events

A turn emits `turn.start`, then `message.delta` for each piece of assistant
text, then for every tool call `tool.call`, any number of `tool.output`, and
`tool.result`. A turn that asked for tools calls the model again, so these
repeat. The turn ends with `turn.end`, or with `run.error` if it failed.

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

### `tool.call`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `call_id` | string | Identifies the call; ties `tool.output` and `tool.result` to it. |
| `name` | string | Tool name, for example `bash`. |
| `arguments` | object | The arguments the model supplied, as the tool's schema defines them. |

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

### `turn.end`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `stop_reason` | string, optional | Why the model stopped, for example `stop`. |
| `usage` | object | `input_tokens`, `output_tokens`, and `total_tokens`, summed over every model call in the turn. |

### `run.error`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `message` | string | What failed. |
| `retryable` | boolean | The failure was of a retryable kind, so the run exhausted its retry budget. |

## Event types that other phases own

`question.asked` (phase 4), `subagent.started` and `subagent.finished`
(phase 6), and `workspace.state` (phase 2) have their names fixed in
`internal/event` and get their payloads documented here when they land.
