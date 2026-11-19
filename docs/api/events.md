# Event stream

Every event Eika streams uses one envelope. `type` decides the shape of
`payload`; `topic` decides who receives it. The Go types live in
`internal/event` and the TypeScript mirror in `web/src/api/events.ts`. Change
all three in the same commit.

## Transport

`GET /api/events` upgrades to a WebSocket. It takes the bearer token as the
`token` query parameter, because a browser cannot set a header on a handshake.

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
{"type": "subscribe", "topics": ["global", "session:s1", "computer:w1"]}
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
| `topic` | string | `global`, `computer:<id>`, or `session:<id>`. |
| `time` | RFC 3339 timestamp, UTC | When the event was created. |
| `payload` | object, optional | Type-specific body. |

There are three kinds of topic: `global`, `computer:<id>`, and
`session:<id>`. Agent run events, question and elicitation events, and replay
events are published on `session:<id>`; computer lifecycle events on
`computer:<id>`; MCP server state and session titles and edits on `global`. A
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
Before a model call the turn may compact the conversation, which emits
`compaction.start` and `compaction.end`; after an endpoint refused a call as
too large, `message.reset` comes first.

`run_id` identifies one turn and appears on every event of that turn.

### `turn.start`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `session_id` | string | The session the turn runs in. |
| `computer_id` | string, optional | The computer the session belongs to. Absent in a chat. |
| `message` | string | The user message that started the turn. Empty when the turn was started by tools alone, or by images alone. |
| `images` | array, optional | The images the message carries, as `session.message` stores them: `media_type`, base64 `data`, `width`, `height`. |

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
(vLLM, llama.cpp) is once per chunk.

`generation_ms` is timed from each response's **first streamed token**, so it
excludes the endpoint's queueing and prefill. A retried model attempt is not
counted.

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `usage` | Usage | What the turn has cost, summed over every model call in it so far. |
| `context` | Usage | The **most recent** model call's own usage. This is what fills the model's context window; `usage` is what the turn costs, which is larger. |
| `generation_ms` | number | The turn's time inside model responses so far. |
| `context_window` | number | The effective window of the model that produced this usage (the smaller of its configured window and what its endpoint reported per request). Zero means it is not configured. |
| `timings` | object, optional | How fast the **most recent** model call ran. See below. |

#### `timings`

Generation speed, split into the two phases whose speeds differ by orders of
magnitude. Both are stated as a token count and the milliseconds that count
took, so a client divides the pair rather than trusting a rate somebody else
rounded. The two are never summed.

| Field | Type | Meaning |
|---|---|---|
| `prompt_tokens` | number, optional | Tokens of prompt the endpoint read. |
| `prompt_ms` | number, optional | How long reading them took. |
| `decode_tokens` | number, optional | Tokens generated in the measured window. |
| `decode_ms` | number, optional | How long generating them took. |
| `source` | string, optional | `endpoint` or `harness`: who timed the generation phase. |

A phase nobody measured is absent, and `timings` itself is absent when neither
was. The prompt phase comes only from an endpoint that reports its own
timings: from outside, the time an endpoint spends reading a prompt cannot be
told apart from the time it spends queueing.

These endpoints report their own timings, and Eika reads all of their shapes:

| Engine | Where it reports |
|---|---|
| llama.cpp | `timings.{prompt_n, prompt_ms, prompt_per_second, predicted_n, predicted_ms, predicted_per_second}` |
| vLLM | `metrics.{time_to_first_token_ms, queue_time_ms, generation_time_ms}`, with `--enable-per-request-metrics` |
| NVIDIA NIM | `stats.{llm_input_token_length, time_in_queue_in_ms, response_tokens{…}}` |
| LM Studio | `stats.{time_to_first_token, generation_time, tokens_per_second}`, in seconds |
| TabbyAPI | `usage.{prompt_tokens_per_sec, completion_tokens_per_sec}` |
| Groq | `x_groq.usage.{prompt_time, completion_time}`, in seconds |
| Ollama | `prompt_eval_count`, `prompt_eval_duration`, `eval_count`, `eval_duration`, in nanoseconds |

For the rest — OpenAI, Anthropic, OpenRouter, SGLang, Together — the harness
times the generation phase itself, from the response's first streamed token to
its last, and `source` is `harness`. The first token is not among
`decode_tokens`: the clock starts when it arrives, so the window it opens
holds the tokens that followed it. A response of one token measures nothing
and reports no `timings`.

### `turn.end`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `stop_reason` | string, optional | Why the model stopped, for example `stop`. `length` and `content_filter` mean the endpoint cut the response off: what it did produce is kept and stored, its tool calls are dropped, and the turn ends here. A client says so rather than showing an answer that simply stops. `max_calls` and `max_wall_time` mean the run reached its limit of model calls or of time and ended there, without an error: everything it did is stored, every tool call is answered, and a message sent next continues it. Messages still queued stay queued for that run. |
| `usage` | Usage | What the turn cost, summed over every model call in it. |
| `context` | Usage | The last model call's own usage: how much of the model's context window the conversation now fills. |
| `generation_ms` | number | The turn's total time inside model responses. |
| `context_window` | number | The effective window of the model that ran the turn. Zero means it is not configured. |
| `timings` | object, optional | How fast the turn's last model call ran, in the shape `turn.progress` documents. |

### Usage

The `usage` and `context` objects of `turn.progress`, `turn.end` and
`compaction.end`, and the `usage` and `context` of a stored message's `metrics`,
are all this shape. Every field is a whole number or `null`. `null` means the
endpoint did not state the count; it is never `0` in its place, and a stated `0`
stays `0`. A turn's `usage` is the sum of its calls, so a count is `null` there
when any of the calls did not state it.

| Field | Type | Meaning |
|---|---|---|
| `input_tokens` | number or null | The whole prompt as the endpoint tokenised it, **cached tokens included**. |
| `output_tokens` | number or null | Everything the model generated, **reasoning included**. |
| `total_tokens` | number or null | What the endpoint states as the sum of the two. |
| `cached_tokens` | number or null | The part of `input_tokens` served from the endpoint's cache. |
| `prefilled_tokens` | number or null | The part of `input_tokens` the endpoint processed, where it says so (llama.cpp's `prompt_n`). |
| `reasoning_tokens` | number or null | The part of `output_tokens` that was reasoning. llama.cpp does not state it. |

A message stored before a count could be `null` has no `cached_tokens` key; it
reads as `null`, and a stored `0` in all of `input_tokens`, `output_tokens` and
`total_tokens` reads as nothing stated.

### `run.error`

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `message` | string | What failed. |
| `retryable` | boolean | The failure was of a retryable kind, so the run exhausted its retry budget. |

### `compaction.start`

The oldest part of the conversation is being summarized. It comes inside a
turn, between `turn.start` and the model call it makes room for, or alone,
from a run that `POST /api/sessions/{id}/compact` started and that has no
`turn.start` or `turn.end`. Nothing streams while the summary is written.

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | The turn, or the compaction's own id when it is a run of its own. |
| `reason` | string | `manual` (asked for), `idle` (maintenance), `threshold` (the next request would leave less of the window free than the reserve), or `overflow` (the endpoint refused a request as larger than its window; it is sent again after). |
| `tokens_before` | number | The estimated size of the context being compacted. |

### `compaction.end`

The compaction finished. One that succeeded is stored as an entry of kind
`compaction`, which a replay delivers: a compaction inside a turn with the
turn's entries, and one that is a run of its own at once. The context the
client last measured is no longer what the next request sends. One that
failed carries `error` and changed nothing; a failed `threshold` compaction
lets the turn go on with the conversation as it was.

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | As in `compaction.start`. |
| `reason` | string | As in `compaction.start`. |
| `tokens_before` | number | As in `compaction.start`. |
| `tokens_after` | number, optional | The estimated size of the context that replaced it. |
| `summary` | string, optional | The summary the context now begins with. |
| `kept` | number, optional | How many messages of the context before stay after the summary. |
| `usage` | Usage | What writing the summary cost. |
| `error` | string, optional | Why the compaction failed. |

### `question.asked`

For a subagent, this event is read-only: Eika answers through
`subagent_message`, and the question is absent from the HTTP broker.

The wait ends when the subagent run is cancelled.

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Identifies the turn. |
| `session_id` | string | The session that is waiting. |
| `call_id` | string | The `ask_user` call the answer completes. |
| `question_id` | string | The question id in the home notice. |
| `question` | string | The question to put to the user. |
| `options` | string array, optional | The answers to choose from. Absent for an open question. |
| `allow_free_text` | boolean | An answer outside `options` is accepted. |

### `mcp.elicitation`

An MCP server asked the user for input during a tool call, which waits until
`POST /api/elicitations/{id}/answer` delivers an answer or the run is
aborted. The request is also in the run state's `elicitations` until then,
so a client that connects late still finds it; nothing announces its answer
but the call's `tool.result`. A request the UI could not show, a form whose
schema is not flat or a URL that is not http or https, is declined without
being asked.

| Field | Type | Meaning |
|---|---|---|
| `elicitation_id` | string | What to POST the answer to. |
| `run_id` | string | Identifies the turn. |
| `session_id` | string | The session that is waiting. |
| `call_id` | string | The MCP tool call that waits. |
| `server` | string | The name of the MCP server that asks. |
| `mode` | string | `form`, for fields to fill in, or `url`, for a page to open. |
| `message` | string | What the server says it wants. |
| `requested_schema` | object, optional | In `form` mode, the flat JSON Schema of the fields; see Elicitation in `http.md`. |
| `url` | string, optional | In `url` mode, the page to send the user to. |

An MCP tool's `tool.result` carries the server's content blocks in
`details`; `http.md` documents the shape under MCP tool calls.

## Computer events

### `computer.state`

Published on `computer:<id>` whenever a computer reaches a new lifecycle
state, including the reconciliation a harness does at startup. It is also
published, with the state unchanged, after a write of `sandbox_limits` or
`sandbox_egress` is applied to the `home` computer, so that views of the
computer refresh.

The `home` computer's state, when the harness records it, is published on
`global` as well, so a client that has not seen the computer yet learns of it
and refetches `GET /api/computers`.

| Field | Type | Meaning |
|---|---|---|
| `computer_id` | string | The computer. |
| `state` | string | `creating`, `running`, `stopped`, or `gone`. |

## MCP events

### `mcp.server`

Published on `global` whenever an MCP server's state changes, what it serves
changes (its tools, resources, or prompts were listed again), it is
authorized or signed out, its configuration changes, or it is deleted. A
client refetches `GET /api/mcp/servers/{id}` for the rest, so the settings
page stays current without polling.

| Field | Type | Meaning |
|---|---|---|
| `server_id` | string | The server. |
| `name` | string | Its name. |
| `state` | string | `disabled`, `idle`, `connecting`, `connected`, `unauthorized`, `error`, or `removed` for a server that was deleted. |
| `error` | string, optional | Why it is `unauthorized` or in `error`. |

## Session events

### `session.title`

Published on `global` when an untitled session is named after its first
message by the model of the `fast` class (the `model_classes` setting).
It goes to every client, since any client may list any session; a client
refetches its session lists.

| Field | Type | Meaning |
|---|---|---|
| `session_id` | string | The session. |
| `computer_id` | string, optional | Its computer, absent for a chat. |
| `title` | string | The title it now has. |

### `session.updated`

Published on `global` when `PATCH /api/sessions/{id}` renames, pins, or
archives a session. Subagent creation and each lifecycle state or waiting
reason change and each stored subagent report also publish it, after the store
write. Each client refetches its session lists and the session to read the state
and optional `waiting_reason`. It is also published on the session topic after
a run releases the session, so clients can recover a missed turn-end event.

| Field | Type | Meaning |
|---|---|---|
| `session_id` | string | The session. |
| `computer_id` | string, optional | Its computer, absent for a chat. |

## Stream events

These two exist only on the stream: nothing in the agent loop emits them.

### `session.message`

One stored entry, sent in reply to a replay. A client renders it exactly as it
renders a message it watched arrive live.

| Field | Type | Meaning |
|---|---|---|
| `session_id` | string | The session replayed. |
| `entry_id` | string | The entry. Pass the last one you saw as `since` to resume. |
| `run_id` | string, optional | The agent turn that wrote the entry, when known. |
| `parent_id` | string, optional | The entry it follows. |
| `channel` | string, optional | Source or destination channel, absent when unset. |
| `kind` | string | `user`, `assistant`, `tool_call`, `tool_result`, `system`, `event`, `compaction`, `preamble` or `notice`. |
| `created_at` | time | When the entry was written. |
| `message` | object | The entry's stored payload: a provider message for the conversation kinds. For `preamble` and `notice`, the payload is `{"text": string}` with the stored text; a `preamble` can also hold `definition`, `{"prompt": string, "tools": [string]}`, the base prompt and tool names its runs use until a compaction replaces it. An assistant message can include `reasoning` and `metrics`; `metrics` holds `run_id`, turn `usage`, last-call `context`, `generation_ms`, `context_window`, and optional `timings`, so replay restores the context view and the speed the answer was produced at. Metrics are not sent to the provider. For a `compaction` entry it is the compaction: `summary`, `kept`, `tokens_before`, `tokens_after`, `reason`, `usage`, and on home `board_as_of`. It stands for the conversation before it, which stays in the path; the model reads the summary and the last `kept` messages in its place. |

### `bus.dropped`

The client read too slowly and lost events. It reaches that client alone.

| Field | Type | Meaning |
|---|---|---|
| `dropped` | number | How many events were lost since the last report. |
