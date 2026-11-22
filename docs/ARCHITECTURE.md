# Architecture

How Eika works today. Update it in the same change that moves structure.
Reasons for the design are in `DECISIONS.md`; wire details in `api/`.

## Domain model

```
Computer  A sandbox container with a volume of its own at /home/eika. States:
           creating -> running -> stopped (gone if the container disappeared).
           The `home` computer is the one that holds the user's durable files.
Session    A tree of entries with a head pointer and an agent definition.
Run        One execution of the agent loop from a session's head.
Project    A durable area of work with metadata and a directory on home.
```

Every agent action on files or processes happens inside a computer container
through `executor.Executor`. Model calls, search, fetch, remote MCP, and the
egress proxy are network calls and run in the harness.

## Packages

| Package | Responsibility |
|---|---|
| `cmd/eika` | Loads config, calls `server.Run`; `-web <dir>` serves the built frontend with an `index.html` fallback |
| `cmd/eikad` | Sandbox daemon; `eikad filter` runs a web_fetch filter |
| `config` | Deployment config: compose defaults, optional YAML, `EIKA_*` overrides, validation, redacted `String()` |
| `secret` | Seals stored credentials (AES-256-GCM) |
| `event` | Envelope, type constants, payload structs, `Emitter`, fan-out `Bus` |
| `server` | Composition, JSON API, event stream, auth, run manager; `server/servertest` builds and checks `docs/api/contract.json` |
| `agentdef` | Built-in agent definitions, embedded system and compaction prompts, class and prompt overrides |
| `agent` | The loop: turns, tool dispatch, queues, retries, context assembly. `Options.Gate` is asked (`Decide`, 5 s limit) before every tool call: a deny or ask returns `Blocked: <reason>` and the tool does not run; a gate that errors or times out blocks tools not marked `tool.ReadOnlyTool` and lets read-only ones run. The model is never told about the gate. `server.newAgent` sets it for the home session and every subagent |
| `provider` | Model interface, kind registry, `Complete`; `openai` implementation in two protocols (Chat Completions and the stateless Responses API); `providertest` scripted fake; `probe` identifies the backend behind an endpoint, reads its slots, window, KV cache and capabilities, and runs the Responses conformance check, with `probetest` serving its fixtures; `counter` predicts a request's token count (exact, anchored or estimated) and learns from the measured one |
| `inference` | The queue every model call waits in: pools of slots (and a shared cache), four priorities with aging, an idle gate for maintenance, optional preemption of running low-priority calls, one lease per model call; standard library only |
| `utility` | The harness's own one-shot model tasks (session titles), sent to the model of the class the task runs on |
| `imaging` | Prepares attached images: decode, EXIF orientation, fit within 1080p (Catmull-Rom), PNG or JPEG without metadata |
| `tool` | Tool interface, call context, registry; `tool/builtin` holds the built-ins and `limits.go` |
| `executor` | The interface every agent action uses, path validation; `local` (tests only), `sandbox` (eikad client) |
| `eikad` | Daemon handlers, path confinement, wire types |
| `computer` | Container and volume lifecycle, confinement |
| `egress` | Egress modes, allowlist patterns, the proxy |
| `netguard` | Public-address-only dialing |
| `store` | PostgreSQL pool, embedded migrations, queries |
| `sentry` | The permission authority behind one interface, `Gate`: `Decide` before a tool call and `Ingest` on untrusted results. Depends on no other part of Core (never `server`, `agent` or `computer`); allows everything until later packages add rules, and writes the audit log and the withheld rows through `AuditLog` and `WithheldLog` that `store` implements. `Ingest` takes segments, each cleaned, screened, wrapped and recorded on its own, with one guard call for all of them; it is the pipeline for `web_fetch` results (one segment) and `web_search` results (one segment per result, `search.Outcome.Segments` through `tool.Result.Parts`): `scan` strips invisible characters and redacts secrets, `screen` (the guard client) scores the text and a window at or over the threshold becomes `[removed:suspect]`, then the `<untrusted>` wrapper. `agent.callTool` runs it right after the tool returns and before the result is emitted or stored; a guard that does not answer passes the text wrapped, an ingest error returns `Blocked: this is not available right now`. `agent` appends `sentry.UntrustedNotice` to the base prompt in code, after any override |
| `subagent` | Named child sessions, serialized start/resume, code-owned states, cancel, reports and the running limit; run callback supplied by the caller |
| `session` | Session tree: append, head, branch, outline; `session.Store` records runs |
| `search` | Failover chain, quotas, cache; backends in `web`, `wikipedia`, `arxiv`, `github`; `fetch` reads a URL; `filter` is the JS filter |
| `mcp` | MCP client, transports, pool, elicitation; `mcp/oauth` authorization; `mcptest` fakes |
| `web/` | The frontend |

Health: `GET /healthz` (container check) and `GET /api/healthz` (frontend)
return `{"status":"ok"}`. `/api` is the JSON API.

## Endpoints, models and classes

A provider row describes an endpoint: `backend`, `pool`, `api`, `slots`, `kv`,
`kv_tokens` and the probed `capabilities`. An empty pool name uses the provider's
name. Endpoints in one pool share admission limits. `api` selects `chat` or
`responses`; only a passed Responses conformance check selects `responses`
automatically. The user can override the probed fields. Capabilities include
counting (`chat`, `text`, `none`), cache metrics (`reported`, `unknown`), slot
hints, the per-request window, build, and the Responses and slot affinity checks.
The checks record their verdict, reason, model and build.

The backend probe reads these routes. **Verified** means checked on the live
bench; the other rows describe the implemented parsers and fixtures, with no
live verification. Fixture sources are in
[the probe fixtures](../internal/provider/probe/probetest/testdata/README.md).

| Backend | Identify by | Window per request | KV cache | Exact counting | Cache metric |
|---|---|---|---|---|---|
| llama.cpp (**verified**, b11942) | `GET /props` (`build_info`, `total_slots`) | `default_generation_settings.n_ctx`, which `/slots` repeats for every slot (the per-slot context, not `-c`) | **not reported**: `/props`, `/slots`, `/metrics` and `/v1/models` carry no `--kv-unified` field and no total context, and an N-slot unified server reads the same as a partitioned one with N times the context. One slot is `shared` (it owns the whole cache either way) with size `n_ctx`; several are `unknown`, and the user says which | `POST /apply-template` then `POST /tokenize` (`{"content": prompt}`) | `timings.cache_n` and `usage.prompt_tokens_details.cached_tokens`, equal |
| vLLM (unverified) | `GET /version`, confirmed by `/metrics` series named `vllm:` or `max_model_len` in `/v1/models` | `max_model_len` of `/v1/models` | `shared` (paged blocks); size `num_gpu_blocks` x `block_size`, labels of `vllm:cache_config_info` in `/metrics`; unknown size when metrics are off. Slots are not reported (`max_num_seqs` is not exported) | `POST /tokenize` with `messages` (and `tools`, `chat_template_kwargs`) | `usage.prompt_tokens_details.cached_tokens` (needs `--enable-prompt-tokens-details`); `unknown` until a response says |
| SGLang (unverified) | `GET /get_server_info`, or `/server_info` | `context_length`, or `max_model_len` of `/v1/models` when it is null | `shared` (radix cache); size `max_total_num_tokens`; slots `max_running_requests` when set | `POST /tokenize` where the version has it (text only) | `cached_tokens` in usage where reported; `unknown` |
| TabbyAPI (unverified) | `GET /v1/model` (`parameters.max_seq_len`) | `parameters.max_seq_len` | `shared` across the batch; size `parameters.cache_size`; slots `parameters.max_batch_size` | `POST /v1/token/encode` (text, or messages with the template) | none known; `unknown` |
| Cloud (unverified) | `/v1/models` and nothing else | listing fields, per model | not applicable: `unknown` | none | `prompt_tokens_details.cached_tokens`; `unknown` |

With several llama.cpp slots the cache mode and total size are unknown until
the user sets them. A single slot owns the whole cache and is `shared`.
The effective request window is the smaller of the model's configured window
and the probe's per-request window. Settings warns when the configured value
is larger. With no probed window, the configured value applies.

### Model kinds and classes

A model has an immutable `kind`: `chat` or `decision`. Decision models are
excluded from conversation settings, model pickers and the chat model test.
The `decision` class accepts only decision models; the chat classes accept only
chat models. No current task calls a decision model.

The `model_classes` setting maps class names to model names:

| Class | Kind | Current use |
|---|---|---|
| `deep` | chat | Available assignment for agents |
| `main` | chat | Fallback for `deep` and `fast` |
| `fast` | chat | Session titles |
| `decision` | decision | No caller; its client is deferred |

Unset or deleted assignments fall back along `deep -> main` and `fast -> main`,
then to the default chat model (`default_model`, or the first chat model if that
setting names none, a deleted model or a decision model). `decision` has no
fallback. Settings reports the resolved models and pools, so classes that share
hardware are visible. Agent definitions select classes.
Titles are skipped if an unset `fast` falls back onto the model assigned to
`deep` and that model's backend is `openai`. An explicit `fast` assignment is
always used.

## Agent core

```
Run(ctx, session, text)
  turn: append user message -> turn.start
    provider.Stream(request) -> message.delta / reasoning.delta
    no tool calls? -> turn.end
    each call in order: tool.call -> executor -> tool.output* -> tool.result
    deliver queued steering messages, call the model again
  after the turn: queued follow-ups start the next turn
```

- **probe** (`provider/probe`) asks an endpoint what it is: `Run` tries the
  description route of llama.cpp, SGLang, TabbyAPI and vLLM in turn, falls back
  to a hosted API or an unknown local server, and checks the token-counting
  routes with a one-token request. The server runs it, after listing the models,
  on `POST /api/providers/probe` and for every provider at start (`Server.Run`),
  and `store.RecordProbe` writes the result in one statement that leaves the
  fields in `overridden` alone. A failed probe writes only `probe_error`.
  `probe.CheckResponses` is the one thing here that asks a model: two small
  requests (a streamed tool call with the model's reasoning, then the call, its
  result and the reasoning item sent back), which must complete, state their
  usage with a cached count, and be counted to the token by the counter. It
  runs on the explicit probe, and at start only for a stored provider whose
  recorded outcome (`capabilities.responses`) was made against another server
  build or another model than the one it would ask now; the outcome picks the
  provider's `api` (`responses` only when it passed), which a user's own choice
  overrides. `server/providers_responses.go` runs it. `probe.CheckSlotAffinity`
  is the second such check, for a llama.cpp server with several slots: three
  small requests that name slots to see whether a named slot keeps its prefix
  across a request on the other, on the same schedule, recorded in
  `capabilities.slot_affinity` (`server/providers_slots.go`). Only a pass makes
  the harness send `id_slot`.
- **inference** (`inference`) is the queue between the loop and the GPU.
  `Queue.Acquire(ctx, Need)` blocks until the call's pool has a slot (and, on
  a pool with a cache size, room: predicted prompt plus maximum output of
  every running call), and returns a `Lease` whose `Context` the model call is
  made with. The agent takes one per attempt inside `stream`, so queue time
  (`queue_ms`) is outside the response's timings, and releases it on every way
  out; a preempted attempt fails with a retryable error the retry path takes
  without spending its budget. Titles, the model test and the Responses check
  hold a lease around their call through `Queue.Do`. On a llama.cpp endpoint
  with several slots that passed the slot affinity check, the queue also keeps
  a book of what each slot of the server holds (`inference/affinity.go`): a call
  that continues a session (`Need.Affinity`) gets the slot with the session's
  prefix, `Lease.SlotHint` says which, the loop sets it on the request as
  `Request.SlotHint`, and the OpenAI providers send it as `id_slot` on both
  routes. Every other call names no slot. The server describes the
  queue from the provider rows and three settings (`server/inference.go`) and
  serves its state on `GET /api/inference`.
  `internal/modelcalls_test.go` lists the files that send a model request.
- **counter** (`provider/counter`) predicts the size of a `provider.Request`
  before it is sent. One `Counter` belongs to one session: `Predict` returns a
  `provider.Prediction` (tokens and method) and `Observe` takes the measured
  usage back, returns the drift and trains the learned parts. The methods:
  `exact` (the endpoint renders and tokenises the request: llama.cpp
  `/apply-template` then `/tokenize`, vLLM `/tokenize` with messages),
  `anchored` (the text tokenised exactly, the template's share fitted by least
  squares over the measured calls: a base, a weight per role, per tool call,
  and for replayed reasoning; the last measurement is the anchor while a
  request extends it) and `estimated` (a learned bytes-per-token ratio, with
  its running error exposed for the compaction reserve). The estimated method
  starts at four bytes per token and learns one ratio from measured calls.
  Before an anchored model has a measurement it also uses `estimated`; after
  compaction the fitted template model predicts without the old anchor.
  A request is rendered from `provider.ChatEncoder.ChatBody`, the one serialiser the
  OpenAI provider also sends, so the template sees the same messages, tools,
  thinking switch and replayed reasoning. A `Source` is the endpoint's routes
  (`Routes` over HTTP; `providertest.TokenServer` is the fake). Counts are
  cached by hash per session: a rendered prompt is cut at the control tokens
  the server names and each prefix remembered, so only the messages added since
  the last call are tokenised. Pictures are not counted by any route, so
  they are taken out of the request, the text is counted, and each picture
  size costs what a measurement showed (`anchored`), or a pixel estimate until
  one has (`estimated`): a request with a picture is never `exact`. The agent
  holds the session's counter (`agent.Options.Counter`, kept across runs by
  `server/counters.go`, which attaches the routes, and the key, through a
  `counter.Switch` for the length of a run only); `Estimate` answers without asking the server when a
  prediction cannot finish in time, `Seed` teaches a fresh counter from a stored
  measurement, and `CountText` tokenises a text of the caller's own. For the
  context breakdown it also sizes parts: `Size` counts a text (exact, or
  estimated and said so), `ToolSizes` sizes each tool by rendering the request
  with growing tool prefixes where the endpoint renders (otherwise as JSON text,
  not exact; lists over 64 tools also use text), `ImageSize` is a picture's
  learned cost, and `Peek` sizes a request for a preview without leaving a prediction for `Observe`.
  `provider.NewHTTPClient`, which the provider, the probe and the counter share,
  repeats a request whose idle connection the server had closed.
- **provider** turns a `Request` into a channel of `Event`s (text and
  reasoning deltas, tool calls, usage with optional `Timings`, done or
  error). `openai/timings.go` reads the speed fields of llama.cpp, vLLM, NIM,
  LM Studio, TabbyAPI, Groq, and Ollama. Usage is relayed as soon as a chunk
  carries it. `provider.Usage` holds six `Count`s (prompt, completion, total,
  cached, prefilled, reasoning), each a number or *not reported*, which is not
  0. `provider.ChatUsage` and `provider.ResponsesUsage` read them from a
  Chat Completions chunk and a Responses event by each field's documented
  meaning, and `provider.Tally` sums a run of calls so that a count any call
  did not state is not stated for the total.
  Hosted Responses requests ask for `reasoning.encrypted_content` when thinking
  is preserved, so reasoning can be replayed with `store: false`.
  Both protocols implement `provider.Provider`, in package `openai` on the
  same SDK client: `Provider` speaks Chat Completions and `Responses` the
  stateless Responses API (`store: false`, the whole input every call). The
  registry builds one or the other from `Endpoint.API`, which `server` takes
  from the provider row's `api`, and `Endpoint.Hosted` (a hosted API, not a
  server on the user's hardware) decides which fields a Responses request may
  carry. `Responses` borrows the chat provider's HTTP client, error
  classification, model list and `ChatBody`, so the token counter renders one
  chat body whichever protocol sends. An assistant message keeps the reasoning
  items the endpoint returned as `Message.ReasoningItems`, exactly as sent,
  beside the reasoning text; they are stored in the entry, sent back when the
  model preserves thinking, and ignored by Chat Completions, which sends the
  text. A tool call has two ids: `ID` is Eika's, minted by the loop and unique
  in the session, and `WireID` the backend's, which both protocols send back
  unchanged (`ToolCall.Wire`); entries, events and approvals use `ID`.
- **tool**: a call gets a `CallContext` (executor, emitter, ids). A
  `tool.Standalone` tool runs with a nil executor in chats;
  `Registry.Filter` narrows the shared registry per run. `bash` runs shell
  commands; `file_read`, `file_write` and `file_edit` handle bounded text files.
  `ask_user` relays subagent questions through home (see
  [Subagent questions](#subagent-questions)) and emits `question.asked`.
- **executor**: `executor.Resolve` rejects paths leaving the root;
  `executor/local` has the `!prod` tag.
- **Context assembly** (`agent/context.go`) builds every request as an
  `agent.Context`: system prompt sections (base prompt,
  instructions), tool schemas (built-in or MCP), messages, parameters, and a
  size estimate for each. Runs and `Agent.Preview` share it; a `Recorder`
  sees each conversation call that returned; summary calls do not use it.

### Inference admission and priority

Provider rows in one pool use the fewest slots and smallest known cache limit
that they report. Settings and probe results update admission limits at once;
leases already running continue. A shared cache admits calls against predicted
prompt plus maximum output. An unknown cache uses the probed request window as
its shared limit. Partitioned caches, or caches with no known size, use slots
alone. Calls count in full, even if their prefixes match. The first eligible
call by priority must fit before another can enter. A call larger than the
whole cache runs alone when the pool is empty, with a warning.

The queue supports `interactive`, `foreground`, `background`, and `maintenance`,
in that order. Current session runs and model tests use `interactive`; titles
use `maintenance`. Startup conformance checks use `maintenance`, and explicit
probes use `interactive`. Subagent runs use `foreground` when an interactive
home turn starts or messages them, else `background`. Compaction summary calls
use the run's priority. A background home turn that delivers notices uses
`background`. `Rank` is read per call: a steering or follow-up message, or a
`run` message that joins a background home turn, raises the run's next calls to
`interactive`.

A waiting call gains one priority level per `aging_seconds` (default 300).
Ties use the oldest wait, which is kept across retries. Maintenance waits until
no higher-priority call has run for `maintenance_idle_seconds` (default 120);
a pool that has never run such a call is idle. Maintenance calls can run
together. After one aging period a maintenance call no longer needs an idle
pool. `preempt_running` defaults to false. If enabled, an interactive call can
cancel running background or maintenance calls to make room. The loop retries
without backoff or retry-budget cost; `Queue.Do` has a 16-attempt bound.

Slot affinity uses a book per endpoint. A session gets its previous server
slot when free, or waits up to the time needed to read its prefix again
(`prompt / Policy.PrefillRate`, default 2000 tokens/s). It then takes another
free slot and the book follows. A new session takes the least recently used
free slot. Only a passed affinity check permits `id_slot` under either protocol.
The bench's llama.cpp b11942 check fails, so its requests name no slot.
`GET /api/inference` reports running and waiting calls and the policy. Activity
views and per-day compute aggregates are later UI work.

### Context layout and prefix stability

Requests contain the base system prompt, instructions, an agent session's
stored preamble, sorted tool schemas, and the conversation. The preamble holds
global and project instructions, active projects for home, delegable agents,
the project and brief, and the user's time zone and locale. The snapshot also
pins the agent definition's base prompt and tool names (`session.PreambleEntry`),
and runs build their system prompt and tools from them (`runConfig.withSnapshot`)
rather than from the definition as it is now. A pinned list only narrows the
definition's, so a tool a build adds waits for a compaction and one it drops is
gone at once. A run stores the first snapshot, and pins a snapshot stored before
definitions were (text alone) again with the same text; compaction stores its
replacement and summary in one transaction (`Server.refreshSnapshot`), and the
run goes on with the replacement's prompt and tools (`Options.RefreshSnapshot`).
The replacement precedes the compaction entry, so a branch from the summary
uses the replacement snapshot.
After compaction the conversation starts with its summary and retained messages.
Memory, skills and plugin catalogs are not present yet.

Both protocols serialize request fields deterministically. Stored tool arguments
and reasoning items retain their original text when JSON encoding would change
it; entry payloads use PostgreSQL `json`. Replaying a stored conversation thus
keeps its wire bytes: `internal/session/prefix_test.go` stores a conversation
with images, reasoning and awkward tool arguments in PostgreSQL, loads it back,
and checks that both providers send the same request bytes as before.
Compaction starts a new chain; its summary request reuses the old prefix.

Every user-side message ends with a `<context>` block (`agent/contextblock.go`)
that states the time it was sent to the minute, in `Options.Location` (UTC when no zone is stored), read from `Options.Clock`. It is written
when the loop first holds the message (`Run`, `Steer`, `FollowUp`). Before an
unstored message is sent, an ended question notice can be replaced; its time
stays as stamped. `Message.Context` is stored beside `Content`, so a resumed
session sends the same bytes and the prefix never changes under it. A request
joins it to the text (`Message.ModelText`); the interface, titles and the
transcript form of a summary use `Content` alone. In the home session the block
also names the channel (`web`) and carries one `notice:` line per notice the
message delivers (`Options.Notices`, asked once per message), and the message
lists the notice entry ids in `Message.Notices`.

A run is bounded by `Options.Limits` (`agent/limits.go`): 40 model calls and 24
hours, checked before each call. A run that reaches one ends cleanly, with every
tool call answered, and its last `turn.end` says which (`max_calls`,
`max_wall_time`). A tool call made three or more times in a row with the same
arguments has a warning appended to its result and still runs. Each attempt at a
model call is also watched (`agent/stall.go`): once the lease is granted, a
stream that sends no bytes for `Limits.Stall` (30 s; before the first token,
that plus twice the time reading the predicted prompt takes at the endpoint's
prefill rate) is aborted as a retryable failure, and the lease goes back. The
bytes are counted by the shared HTTP client (`provider.WithProgress`), so
comments and empty chunks count too. The rate is the endpoint's measured one
(`store.PrefillRate`, from `model_calls`), else the queue's.

Queues follow Pi: steering joins after the running tool, before the next model
call; follow-ups wait for the turn to end and start one turn each.
Aborting returns an unanswered message to its queue and answers every pending
tool call so the session stays valid. Messages accepted after an abort move to
the session's next run.

Retryable failures (408, 409, 429, 5xx, transport) back off exponentially up
to `MaxRetries`, honouring `Retry-After`, and emit `message.reset`. Others end
with `run.error`. Before every call the request is sized by the session's
token counter (`agent/counting.go`): `Predict`, bounded by `Options.CountTimeout`
(ten seconds), falls back to the counter's estimate when it cannot answer, and
never fails the call. A request of the whole window or more fails before the
call; one that fits with less room than its max output gets max output lowered
to the room left. After the call `Observe` takes the endpoint's own prompt count
back, the `ModelCall` handed to the `Recorder` carries the prediction, its
method and its drift, and an `exact` prediction with any drift is logged as a
warning with its numbers. A session resumed with a counter that has learned
nothing is seeded from the newest measurement its messages carry.

The window every one of those checks uses is `Options.ContextWindow`, which the
server sets to the model's effective window: the smaller of its row's
`context_window` and the per-request context its provider's probe reported
(`server/window.go`). With no probed window it is the configured one.

**The context breakdown** is not computed by the loop. When a client asks for
the context of the next request or of a recorded call (`server/calls.go`,
`server/breakdown.go`), `agent.MeasureParts` counts each part of it (the
sections of the system prompt, each tool, each message and its replayed
reasoning) with the session's counter, remembered by content hash, and the
server states the remainder, `prompt - sum(parts)`, as the template and
formatting: against the measured prompt for a recorded call, against the
prediction for the next request when that is `exact`, and absent otherwise.
Nothing is scaled and a negative remainder is returned as it is. The status
bar's meter reads `GET /api/sessions/{id}/prediction`, which sizes the request
once without counting the parts.

**Images** are `provider.Message.Images` on a user message.
`POST .../messages` runs each through `imaging.Prepare` before the run is
claimed; `runs.begin` refuses images for a model without `image_input`, and
`enqueue` checks the running model's. `Options.ImageInput` decides whether
`Context.WithMessages` sends them or a note in their place, `messageTokens`
counts each at one token per 750 pixels rather than its
base64 (`counter.EstimatedImageTokens`), and the OpenAI provider sends them as `image_url` data-URL parts
before the text. `turn.start` carries them for the live transcript.

**Compaction** (`agent/compaction.go`, `agent/summary.go`) is Pi's. With
`CompactionSettings.Auto`, a call whose predicted size passes `window - reserve`
compacts first (when the prediction is only `estimated`, the reserve grows by the error estimates have shown, to at most half the window), and a call the endpoint refuses as too large
(`provider.ContextOverflow`, or a silent truncation) is compacted and sent
again, once. `Agent.Compact` compacts on request. `planCompaction` walks back
from the newest stored message until about `keep` tokens are kept and cuts
at a user or assistant message; a cut inside a turn summarizes the turn's
start apart. The home session cuts at a user-turn boundary instead
(`CompactionSettings.KeepRecentShare`, 15% of the window, set by `newAgent`
for home only): `planTurnCut` keeps from the oldest user message whose tail
fits the share, and at least the newest turn. A newest turn larger than the
share, or a tail with no user message, falls back to `planCompaction` with
the share as its budget, which splits the turn. Threshold, overflow and
manual compactions all cut this way. The summary is asked for first as the run's own request up to
the cut plus a prompt, which the endpoint's prompt cache already holds, and
else as Pi's serialized transcript; a cut-off or empty answer is asked for
again at `FallbackEffort`. The conversation becomes a summary message
(`Message.Summary`) followed by the kept messages, the store gets one
`compaction` entry, and `compaction.start` and `compaction.end` go out.
Reserve and keep are capped at a quarter of the window.

The home session's summary has two sections (`CompactionSettings.Board`,
which `newAgent` sets for home only). `[Subagents]` is the board:
`subagent.Board` renders it from the children's rows once the model has
answered, under a heading with the time it holds for. Running and waiting
children come first, then those active since the previous board was read (its
time is the compaction's `board_as_of`; an older compaction counts from its
entry's time), each with agent, project, state, last activity, waiting reason and
the first line of its last report; one line counts the rest. `[Conversation]`
is the model's: Eika's summary prompt writes it, and each later compaction asks
Eika's update prompt to fold the new messages into it, with the previous
summary in the cached prefix. The prompt asks for six headings: Commitments and
open loops, People and context, Preferences and corrections, Decisions, Recent
topics, and Next. It asks for absolute dates; exact names, numbers, amounts,
IDs and paths; and subagents and projects by name. It keeps sections concise
and does not repeat the board. The update prompt keeps facts that are still
true or open, adds new commitments and context, removes resolved or irrelevant
items, and updates Next. A board that cannot be read fails the compaction
as a failed summary does, leaving the session as it was. Subagent and routine
sessions keep Pi's summary with their definition's prompts. Home wraps its
summary in a tag with `as_of`, the compacted entry sequence span and the
compaction count. A footer points to `history_search` and `history_read`.
`store.HistoryState` folds the stored path to find the span and retained
messages; `CompactionSettings.SummarySpan` reads it for home before the
compaction is stored.

The server checks for idle home compaction once a minute. It starts at
`maintenance` priority when no run is active, important work has been idle for
`compact_idle_minutes` (default 30), and the home context exceeds
`compact_idle_fill` (default 60%). Important work can cancel the idle
compaction; a failed or cancelled compaction leaves the stored context intact.
Threshold and overflow compaction remain in place.

### History recall

`session_entries.tsv` indexes user and assistant text, tool-call names and
arguments, notices and compaction summaries with PostgreSQL full-text search.
Tool results stay in the entry store but are not indexed. The home-only
`history_search` tool ranks matches, filters by session, project and time, and
returns five groups per page. Each group contains the hit and nearby turns,
with limits on entry count and output size. Without a query it browses entries
before the oldest retained message. Entries show readable message text and tool
calls without reasoning or usage metadata. Long hits show an excerpt near the
literal query when possible. `history_read` reads an entry or a window,
including tool results, and can return full text. The server binds both
through the builtin registry to the store.

### Model-call accounting

`model_calls` records the requests made by a session's loop. It does not record
utility calls, probes or compaction summary calls. The latter take queue leases
but are not yet observed by the counter or recorded. Counts are NULL when no
source supplies them; a stated zero stays zero. A run total is unknown for a
field if any call did not report that field.

| Stored field | Meaning and source |
|---|---|
| `prompt_tokens` | Server's prompt count, including cached tokens; `input_tokens` under Responses |
| `cached_tokens` | Usage details first, else llama.cpp `timings.cache_n`; NULL if neither is stated |
| `prefilled_tokens` | `timings.prompt_n`, else prompt minus cached when both are known |
| `completion_tokens`, `total_tokens` | Server's generated count (reasoning included) and stated total |
| `reasoning_tokens`, `reasoning_source` | Reported reasoning count, else tokenizer count of reasoning text; source `reported` or `tokenised`. The latter is approximate and stays out of usage events |
| `predicted_prompt_tokens`, `prediction`, `drift` | Counter prediction, method, and predicted minus measured prompt; drift is NULL unless both counts exist |
| `context_window` | Effective window used to judge this request |
| `queue_ms`, `slot`, `priority` | Wait summed across attempts, server slot named by the request (NULL when none), and requested priority |
| `endpoint`, `class`, `agent` | Provider name at call time; class and agent remain NULL until session runs use them |
| `prefill_ms`, `decode_ms` | Endpoint timings; decode falls back to harness time from the first token |

The meter's last-call fill is `prompt_tokens + completion_tokens`, against the
effective window. Before a measurement, fill and cache read `n/a`. The predicted
next request shows its method, with a tilde for `estimated`. Settings,
Developer shows request parts and drift.

Usage parsing has the following verification status. The live recordings and
unverified fixture sources are in
[the usage fixtures](../internal/provider/testdata/usage/README.md).

| Backend | Prompt | Cached | Reasoning | Status |
|---|---|---|---|---|
| llama.cpp b11942, `/v1/chat/completions` | `usage.prompt_tokens`, **includes cached** | `usage.prompt_tokens_details.cached_tokens` = `timings.cache_n`; `timings.prompt_n` is the processed part | none: NULL. `completion_tokens` includes the reasoning | **verified** on the bench, streamed, cold and warm, reasoning on: prompt 90 = cache_n 0 + prompt_n 90 cold, 90 = 86 + 4 warm; a request cut off at 20 tokens inside the reasoning had `completion_tokens` 20 |
| llama.cpp b11942, `/v1/responses` | `response.usage.input_tokens`, **includes cached** | `input_tokens_details.cached_tokens` = `timings.cache_n`, and `timings` is a sibling of `response` in the `response.completed` event | none: NULL (no `output_tokens_details`). `output_tokens` includes the reasoning | **verified**: the same numbers as `chat` for the same prompt (90 tokens, 0 then 86 cached); output 107 over 77 reasoning and 27 answer chunks, and 20 for a response cut off inside the reasoning |
| OpenAI and other cloud APIs | `prompt_tokens` / `input_tokens` | `prompt_tokens_details.cached_tokens` / `input_tokens_details.cached_tokens` | `completion_tokens_details.reasoning_tokens` / `output_tokens_details.reasoning_tokens` | unverified, from the API reference |
| vLLM | `prompt_tokens` / `input_tokens` | `prompt_tokens_details.cached_tokens`, only with `--enable-prompt-tokens-details` (else `null`: NULL) | none on chat; `output_tokens_details.reasoning_tokens` on responses | unverified, from the source |
| SGLang | `prompt_tokens` | `prompt_tokens_details.cached_tokens`, only with `--enable-cache-report` | none read: its top-level `reasoning_tokens` is 0 whether or not reasoning was counted | unverified, from the source |
| TabbyAPI | `prompt_tokens` | none | none | unverified, from the source |

## Persistence

`store` owns the database: a pgx pool, embedded migrations applied by `Open`,
and SQL in one file per entity. Its tables live in schema `core` (every
connection's `search_path`). Forward migrations extend the baseline schema.
Missing rows are `store.ErrNotFound`,
duplicates `store.ErrConflict`. Ids are `store.NewID` text.

| Table | Holds |
|---|---|
| `computers` | name, image, state, container_id, sandbox (jsonb: limits, egress), home |
| `sessions` | computer_id, title, untitled, kind (home/subagent/routine), owner, agent, name, project_id, parent_session_id, state, waiting_reason, brief, result, report_notice_id, last_active_at, head_entry_id, pinned, archived |
| `session_entries` | session, parent_id, seq, kind, channel, payload (json: the text it was written as, so a message reads back as the bytes it was sent as), tsv (generated search index) |
| `runs` | session, state, started/finished, error |
| `settings` | key → jsonb value |
| `providers` | name (unique), kind, base_url, api_key (sealed), and what a probe found: backend, pool, slots, kv, kv_tokens, api (`chat` or `responses`), capabilities (jsonb, with the Responses check's outcome), overridden (the fields the user set, which a probe keeps), probed_at, probe_error |
| `models` | provider, name (unique), kind (chat or decision), model (endpoint id), context_window, max_output, reasoning_effort, reasoning_efforts, thinking_switch, preserve_thinking, image_input |
| `agents` | built-in name, nullable class and prompt overrides |
| `projects` | stable slug, name, description, status, sandbox directory, timestamps |
| `retired_profiles`, `retired_session_profiles`, `retired_profile_defaults` | snapshots of profile rows, session choices, and the default profile setting from migration 0021; not read by the runtime |
| `model_calls` | session, run, entry, model, sections, tools, parameters (jsonb); the measured counts (prompt, completion, total, cached, prefilled, reasoning with its source), the prediction (tokens, method, drift), the window and the endpoint; NULL wherever a number is not known; the queue fills `queue_ms`, `slot` (the server's slot the request named, NULL when it named none), `priority` (and `class` where a run is resolved from one), `prefill_ms` and `decode_ms` come from the endpoint's timings, and `agent` stays NULL until the agents fill it |
| `audit` | Sentry's append-only log (migration 0022): event, session, run, subagent, routine, computer, action key, kind, name, argument digest, reason, detail (jsonb). A trigger refuses update, delete and truncate; no foreign keys, so rows outlive their session |
| `withheld` | text ingest removed from a result (migration 0023): source kind and name (the URL), session, run, call key (matches the tool card), wrapper id, text, score. No foreign keys |
| `auth_password`, `auth_sessions` | PBKDF2 hash; session tokens by SHA-256 |
| `search_keys`, `search_usage` | sealed search keys; quota counters and cooldowns |
| `mcp_servers`, `mcp_credentials` | configured servers (sealed headers/env); OAuth client and tokens (sealed) |

A computer's sessions are deleted with it; MCP credentials cascade from their
server. `sessions.head_entry_id` has no foreign
key (it would be a cycle); `SetSessionHead` checks ownership in its `UPDATE`.

`AppendEntry` locks the session row, inserts with the head as parent and
`max(seq)+1`, and moves the head, so concurrent appends form one chain.

### Session trees

Migration 0014 adds `home`, `subagent` and `routine`, agent and name identity,
project and parent references, lifecycle state, brief, result and last activity.
Home identity is unique by owner and name. Subagent names are unique per parent.
Migration 0021 converts old user sessions to routines with agent `eika` and a
name from the title or ID. It keeps each session ID, computer, title, head and
entry tree. Profile rows, session profile IDs, overrides, tool choices and the
default profile setting are saved in the three `retired_*` tables. They are not
applied to agents. The old columns and live profile table are removed.

Old entries keep their kind and exact JSON. The entry-kind constraint is `NOT
VALID`: it rejects new obsolete kinds without rewriting old entries. Entries can
store a channel, a `preamble` snapshot or a `notice` text payload. A notice is
delivered by the user entry whose payload lists its ID (see [Notices](#notices)).

`session.Tree` appends, reads `Path` (root to head), moves the head, and renders an `Outline`. `session.Store` implements
`agent.Store`; `Load` rebuilds a conversation from the path.

`user`, `assistant`, and `tool_result` entries are the conversation; `system`
and `event` entries are shown but not sent. A `compaction` entry holds an
`agent.Compaction`: `session.Messages` folds the path, so each one replaces
the conversation before it with its summary and the last `kept` messages.
The entries it covers stay in the tree, so a head moved above it
has the whole conversation. A user entry may hold `images` (base64 PNG or
JPEG with their size), stored inline as the model is sent them. An assistant entry holds the
message JSON (with tool calls, reasoning, and measured `metrics`).

```
branch in place (head moved to e2)
  e1
  e2
  |\
  e3 e5 <- head
  e4
```

Moving the head requires a resumable entry (`session.PathResumable`: no tool
call left unanswered and the entry is not a preamble). The outline reports it
per node. A session cannot be copied into another.

### Chats

A chat's run gets no executor. `sessionTools` drops
every tool that `NeedsComputer`, then applies the tool choice, so the model is
never offered a computer tool; the loop also refuses one. The system prompt
is the same base prompt every session has. web_fetch refuses filters in a chat.

## Server

```
cmd/eika -> server.Run
  store.Open, secret.Load, computer.NewHost,
  provider.NewRegistry(openai.New, openai.NewResponses), builtin.Registry, event.NewBus, NewMCP
  -> server.New(Deps) -> Reconcile -> Serve

POST /api/sessions/{id}/messages          GET /api/events
  runs.start: StartRun, session.Store,      bus.Subscribe(topics)
  configure + newAgent                             ^
  goroutine agent.Run --entries--> postgres        |
                      --events---> event.Bus ------+ one buffered channel
  FinishRun(done | error | aborted)                  per connection
```

- **Deps**: store, computer host, provider registry, secret box, tool
  registry, question broker, bus, MCP pool. `Computers` and `Providers` are
  interfaces declared here.
- **Auth** is one middleware over `/api`. A bearer token is a sign-in
  session (looked up by SHA-256) or the API token (constant-time compare).
  Public: health, `GET /api/auth/status`, `POST /api/auth/setup` (once),
  `POST /api/auth/login`, `/oauth/client-metadata.json`. `/api/events` also accepts `?token=`. Sign-in
  attempts are serialised.
- **Run configuration** (`run_config.go`): the agent definition supplies the
  prompt, fixed tools and class. The class selects a chat model; the model row
  supplies sampling and thinking settings. Runs and context previews use the
  same resolution. An explicit model request is refused.
- **Settings** the harness reads (`default_model`,
  `model_classes`, `sandbox_image`, `sandbox_limits`, `sandbox_egress`,
  `search_order`, `search_limits`, `setup_complete`,
  `compaction`) are
  validated on write and fall back to defaults on read. Renaming a model
  rewrites the settings that name it.
- **Compaction** (`compaction.go`): each run gets `agent.CompactionSettings`
  from the `compaction` setting, with `FallbackEffort` the lowest effort the
  model offers. `POST /api/sessions/{id}/compact` starts a run whose job is
  `Agent.Compact` instead of `Agent.Run`: it claims the session like any run,
  takes no queued messages, and is `409` when `Compactable` finds nothing to
  summarize.
- **Model kinds and classes** (`classes.go`): a model row has a `kind`,
  `chat` or `decision`, fixed when it is added. A decision model is never a
  chat model: `defaultModel`, the run configuration (`configBase.models`),
  and the setup's model count see chat models only, and the
  model test refuses one, since its request is a chat. The harness's own
  tasks name a class, not a model. The `model_classes` setting maps `deep`,
  `main`, `fast` (chat models) and `decision` (a decision model) to model
  names; `resolveClass` applies the fallbacks `deep -> main`,
  `fast -> main` and, for a chat class left with no model, the default model,
  and `GET /api/settings` reports each class with the model
  and pool that serve it, so the UI never repeats the rule. Nothing asks
  for `decision` before Sentry. Each agent selects its class.
- **Titles** (`titles.go`): a session created without a title is
  `untitled` and called `New session` or `New chat`. When a run begins on
  one, `titles` starts a goroutine (at most one per session, cancelled and
  joined by `Close`) that resolves the `fast` class (`titleModel`: it
  falls back to `main` and then the default model, and is skipped when it
  would fall back onto the
  model assigned to `deep` and that is a hosted API), builds its provider,
  and calls `utility.Title` with the
  first user message: one request, no tools, effort `none` in the model's
  thinking switch when it offers `none`. `TitleUntitledSession` writes the
  title only if the session is still untitled, then `session.title` goes out
  on `global`. No model, a failure, or a timeout leaves the session untitled
  for the next run.
- **Errors**: one shape, `{"error":{"code","message"}}`. `statusOf` maps
  package sentinels to 404/409/400; `mcpFailure` reports MCP server failures
  as 400. Anything unmapped is logged and returned as `internal error`.
- **Run manager**: one goroutine per run, one run per session (a
  placeholder is reserved under the lock before slow work). The client gets
  the run row immediately and follows events. `FinishRun` retries on a
  context that outlives the run and continues in the background past 30 s.
  Startup aborts stale `running` rows. Stopping or deleting a computer or
  session, and shutdown, abort the runs involved first.
- **Bus**: `Emit` never blocks; a slow subscriber loses events and is told
  with `bus.dropped`. Topics: `global`, `computer:<id>`, `session:<id>`.
- **Event stream**: a reader goroutine handles `subscribe` and
  `session.replay`; replays write straight to the socket.

Routes are in `routes.go`, one handler file per resource.

## Configuration

Deployment config (`config.Config`) is loaded once in `main`:
`config.Default()` → YAML from `-config` → `EIKA_*`. Defaults match the
compose stack, which sets only `EIKA_DATABASE_URL`. Notable fields:

- `sandbox_network` (`eika_sandbox`): empty means eikad is published on
  loopback (`make dev`). `sandbox_internal_network`
  (`eika_sandbox_internal`) must differ from it and, with `egress_listen`
  (`:3128`) and `egress_proxy_url` (`http://eika:3128`), enables restricted
  egress. Either network empty disables it.
- `allowed_origins`: extra origins for the event stream and MCP redirects,
  after the one derived from `listen`.
- `public_url`: when https, serves the OAuth Client ID Metadata Document.
- `guard_url` (`EIKA_GUARD_URL`, default `http://guard:9940`): the guard that screens untrusted content; empty turns the screen off. `screen_threshold` (default 0.9): the score at or over which a window is removed.
- `auth_token`: optional fixed API token.

Everything a user chooses is a database row. Stored credentials are sealed
with the 32-byte key in `secret_key_file` (`/var/lib/eika/secret.key`,
created on first start). The API reports only whether a credential is set
and, for long keys, the last four characters.

## Sandboxes

```
 harness (eika)                              computer container eika-cx-<id>
  tool -> executor/sandbox --HTTP, bearer--> eikad :7000
  mcp.Pool -> Host.Process --------------->    /exec /files /stat /list
  Host.Process --------------------------->    /process /environment
  computer.Host ---------docker socket--->  container + volume at /home/eika
  egress.Proxy :3128 <-----------------------  HTTP(S)_PROXY, when restricted
```

### eikad

Reads `EIKAD_TOKEN`, refuses to start without it, and serves via
`server.Serve`. Every path is confined to `/home/eika`: `..` is rejected, the
longest existing prefix is resolved through symlinks before comparison, and
the canonical path is what is opened. `/home` holds other directories, so a
sibling such as `/home/eikaother` is outside, however it is spelled. `EIKAD_TOKEN` is stripped from command
environments. A command leads its own process group, killed on timeout;
output is capped and reported as truncated. API: `api/eikad.md`.

`executor/sandbox` is an HTTP client for one daemon; `Exec` streams the
daemon's newline-delimited frames into the caller's writers. A 404 is
`sandbox.ErrNotFound`.

### Files

The API has no routes for a computer's files, and there is no terminal and no
preview: the person using a computer reaches it only through the agent, whose
tools call the executor. The executor checks a path with `executor.Resolve`
and eikad re-checks it after following symlinks. Text tools return at most
32 KiB per read and accept writes and exact single-match edits up to 1 MiB.
They reject invalid UTF-8, NUL, ambiguous matches and oversized files rather
than return partial text. Both executor implementations replace files with a
temporary sibling and rename through rooted filesystem operations. Failed
writes leave existing file contents intact. See [the tool contract](api/tools.md).

### Computer lifecycle

`computer.Host` wraps the Docker client. `Create` creates volume
`eika-cx-<id>` mounted at the root, creates the container labelled
`eika.computer=<id>` with its sandbox settings, and copies the harness's
static eikad to `/usr/local/bin/eikad` as the entrypoint. `Start` waits for
`/healthz`. `Stop` keeps the volume. The harness reaches eikad at `http://eika-cx-<id>:7000`.
A container gets `HOME` set to the root, and before it starts the daemon
extracts the root directory into it owned by uid and gid 1000, the user every
container runs as, so an
image with no such directory, or one root owns, still has a root the user can
write. A volume that exists when `Create` runs is reattached, and a `Create`
that fails does not remove it: this is how a lost container comes back with
the files it had.

### The home computer

`Server.EnsureHome` (`server/home.go`) is the one way the `home` computer
comes to be. `Server.Run` starts it beside the idle stopper on every start,
so setup, a deployment with a fixed `EIKA_AUTH_TOKEN` that never calls setup,
and a restart all reach it. It inserts the `computers` row first, which the
unique index on `home` allows once, and then creates the container over the
row's id when `Inspect` finds none (dropping the proxy token the harness
cached for that id, since the new container has its own) and starts it when it
is stopped. A failure
(no Docker, no sandbox image) is logged and tried again after 5 seconds, then
10, up to a minute, without keeping the API from serving; meanwhile the row
reads `creating`. A run starts its computer first when the idle stopper has
stopped it (`runExecutor`), and a run in `home` calls `EnsureHome` when its
container is gone; a computer that cannot be started refuses the run with a
`409` naming the cause. The API has no route that creates, changes, stops or
deletes a computer: `GET /api/computers` lists them, and the settings change
`home`'s limits and egress (below).

The server's idle stopper (`server/idle.go`) sweeps once a minute while
`Server.Run` serves and stops every running computer (aborting its runs, ending
its stdio MCP servers) that has not had a run in it for
`computer_idle_minutes` (default 15, 0 for never). Last use is in memory:
a run's end, a start, or a sweep that sees a run going; after a restart a
running computer gets the whole timeout again.

`List` finds containers by label and `Inspect` recovers tokens from the
container's environment. `Reconcile` updates recorded states at startup and
marks a computer `gone` only when Docker says the container does not exist;
any other error stops reconciliation without changing states.

### Limits and egress

`Host.Create` applies the computer's `sandbox` row. Saving `sandbox_limits` or
`sandbox_egress` in the settings writes the home row and then `Host.Confine`s
its container (`Server.applyHomeSandbox`); every start re-applies the row.

Limits are `NanoCPUs`, `Memory` (= `MemorySwap`), and `PidsLimit`, changed
live with `ContainerUpdate`; "no limit" on an existing container is the host's
capacity (`Host.Capacity`).

```
 open: eika-cx-a on eika_sandbox --------------------> internet directly
 allowlist/none: eika-cx-b on eika_sandbox_internal (no route out)
     HTTP(S)_PROXY=http://<computer id>:<proxy token>@eika:3128
     -> egress.Proxy -> allowlisted public hosts only
```

`egress.Proxy` takes `CONNECT` and absolute `http://` requests, identifies the
sandbox from `Proxy-Authorization` (the computer's id and a proxy token of its
own, made at create and read back by `Inspect`), asks `Server.EgressPolicy` per request,
dials through `netguard`, and answers refusals with 403, remembered per
computer in memory (`Proxy.Blocked`, which no route reads now). It serves only when both networks exist
(`Host.EgressControl`). Switching modes connects the container to the new
network before leaving the old. Proxy variables (`HTTP_PROXY`, `HTTPS_PROXY`,
`NO_PROXY` in both cases, `NODE_USE_ENV_PROXY`) are given to eikad with
`PUT /environment` on every change and start.


## Search

```
web_search -> search.Engine
  "web": first usable of search_order (searxng, exa, tavily, brave, marginalia)
  wikipedia | arxiv | github_code / github_repos / github_issues
  Tracker: quotas and cooldowns per bucket -> search_usage; keys from search_keys

web_fetch -> fetch.Reader -> plan URL: GitHub API/raw | text file | HTML
  HTML: doc-generator container, else readability, else body -> Markdown
  cache -> section -> filter (eikad filter in the sandbox) or budget
```

- A search asks for a pool of 30, cached a day; the model sees at most 10.
  A failing provider records an attempt and the next is tried; when SearXNG
  failed and a fallback answered, the result opens with a `Notice:` line.
  An empty `search_order` disables web search.
- arXiv is paced 3 s apart. GitHub sources and web_fetch's GitHub reads share
  the `github` bucket. The GitHub token is sent only to the GitHub API and
  `githubusercontent.com` over https.
- Pages are cached 6 h. Sections match by heading (exact, prefix,
  substring). Past 10,000 tokens a plain read returns the outline; a narrowed
  one is cut on a section boundary.
- Filters run as `eikad filter` through the run's executor, in goja with a
  2 s limit, budgeted inside the sandbox.
- `fetch.NewClient` dials only public addresses (checked after DNS, per
  redirect, at most five) and uses no proxy.

The Search settings tab shows each backend's state and usage, orders
providers, stores keys, edits quotas, and tries `POST /api/search`.

## MCP

```
runs.begin -> mcp.Pool.Tools(ctx, computer)
  remote: one conn per server, ConnectHTTP
    modern (server/discover) -> initialize -> HTTP+SSE; Bearer from mcp_credentials
  stdio: one conn per server per computer
    Launcher -> Computers.Process -> Host.Process -> eikad /process -> command
  catalog: tools, resources, templates, prompts
  events: mcp.server on global, mcp.elicitation on session:<id>
```

- **Eras**: modern requests carry version and capabilities in `_meta`; an
  `input_required` answer is satisfied and resent, up to eight rounds.
  `Client.Info` records era, transport, and version.
- **Transports**: `http.go` (Streamable HTTP, modern `Mcp-*` headers, older
  session ids), `legacysse.go` (same-origin endpoint), `stdio.go`
  (newline-delimited JSON).
- **Pool**: remote servers connect in the background at start; concurrent
  callers share an attempt; a failure stands for 30 s. Modern connections
  hold `subscriptions/listen` open. `Store` (implemented by `server` as
  `mcpStore`) keeps sealed values away from the pool.
- **Stdio servers** end with their computer (after its runs). Their
  environment is visible to the agent.
- **Tools**: `Pool.Tools` waits at most 15 s and returns
  `mcp__<server>__<tool>` (hashed to 64 chars), plus `mcp_list_resources`
  and `mcp_read_resource` when a server has resources. Remote tools are
  standalone; stdio tools need a computer. `Pool.Offered` lists tools from
  the last listing without connecting (for `GET /api/tools` and previews).
  Progress becomes `tool.output`; images and audio are replaced in the model's
  text and kept (≤ 4 MiB) in `tool.result` details. An expired session or
  refreshed token gets one retry.
- **Authorization** (`auth.go`, `oauth`):

  ```
  POST .../authorize {redirect_uri}  -> check <UI host>/mcp/callback
    discover: WWW-Authenticate, resource metadata, RFC 8414 / OpenID
    client: configured id | metadata document | dynamic registration
    PKCE S256, state, scope, resource; pending 10 min -> {authorization_url}
  browser signs in -> /mcp/callback -> POST /api/mcp/oauth/callback
    check iss (RFC 9207), exchange, seal tokens, reconnect
  ```

  Tokens refresh a minute before expiry and after a 401; `insufficient_scope`
  steps up with the union of scopes; signing out revokes both tokens.
- **Elicitation**: `mcp.Elicitations` registers the request, emits
  `mcp.elicitation`, and waits for `POST /api/elicitations/{id}/answer`.
  Server stderr and log notifications go to a 200-line per-server log.

Bounds are in `internal/mcp/limits.go`.

## Compose

```
              127.0.0.1:8080
                    |
 host.docker. <-- eika (harness) ----- searxng     (eika_default)
 internal           |      \---------- postgres 16 (volume eika-postgres)
 (local models)     | docker.sock: sibling sandboxes
                    | volume eika-data -> /var/lib/eika (secret.key)
                    +-- eika_sandbox / eika_sandbox_internal: sandboxes only
                    +-- eika_guard (internal) ----- guard (injection screen)
```

Only the harness publishes a port, on loopback. `compose.dev.yaml` publishes
postgres and searxng on loopback for `make dev`; `compose.smoke.yaml` runs the
stack for `make smoke`. The `sandbox-image` service builds
`eika-sandbox:latest` and exits; the harness waits for it. The `guard`
service (`guard/`) is the injection screen: CPU torch and the model baked into
its image, on the internal `eika_guard` network that only the harness joins.
The harness does not wait for it, and `EIKA_GUARD_URL` points at another
instance. Nothing calls it yet. The root
`Dockerfile` builds the frontend, both static Go binaries, and a runtime image
with eikad at `/usr/local/share/eika/eikad`. `deploy/eika-entrypoint.sh`
joins the socket's group and drops to uid 1000. `deploy/searxng/settings.yml`
enables JSON results.

## Frontend

The frontend uses React, Vite, TypeScript, Tailwind, shadcn with Radix,
TanStack Query, Zustand and React Router. UI pages are registered in
`app/pages.ts` and routed in `app/App.tsx`: Chat at `/`, Activity at
`/activity/:sessionId?`, and Projects at `/projects/:slug?`. The MCP OAuth
callback has its own route. `docs/EXTENDING.md` describes adding a page.

The production build includes a web manifest and service worker. The worker
precaches only the build's shell files. Navigation uses the network first and
falls back to the cached index when offline. API and event requests bypass the
worker. Each build has its own cache; activation removes older shell caches.
The worker is registered in production, except in the mock test build. The
server requires revalidation of frontend files, including the manifest and worker.
Reports and background replies sent through ntfy link to Chat.

The shell shows one live toast at a time for completed subagent reports and
background home replies. Report updates carry the report id; home turn events
identify replies. The queue is local to the page and is not restored on reload.
Toasts link to Activity or Chat and do not appear when that content is open.
On a phone the toast sits at the bottom of the page, above the bottom bar; a
report toast carries the first line of the report.

A subagent's question reaches Eika as a notice whose text is written for the
model. The notice entry also stores a structured `question` (asker, question,
options, question id; `session.QuestionNotice`), which the entry wire type and
the replayed `session.message` payload carry. The transcript shows those fields
and never the model-facing text; the model still receives the text unchanged.

A signed-out browser that cannot reach Eika to ask whether it is set up sees the
offline screen (`features/connect/OfflineScreen`), with Retry and a way to the
sign-in screen's address field, not a password form. Links that must be
touch-sized carry the `touch-link` class.


```
web/src/
  app/         routes, shell, navigation, theme
  features/    one folder per feature, imported through its index.ts
  components/  shared components; components/ui is shadcn
  api/         wire types, HTTP client, event stream
  lib/         pure utilities
```

- **Entry** (`app/App.tsx`): no token → `GET /api/auth/status` → setup
  (`features/setup`) or sign-in (`features/connect`). With a token the setup
  shows until a password exists and `setup_complete` is true.
- **Settings** (`features/settings`): providers and models, agents,
  instructions, model classes, the computer's limits and network,
  search, MCP, password. Agents (`features/agents`) edits each built-in
  agent's class and prompt through `/api/agents`, with a chip per field naming
  where its value comes from and a diff against the built-in prompt;
  Instructions saves the user's About me and rules through `/api/instructions`.
  Developer lists sessions and recorded calls and opens the context inspector,
  which shows each request by part and its token accounting. The status bar
  also opens it for the current session. Setup requires a main class; an
  agent can also run with its own assigned class. Migration 0020 assigns main
  to the existing default chat model when main is unset. The UI makes no profile or session configuration
  request. Small stores hold which settings tab is open.
- **Shell** (`app/Shell.tsx`): the frame of every page. A rail down the left
  from 640px (`app/Navigation.tsx`) and a bottom bar under it list the pages
  of `app/pages.ts`, then Settings, which opens the existing dialog; the
  header carries the page's title and the theme toggle. Both navigation
  layouts use a `nav` landmark labelled Pages. Controls use labelled
  inputs or buttons, keyboard navigation and dialog focus handling. The shell is `100dvh`,
  pads the device's safe areas (`pt-safe`, `px-safe`, `pb-safe` in `index.css`)
  and sits on `--keyboard-inset`, which `lib/useViewportAnchor` writes from
  `window.visualViewport` where the browser does not resize the page itself
  (Safari); the bar gives way while a keyboard is up. `app/HaltSlot.tsx` is
  the empty place phase 4 mounts the halt button, in the rail and, on a phone,
  the header. Pages not built yet are absent, not disabled.
- **Chat** (`features/chat`): the page at `/`. It reads `GET /api/home` and renders `features/session`'s
  `SessionView` for the home session, so the transcript, the composer and the
  status bar are the session view's. On a coarse pointer the composer uses a
  plain textarea and a rendered markdown preview; Enter inserts a newline and
  the send button sends. On a fine pointer it keeps the keyboard modes and
  marked-up source field. Appearance can override the pointer choice. The
  touch composer limits its height to the visible viewport and scrolls its
  editor and preview. Escape aborts an active run from the session view, except
  when the key belongs to a dialog or text field. While a run is active, the
  composer can steer it or queue a follow-up and shows the queues. There is no list of chats and no way to
  start one. The status bar's meter shows the last call's fill against the
  effective window, its cached tokens, and the predicted next request with its
  method. The UI has no panel for a computer's files, no terminal, and no
  session tree or fork, and the API has no routes for a computer's files or a
  session fork; rewind uses the outline and head routes.
- **Activity** (`features/activity`): the page at `/activity` and
  `/activity/:sessionId`. The board lists the subagents (`GET /api/sessions`,
  kind `subagent`) by their code-owned state: Waiting with its reason, Running,
  Idle, then Failed and cancelled. It follows `session.updated` on `global`, and
  reads again after a reconnect or a `bus.dropped`. A subagent opens as
  `features/session`'s `SessionView` with `readOnly`: the transcript streams, and
  there is no composer, status bar, abort key or rewind, and a question or MCP
  request in it takes no answer. Cancel is
  `POST /api/sessions/{id}/cancel` (the lifecycle service's cancel); steering goes through
  Eika in Chat.
- **Projects** (`features/projects`): the list and project detail show metadata
  and the project's subagents. Detail tabs are Overview, Instructions and
  Subagents; the instruction editor saves user text, and subagents link to
  read-only Activity views.
- **Touch density** follows the primary pointer: type tokens increase one step
  and controls have 44 px minimum targets. The transcript loads older entries
  on demand without a virtualiser: on mobile emulation at 5,000 entries a scroll
  held its frame rate and a page render took about 30 ms (`DECISIONS.md`,
  "Frontend"). It was not measured on a physical phone.
- **Server state** is TanStack Query over `api/routes.ts`, invalidated by
  events (`useSessionStream`, `useMCPEvents`,
  `useSessionUpdates`).
- **Stream state**: `features/session/store.ts` wraps the pure reducer
  `transcript.ts`, fed by run events (`run_id`) and pages of stored entries
  (`entry_id`). The newest page loads after the stream opens; catch-up reads
  back to a known entry after turns and reconnects. The session outline selects
  the current branch from those pages. Older pages load on demand, and the
  visible row keeps its position. The transcript follows the stream while the
  user is at the bottom; scrolling up stops following and offers Jump to latest.
  A polite live region announces the start of a reply and the finished reply,
  not every token. Chat and Activity share the session transcript. It also
  tracks pending questions and
  elicitations and the status bar's meter (usage, window, timings).
  Display preferences are in `features/session/preferences.ts`
  (localStorage).
- **API**: `api/client.ts` is the only `fetch` caller (token, `ApiError`,
  forgets the token on 401); `api/connection.ts` stores token and URL;
  `api/stream.ts` is the one WebSocket with reference-counted topics.
- **Images**: the composer attaches PNG, JPEG, GIF, and WebP files by button,
  paste, or drop (`features/session/attachments.ts` checks them), only for a
  model with `image_input`; the store keeps them beside the draft so a rewind
  restores them. `components/MessageImages.tsx` draws a message's thumbnails
  in the transcript and opens one in the single
  `ImageViewer` the shell mounts.
- **Tool cards**: `features/session/renderers/renderers.tsx` maps tool names
  to a summary and a body (`bash`, `ask_user`, web, subagent, report, project,
  history, file and `mcp_*` tools); unknown tools render as JSON. Subagent start
  cards show the current state and link to Activity. Notices retain their full
  text and link to a named subagent when one exists. Home compaction cards
  show the board, conversation and summary attributes.

In development Vite serves :5173 and proxies `/api` to :8080; in production
the harness serves the bundle.

## Dependency direction

An arrow means "may import".

```
 cmd/eika -> server                     cmd/eikad -> eikad, search/filter
 server -> agent, session, store, computer, search, mcp, egress,
           utility, imaging
 agent -> tool, provider, inference       session -> agent, store
 provider/probe -> inference              inference: a leaf (standard library only)
 utility -> provider                      imaging -> provider
 provider/counter -> provider, provider/probe   agent -> provider/counter
 tool -> executor
 executor/sandbox -> eikad (wire types)   computer -> executor, executor/sandbox
 tool/builtin -> search, search/fetch     search/fetch, egress -> netguard
 mcp -> tool, event, mcp/oauth            config, event, secret: leaves
```

- Nothing imports `server` except `cmd/`.
- `tool` never imports `computer`; tools reach a computer only through an
  executor. `mcp` never imports `computer` or `executor`: the server hands
  it a `Store` and a `Launcher`.
- `search` imports nothing from `internal/`; goja (`search/filter`) is linked
  into eikad only.
- `internal/imports_test.go` holds these rules as a list of boundaries; an
  exception names the importing package and says why.

## Testing

`make check` runs gofmt/goimports checks, `go vet`, staticcheck,
golangci-lint if installed, the Go tests including `docker`-tagged ones,
ESLint, `tsc`, Vitest, then the visual suite. Go targets name
`./cmd/... ./internal/... ./evals/...` because `web/node_modules` contains Go
files. Vet and staticcheck also compile the `live`-tagged tests.

- Go: stdlib tests; `providertest` and `executor/local` make the loop
  testable without Docker or a model. `docker`-tagged tests (handlers, store,
  computers) use `storetest` and skip without a daemon.
- Contract: `docs/api/contract.json` is generated from the wire types; handler
  tests check responses against it and the mock harness checks requests,
  responses, and events. `docs_test.go` in `server` keeps `docs/api/http.md`
  listing every route in `routes.go` and nothing else, and `events.md` listing
  every event type.
- Frontend: Vitest for logic; Playwright specs in `web/e2e/` against the mock
  harness (`make visual`); `npm run shot` for screenshots.
- `make smoke`: the real stack, from setup and a provider to a plain chat and
  then a chat on the home computer whose `bash` call runs in its container.
- `make live`: `evals/live`, scenarios scripted against the bench's real chat
  model through `internal/provider/openai`, behind the `live` build tag. The
  package reads `~/.config/eika/live.env`, describes the server with the
  harness's own provider probe (build, model file and revision, slots, window),
  plays each scenario with a token counter of its own (the report records each
  call's prediction, its method and its drift, and a scenario fails on drift in
  an `exact` prediction) and writes a JSON
  report to the ignored `evals/results/`. Its plain code and fakes-backed
  tests run in `make check`; the live test skips without
  `EIKA_LIVE_ENDPOINT` and fails when that is set but silent. `evals/README.md`
  says how to run it, add a scenario and read a report.

The live suite also starts a separate Core process over a temporary Postgres
database and a real home computer. Its persistent home scenario records its
results in the same report as the model scenarios.
It covers restart recovery, history recall, prompt prefixes and queue priority.
The restart test gates a sandbox command until Core restarts. The recall test
seeds complete conversation turns and runs compaction and recall on the model.
For the queue check, two temporary routine sessions and home use one pool slot.
A proxy holds real model responses while the test observes the queue. Controls
compiled only with `live`, and enabled only by the live fixture, start background
runs and expose their lease times through authenticated test routes.
The harness retries `compact` while a subagent's report keeps home busy (409).
Its model offers the effort `none`, so a summary that a thinking model cut off at
its output limit is asked again with thinking off.

## Agent definitions

`internal/agentdef` provides the built-ins `eika`, `researcher` and `worker`.
System prompts and Eika's compaction prompts are embedded Markdown; the
subagents' compaction prompts are the loop's built-in ones with one closing
line each naming what to preserve. Definitions set the class,
fixed sorted tools and model-call limit (40 for Eika, 200 for subagents).
Lists contain only tools that exist. Later packages add their own tools.
Eika's prompt directs it to make and state reasonable assumptions for small
missing details, ask when unclear intent changes the result, and take a safer
route or clarify intent for irreversible actions. This is model guidance.

Migration 0011 adds `core.agents` with a built-in name and nullable class and
prompt overrides. Missing values use built-in defaults; an empty prompt is an
explicit value. The authenticated agent API returns effective values, defaults
and overrides. It accepts only chat classes and cannot change capabilities.
A session uses its agent definition at run start and in context and prediction
previews. Its class resolves through the
existing class fallbacks and the default chat model. Model sampling and thinking
settings come from the selected model row. An explicit requested model is
refused. The archived profile settings and session choices have no effect.

The runtime filters the shared built-in registry to the fixed tool list, in name
order, for both schemas and dispatch. It connects no MCP servers and includes no
MCP tools. An agent receives an executor only when one of its fixed tools needs
a computer (today, the worker). Workers and researchers have `ask_user`,
which relays questions through home. Runs use the definition's
call limit and compaction prompts with the current compaction algorithm and
budget settings. Editing a definition's prompt, an empty override included,
reaches a session at its next compaction, which rebuilds its snapshot; a new
session starts with it. The class applies from the next run. Context and
prediction previews show the snapshot. Model-call records include the agent
name and requested class, including when the class falls back.

## Projects

Migration 0012 adds `core.projects`. Each record has a stable ID and unique
slug, a name, a description, a status, a sandbox directory and UTC timestamps.
The default directory is `/home/eika/projects/<slug>` on the home computer.
`store.CreateProject` validates and applies defaults; `store.UpdateProject`
validates partial updates under a row lock. Slugs do not change.

The authenticated project API lists all statuses, creates records, reads a
record by ID and updates metadata. Archive uses the status field. These paths
are metadata only: no handler accesses the filesystem. Project metadata has no
instruction field. Eika can list active projects, open a project by slug, and
create or update metadata. Opening reads user instructions and the bounded
`STATUS.md` through the home computer executor; a missing file is empty.
The run holds the home computer in use until the status read ends.
Project tools cannot write instructions. They are available only through agent
definitions.

## User instructions

Migration 0013 adds `core.instructions` for global About me and rules, and
`core.project_instructions` for project text. Reads return empty strings when
no text is set. Each field accepts up to 64 KiB of UTF-8 text without NUL.

Authenticated GET, PUT and DELETE routes read, replace and clear instructions.
Project instruction writes use separate store methods and routes from project
metadata. Agent tools have no instruction write path. Settings edits global instructions; project pages edit project instructions.
The preamble snapshots them for each session.

The server ensures the local home session at startup. `GET /api/home` returns
its session and head. Home messages use the web channel.

### Subagent lifecycle

`subagent.Service` creates persistent children under a home session with their
initial `running` state in the same write. A duplicate
name returns the existing child. Message resumes the same session when it has no
active run; a child with an active run returns status. The service owns a run
context and reservation until execution ends and its final state is attempted.
If that write fails, the stored running or waiting state has no active run: the
next Message resumes it and the next Cancel records it cancelled, so a temporary
storage failure does not strand the child. Cancel stops and joins an active
execution. Close joins all executions before
the store closes. Each state or reason change emits `session.updated` on `global`.
Migration 0015 stores the waiting reason; 0022 lets a failed or cancelled child keep why it ended there.

A subagent ends an assignment with a report. The `report` tool (`summary`,
optional `outputs` and `follow_ups`) is in the `worker` and `researcher`
definitions and calls `Service.Report`, which stores the rendered text as the
session's `result` and emits `session.updated`; a call outside an active,
uncancelled execution is refused. A run that ends on its own without a report
is sent "Draft your report now with the report tool." once, in its own session.
If it still has not reported, its last assistant message becomes the report
(`finalMessage` in the server binding), or a fixed sentence when it has none.
Failed and cancelled runs are not asked for a report and keep the last one.
When an assignment ends with the child idle after a report, the service calls
its `Reported` option, which posts the report to home as a notice (below).

At most `subagent_max_running` executions exist at once (setting, 1 to 32,
default 4; a lower value is read as 1). `Start` and `Message` check the count
under the service lock that also registers the execution, so concurrent calls
cannot overshoot it, and a refused call leaves no row. Starting an existing name
and messaging a running child are not refused. Children still take turns on the
inference queue's slots. Aborting a home reply does not touch its subagents:
their runs belong to the service, not to the reply that started them.

The server owns one service for its lifetime and closes it before the run
manager, so shutdown records running children `cancelled`. Its run callback
(`runSubagent` in `internal/server/subagents.go`) starts a run on the child's
session through the run manager and waits for it; an aborted run is a cancel.
Eika's tools `subagent_start`, `subagent_message`, `subagent_status` and
`subagent_cancel` are agent-only built-ins bound to the service by the
`delegation` type. The binding accepts only a home session as caller, so no
subagent delegates, and picks the run priority from the calling run's rank:
`foreground` from an interactive turn, `background` otherwise. A worker gets
the home computer and a researcher none. A worker's run with a project
creates the project directory and wraps its executor so that commands and
relative paths start there; the executor still confines paths to the root.
The HTTP API refuses messages and compactions posted to a subagent's session.
Subagents use `ask_user` when unclear intent would change the result. Small
details are decided with assumptions stated in their report. The worker's prompt also tells it to read
pages with `web_fetch`, not `curl`, so that what it reads is screened.

### Subagent questions

A subagent's `ask_user` calls the server's question relay instead of registering
with the legacy `Questions` broker. `subagent.Service.Ask` sets the child to
`waiting`, with the question id, question and JSON-encoded choices as its reason, and registers a buffered
answer channel under the lifecycle lock. It then emits `question.asked` for
read-only watchers and posts a home notice through the notice writer below.
The notice id derives from the child, run and call ids, so write retries add
no second notice. Idle home starts a background turn. Eika can answer with
`subagent_message` with that question id, or ask the user in its reply and
relay the answer later with the same id. Before an unstored home message is
sent to the model, the notice book marks a question that ended as no longer
pending. The same check applies to queued messages and notice write retries.

`Message` accepts an answer only if its question id matches the current wait.
A missing or stale id cannot answer a different question or start new work.
It validates the answer against the choices, sets `running` with no
waiting reason and delivers one answer under the same lock as cancel. A bad
answer or failed state write leaves the question waiting. The original tool
call continues; no new run or user message is added to the child. Waiting
retains the execution reservation, but holds no inference lease: leases cover
model calls only. Cancel and shutdown end the wait. Questions live in memory;
a restart ends the run and P2-18 repairs its unanswered call. There is no
question table, Inbox item or direct user-answer route for subagents. Activity
shows the reason and the read-only tool card, including after a reload.

### Notices

`internal/server/notices.go` delivers news to the home session. A subagent's
report becomes a notice entry in home, "plumber (worker) reported: <report>",
written before anything else happens, so it survives a restart. Its entry id
is stored with the report in the child’s internal `report_notice_id`, and
`store.AppendEntryOnce` writes an id once, so a write retried after a lost
answer adds no second notice. It reaches Eika
in the `<context>` block of the next user-side message home sends, and the
stored message lists the entry's id, which is what records it delivered: a
notice no stored user entry lists is undelivered (`store.UndeliveredNotices`).

```
report -> notice entry -> home idle?  yes: background turn (empty message,
                                            context with the notices)
                                      no:  the run's next steering or follow-up
                                           message takes it, or the run ends
                                           and a background turn starts
```

The book keeps, per home session, the notices waiting and those a message has
taken but not stored. It reads a session's undelivered notices from the store
on the session's first run in the process. `take` moves the waiting notices to
the message being stamped, the run's store wrapper drops them once the message
is stored, and when a run ends, before it releases the session, any it took
that are not in a message carried to the next run wait again. A notice whose
write fails is written again in the background, after one second and then
twice as long each time up to a minute, until it is written or the server
shuts down. A background turn starts when a notice finds home
without a run, and when a home run ends with notices waiting: unless the user
aborted it, or it was a background turn that failed, so that a failing model is
not asked again and again; those notices wait for the next message. A run that
never started wakes home for a notice that arrived while it was being built.
Background turns run at `background` priority. A message the user posts with
mode `run` during one steers it, since the client may not know of the turn yet,
and any message joining a run raises its next calls to `interactive`. The
client learns of a turn it did not start from `turn.start` and refetches the
run status. A background turn's message has no text, and the transcript shows
no row for it. Shutdown stops background turns first.

### Startup recovery

Before the API serves requests, `Server.Run` reconciles sessions. Each session
gets an error result for every unanswered
tool call on its current branch: "Eika restarted while this ran; its outcome
is unknown. Check before retrying." Stored results and preamble bytes stay
unchanged. One transaction appends these results and aborts the old runs. For
agent home and child sessions it also appends a home notice, and sets an
interrupted child to `idle` with no waiting reason.
This includes a child created before its first run was stored. A failed
transaction stops startup. No tool executes and no child resumes.

Startup also scans every stored child report and appends its home notice with
`AppendEntryOnce`. Migration 0016 adds the internal report notice id; startup
pins an old report to an id before recovery changes the head. It finds a report
entry in the child's path and matches notice ids from that assignment. If an
old result has no report anchor, it reuses an existing child notice or assigns
one fixed recovery id. It cannot distinguish later unanchored results, and sends
at most one recovery notice for them. Report text is not used for matching. A
notice can be recovered after a crash between idle and posting, or during a
notice write retry. Startup starts no home turn. Recovered notices
wait for the next home turn, from a user message or a later new report.
Converted routine sessions get the missing results without an agent notice or
state change. Computer inspection is separate and remains best effort.

Session entry pages use sequence order across branches and entry IDs as backward
cursors. The session path remains the current branch used for model context.

### ntfy notifications

Settings seals the ntfy topic URL in Core. The API reports only whether it is
set. A subagent report and an Eika background reply each start an independent
notification, with fixed text and a link to Chat at `public_url`. Delivery uses
the public-address HTTP client. It retries HTTP 429 while Core runs. It does not retry ambiguous failures
or replay a delivery after a restart.
