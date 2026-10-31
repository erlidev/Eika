# Runtime: models, context, loop, agents, sessions, projects

The runtime is the part of Core that turns messages into model calls and tool calls.
It inherits the Pi-derived loop in `internal/agent` (queues, compaction, retries,
overflow handling, crash-safe persistence) and adds what an always-on local
assistant needs:

- exact token accounting
- model classes
- one inference queue that arbitrates the GPU
- a prefix-stable context layout
- loop guards and a clock
- the Sentry gate
- durable runs
- agents, one home session, projects and persistent subagents
- compaction and history recall built for a session that never ends

## 1. Endpoints, models and classes

### Endpoints

A provider row (kept from today) gains these fields:

| Field | Meaning |
|---|---|
| `backend` | Detected at probe time: `llamacpp`, `vllm`, `sglang`, `tabby`, `openai`, `other`. The user can override it |
| `pool` | Hardware group name. Endpoints in one pool share hardware and are serialised against each other. Default: the provider name |
| `api` | `responses` or `chat`: which wire protocol Core speaks to the endpoint (below). Chosen by the Responses conformance check, `chat` until one passes, and the user can override it |
| `slots` | Concurrent requests the pool accepts. The probe reads it where the server states it (llama.cpp `total_slots`, SGLang `max_running_requests`, TabbyAPI `max_batch_size`); otherwise 1 for a local backend, 8 for a cloud one. The user can override it |
| `kv` | `shared` (one cache all requests draw on, with its size in tokens), `partitioned` (a fixed share per slot) or `unknown` (the server does not say and nothing it serves tells the two apart). Probed; the user can override it and the size |
| `capabilities` | Probe result (jsonb): how the endpoint counts tokens (`chat`, `text`, `none`), whether it reports cache metrics (`reported`, `unknown`), whether it takes slot hints, the per-request context it reported (`window`), the server's version (`build`), and the outcome of the Responses check (`responses`: verdict, reason, the model and build it ran on) |

The probe (`POST /api/providers/probe`, and again on every Core start) reads the
endpoint's own description where it has one. It identifies the backend by the
routes in the second column, tried in the order llama.cpp, SGLang, TabbyAPI, vLLM
(SGLang answers `/version` as vLLM does, so it is asked first); an endpoint none
claims is `openai` when its address is not on the user's own network and `other`
when it is. It checks the exact-counting routes with one tiny request each and
never sends a prompt to a model; the Responses conformance check (below) is the
one separate step that does. A probe that fails at start is logged and
recorded on the provider (`probe_error`) and never stops Core or replaces an
earlier result. A field the user set is never replaced by a probe; the user
hands it back to the probe explicitly.

Status of each row: **verified** means read from the live bench; **unverified**
means written from the project's documentation or source, with a fixture marked
so in `internal/provider/probe/probetest/testdata/README.md`, to be replaced by
a recording when a server of that kind is on the bench.

| Backend | Identify by | Window per request | KV cache | Exact counting | Cache metric |
|---|---|---|---|---|---|
| llama.cpp (**verified**, b11942) | `GET /props` (`build_info`, `total_slots`) | `default_generation_settings.n_ctx`, which `/slots` repeats for every slot (the per-slot context, not `-c`) | **not reported**: `/props`, `/slots`, `/metrics` and `/v1/models` carry no `--kv-unified` field and no total context, and an N-slot unified server reads the same as a partitioned one with N times the context. One slot is `shared` (it owns the whole cache either way) with size `n_ctx`; several are `unknown`, and the user says which | `POST /apply-template` then `POST /tokenize` (`{"content": prompt}`) | `timings.cache_n` and `usage.prompt_tokens_details.cached_tokens`, equal |
| vLLM (unverified) | `GET /version`, confirmed by `/metrics` series named `vllm:` or `max_model_len` in `/v1/models` | `max_model_len` of `/v1/models` | `shared` (paged blocks); size `num_gpu_blocks` x `block_size`, labels of `vllm:cache_config_info` in `/metrics`; unknown size when metrics are off. Slots are not reported (`max_num_seqs` is not exported) | `POST /tokenize` with `messages` (and `tools`, `chat_template_kwargs`) | `usage.prompt_tokens_details.cached_tokens` (needs `--enable-prompt-tokens-details`); `unknown` until a response says |
| SGLang (unverified) | `GET /get_server_info`, or `/server_info` | `context_length`, or `max_model_len` of `/v1/models` when it is null | `shared` (radix cache); size `max_total_num_tokens`; slots `max_running_requests` when set | `POST /tokenize` where the version has it (text only) | `cached_tokens` in usage where reported; `unknown` |
| TabbyAPI (unverified) | `GET /v1/model` (`parameters.max_seq_len`) | `parameters.max_seq_len` | `shared` across the batch; size `parameters.cache_size`; slots `parameters.max_batch_size` | `POST /v1/token/encode` (text, or messages with the template) | none known; `unknown` |
| Cloud (unverified) | `/v1/models` and nothing else | listing fields, per model | not applicable: `unknown` | none | `prompt_tokens_details.cached_tokens`; `unknown` |

Where a version does not report the size, the pool is `shared` with an unknown
size, and the user can enter it. For `unknown`, the inference queue (section 3)
admits calls as if the cache were shared with the per-request window as its
size: that cannot overfill a unified cache, and on a partitioned server it holds
every request within its slot's share. It loses parallelism on a large unified
cache, so a user who knows the cache is shared sets `shared` and its size.

### Responses or Chat Completions

Eika prefers OpenAI's Responses API. It is used statelessly (`store: false`, the
full input sent on every call, reasoning items passed back as the server returned
them). Its typed items for reasoning, tool calls and tool outputs round-trip
thinking and tool calls without the `reasoning_content` and chat-template
conventions that each Chat Completions backend handles a little differently. Chat
Completions stays as the fallback and as the only choice for endpoints that lack
the route.

- **Default.** `responses` only when the conformance check passes, for
  llama.cpp as for any other endpoint: a streamed tool-call round trip with a
  reasoning item sent back, usage with cache metrics, and, where the endpoint
  counts exactly, the second request counted to the token. Otherwise `chat`. The
  check asks the user's model (the provider's oldest configured chat model,
  never a decision model, which cannot answer a chat request) two small
  requests, so it runs on an explicit probe and, at start, only when its recorded
  outcome (`capabilities.responses`) was made on another server build or model;
  a provider with no model is skipped and stays on `chat`. llama.cpp passes on
  b11942 with zero drift under `responses` (the Log has the numbers); a build
  whose Responses-to-chat conversion differs would fail on its drift and stay on
  `chat`. The user can override per provider. A hosted API is asked for
  `reasoning.encrypted_content` through `include` (when the model preserves its
  thinking, the only time the reasoning is sent back), since `store: false`
  otherwise returns no reasoning to pass back.
- **One provider interface.** Both protocols implement `provider.Provider`, so the
  loop, accounting and compaction do not see which one is in use. Exact token
  counting (section 2) works from the equivalent chat-format request, which either
  provider can build. That holds only if the server converts Responses input to
  chat the same way; phase 1 checks it by drift, and an endpoint with drift on
  `exact` under `responses` stays on `chat`. Usage fields differ by protocol:
  `input_tokens`, `input_tokens_details.cached_tokens` and
  `output_tokens_details.reasoning_tokens` under `responses`.
- **Verified, not assumed.** Support differs by backend and version, so each
  backend's `responses` behaviour is checked against a live server in phase 1 and
  its fixtures recorded. A backend whose support is partial stays on `chat` by
  default.

Every row in the table above is verified against a live server in phase 1, and fixtures
of each response shape are recorded; the table says which rows are still unverified.
Where a version lacks a route, the capability is `none`, and the runtime falls back
as section 2 describes.

The effective window of a model is the smaller of its row's `context_window` and
the per-request context the probe reported. A model whose configured window is
larger gets a warning in Settings. On a `partitioned` llama.cpp server with `-np`
above 1 this is the commonest cause of silent truncation.

Most engines share one KV cache among all their requests: vLLM, SGLang,
TabbyAPI/exllamav3, and llama.cpp with `--kv-unified`. There a single request may
use its whole window, but concurrent requests together cannot exceed the cache.
What happens when they would differs by engine. vLLM and SGLang preempt a request
and recompute it later, which costs prefill and evicts prefix cache. llama.cpp
fails the request once the unified cache is full. The inference queue therefore
admits calls on a `shared` pool against its size as well as its slot count
(section 3).

### Model kinds and classes

A model row gains `kind`: `chat` (today's rows) or `decision` (System One, see
`sentry.md` section 9).

Agents and harness tasks never name a model. They name a class, and the setting
`model_classes` maps each class to a model row:

| Class | Kind | Used for |
|---|---|---|
| `deep` | chat | Hard reasoning, and anything an agent definition marks deep. May be a cloud model |
| `main` | chat | The main agent and most tasks |
| `fast` | chat | Titles, summaries for notifications, the reviewer when no decision model is set, routine pre-checks |
| `decision` | decision | Sentry's reviewer and monitor, injection screening, routing, "is this worth a notification" |

A class with no model falls back along `deep -> main` and `fast -> main`, and a chat
class with no model along that chain falls back to the default model
(`default_model`, or the first chat model when it names none), so `fast -> main ->
default model` works before the user has set a class. `decision`
falls back to a chat model emulating the protocol (`sentry.md` 9.3). The UI shows
which classes share an endpoint, since a shared endpoint means they contend for
the same slots.

`model_classes` replaces `utility_models`. The session title task moves to `fast`.
For chat classes the fallback is deliberate: a missing `fast` model must not
disable titles. Titles are still skipped when `fast` would fall back, through `main`
or the default model, onto the model assigned to `deep`, if that is a hosted API (a
"cloud" model: its provider's backend is `openai`). A model the user assigned to `fast` is used
whatever it is. A class whose model row is deleted counts as unset.

A model's `kind` is chosen when it is added and does not change. A `decision`
model is refused wherever a conversation is held: the default model, a profile's
or session's model, a message's model, and the `deep`, `main` and `fast`
classes. The model test, a chat request, does not run on one; the test that
asks a decision model its own question comes with its client (`sentry.md` 9).
The Responses check asks a provider's oldest chat model.

Naming alternatives that were considered: high/medium/low; System 2/System 1;
thinker/worker/scout. `deep`/`main`/`fast` describe what each class is for and
read clearly in config.

## 2. Exact token accounting

Knowing exactly how much context is in use matters more than any cache optimisation.
A guess of 4 bytes per token can be off by 30% or more for code, CJK text and JSON,
and says nothing about template overhead. So the runtime sizes a request with the
endpoint's own tokenizer where there is one (the methods below), and a guess is the
last resort.

### Terms

Every model call records these, in a renamed `model_calls` table (formerly
`model_requests`):

| Field | Source | Meaning |
|---|---|---|
| `prompt_tokens` | `usage.prompt_tokens`; `usage.input_tokens` under `responses` | Tokens the server tokenised for this request, template included, **cached tokens included** (verified, below). Ground truth |
| `cached_tokens` | `usage.prompt_tokens_details.cached_tokens` (`input_tokens_details.cached_tokens` under `responses`), else llama.cpp's `timings.cache_n` | Part of the prompt reused from cache. NULL when the server states neither, never 0 by assumption; a stated 0 is 0. The usage field is read first because it is the one every backend carries, so the count means the same everywhere; `cache_n` is the fallback for a llama.cpp build without it (verified equal, below) |
| `prefilled_tokens` | `timings.prompt_n`, where the server states it, else `prompt - cached` | Tokens actually processed. NULL when the cached count is unknown too: the prompt alone does not say what was processed. Usage parsing derives nothing; the record does (`Usage.Prefilled`), which is exact because the prompt includes the cached part |
| `completion_tokens` | `usage.completion_tokens`; `usage.output_tokens` under `responses` | Generated tokens, **reasoning included** (verified, below) |
| `reasoning_tokens` | `usage.completion_tokens_details.reasoning_tokens` (`output_tokens_details.reasoning_tokens` under `responses`) | Part of the completion that was reasoning. NULL when the server states none (llama.cpp does not). Where the endpoint can tokenise text, the record holds a tokenisation of the reasoning text instead, and `reasoning_source` says which: `reported` or `tokenised`. A tokenised count approximates the generated tokens (on llama.cpp b11942 it equalled them), so it never enters `provider.Usage` or the events |
| `predicted_prompt_tokens`, `prediction` | the runtime | What the runtime expected before sending, and how: `exact`, `anchored` or `estimated` |
| `context_window` | effective window | The window this call was judged against (`window` is a reserved word in SQL) |
| `queue_ms`, `prefill_ms`, `decode_ms` | inference queue, timings | Wait before the call, prompt phase, generation phase. `queue_ms` exists and is NULL until the queue fills it; `prefill_ms` and `decode_ms` are not columns yet |
| `endpoint`, `slot`, `class`, `agent`, `priority` | runtime | Attribution. `endpoint` is the provider's name; the others exist and are NULL until the queue and the agents fill them |
| `drift` | the runtime | `predicted_prompt_tokens - prompt_tokens`; NULL unless both exist |

The window fill after a call is `prompt_tokens + completion_tokens`: what the
KV cache held at the end. The context meter shows this number, and it is exact.

One rule is checked in phase 1 against each backend: whether `prompt_tokens`
includes cached tokens. llama.cpp's `prompt_n` counts processed tokens only and
`cache_n` the reused ones. The accounting code reads each backend's fields by its
documented meaning and never sums fields of unknown meaning. A count the server
does not state is NULL here and in `provider.Usage` (`provider.Count`), and a sum
over calls is NULL when any call's count was.

What each backend states, and what was verified:

| Backend | Prompt | Cached | Reasoning | Status |
|---|---|---|---|---|
| llama.cpp b11942, `/v1/chat/completions` | `usage.prompt_tokens`, **includes cached** | `usage.prompt_tokens_details.cached_tokens` = `timings.cache_n`; `timings.prompt_n` is the processed part | none: NULL. `completion_tokens` includes the reasoning | **verified** on the bench, streamed, cold and warm, reasoning on: prompt 90 = cache_n 0 + prompt_n 90 cold, 90 = 86 + 4 warm; a request cut off at 20 tokens inside the reasoning had `completion_tokens` 20 |
| llama.cpp b11942, `/v1/responses` | `response.usage.input_tokens`, **includes cached** | `input_tokens_details.cached_tokens` = `timings.cache_n`, and `timings` is a sibling of `response` in the `response.completed` event | none: NULL (no `output_tokens_details`). `output_tokens` includes the reasoning | **verified**: the same numbers as `chat` for the same prompt (90 tokens, 0 then 86 cached); output 107 over 77 reasoning and 27 answer chunks, and 20 for a response cut off inside the reasoning |
| OpenAI and other cloud APIs | `prompt_tokens` / `input_tokens` | `prompt_tokens_details.cached_tokens` / `input_tokens_details.cached_tokens` | `completion_tokens_details.reasoning_tokens` / `output_tokens_details.reasoning_tokens` | unverified, from the API reference |
| vLLM | `prompt_tokens` / `input_tokens` | `prompt_tokens_details.cached_tokens`, only with `--enable-prompt-tokens-details` (else `null`: NULL) | none on chat; `output_tokens_details.reasoning_tokens` on responses | unverified, from the source |
| SGLang | `prompt_tokens` | `prompt_tokens_details.cached_tokens`, only with `--enable-cache-report` | none read: its top-level `reasoning_tokens` is 0 whether or not reasoning was counted | unverified, from the source |
| TabbyAPI | `prompt_tokens` | none | none | unverified, from the source |

The first two rows hold at the bench's one build; `cache_n` and the usage field
agreed in all six recordings (`internal/provider/testdata/usage`).

### Predicting the next request

Before every call the runtime needs the size of the request it is about to send: to
decide on compaction, to lower max output to the room left, and to show the
meter. It uses the best method the endpoint offers:

1. **Exact** (`exact`). The endpoint renders and tokenises the request itself:
   vLLM `/tokenize` with messages and tools, or llama.cpp `/apply-template` followed
   by `/tokenize`. This adds one HTTP round trip and a CPU tokenisation, a few tens
   of milliseconds at 200k tokens, which is negligible beside prefill (measured on
   llama.cpp at 20k tokens: 31 ms uncached, 10 ms with the cache). Results are
   cached per session by prefix hash: llama.cpp's rendered prompt is cut at its
   control tokens and the count up to the last cut is remembered, so only the
   messages added since are tokenised again. vLLM shows no prompt, so a whole
   request is remembered and nothing finer. The server renders the equivalent
   chat body that the provider sends (`provider.ChatEncoder`), so the two
   protocols count alike.
   **Pictures are not counted** by any of these routes (checked on llama.cpp: the
   placeholder is tokenised as text and the picture's own tokens are missing). A
   request with a picture is therefore never `exact`: it is counted without the
   picture, and each picture size costs what the first measurement of that size
   showed (`anchored`), or a pixel estimate until then (`estimated`).
2. **Anchored** (`anchored`). This is for endpoints that can tokenise text but not
   render a chat request. The text of the request is tokenised exactly and the
   template's share is a weighted sum fitted on every measured `prompt_tokens`: a
   base, a weight per role, per tool call, and for replayed reasoning after and
   before the last user message (a template that drops reasoning from earlier
   turns learns a weight of 0 for the second). The last measurement is the anchor
   while a request extends it; after a compaction the fitted model alone
   predicts. Before the first measurement the method is `estimated`. The weights
   are kept in memory in the session's counter and relearned after a restart.
3. **Estimated** (`estimated`). This is for endpoints with no tokenizer route
   (TabbyAPI without messages support, cloud APIs). It uses the anchor plus bytes
   divided by a ratio learned from the session's history, starting at 4 until
   there is data (one ratio for now; per content class is not built). It is
   shown with a tilde, and the compaction reserve is widened, for this method
   alone, by the ratio's observed error (`Counter.EstimateError`) times the
   predicted size, up to half the window. A session resumed after a restart is
   seeded from the newest measurement its messages carry (`Counter.Seed`), so the
   first estimate starts from a measured size and not from bytes alone.

Each call stores `drift = predicted - measured` in `model_calls`, NULL when
either is. An `exact` prediction with any drift is a bug: the loop logs it as a
warning with the numbers and none of the request, and the Context inspector shows
it (as an error notice). Persistent drift on `anchored` retrains its constants.
Counting never fails a call: a prediction that cannot finish in its time (ten
seconds by default) is replaced by the estimate, and the stored method says so.

### Breakdown by part

The Context panel and inspector show where the tokens go: system prompt, tools,
preamble (instructions, memory index, skills catalog, plugin catalog, project
brief), each message, and reasoning. Each part is tokenised exactly as text where
the endpoint can (cached by content hash; parts rarely change). The remainder is
shown as its own line, "template and formatting":

```
template and formatting = measured prompt_tokens - sum(parts)
```

Nothing is scaled to make the sum fit, so a negative or growing remainder exposes an
accounting bug. Tool schemas are counted by rendering the request with and without
tools when the endpoint supports exact counting, and otherwise as their JSON text,
marked approximate. Rendered, each tool is what the template adds when it is added to
the tools before it, so the sizes add up to the whole list's and the first carries the
template's wrapper around the list; a list of more than 64 tools is sized as text.
A picture is never exact (above), so a message with one is marked approximate until
its size has been measured. A part the time to count ran out for is estimated and marked.

The parts are counted when a client asks (`GET /api/sessions/{id}/context` and
`.../calls/{call_id}`), never in the loop, and each is remembered by the hash of its
content in the session's counter. A recorded call states its remainder against its
measured `prompt_tokens`. The next request has no measurement, so its remainder is
stated against its prediction when that is `exact` (the endpoint rendered and tokenised
that very request) and is absent otherwise. The preamble is not a part yet: the
sections of the system prompt are the base prompt and the instructions, and the
preamble joins them when it exists.

### Where it shows

- Status bar meter: the last call's window fill out of the effective window, the
  cached tokens of that call, and the predicted next request with its method
  (`GET /api/sessions/{id}/prediction`, a tilde on `estimated`). Before any call has
  been measured the fill and the cache read `n/a` and the prediction stands alone; a
  count the endpoint did not state is never shown as 0.
- Context panel: the parts with exact or approximate markers, and the remainder line.
- Activity and Settings: per-day prefill seconds and tokens, cache-hit rate
  (`cached / prompt` over calls that report it), and compute by priority.

### Tests

- Unit tests per backend, over recorded response fixtures: which field means what,
  and NULL where a backend does not report.
- A prediction test with `providertest`: the fake endpoint implements `tokenize`
  with a deterministic tokenizer, and `predicted == measured` holds over a scripted
  conversation that includes compaction.
- `make live`, opt-in and not part of `make check`: runs N scripted requests
  against `EIKA_LIVE_ENDPOINT` and requires zero drift on `exact` and stated bounds
  on `anchored`. It is run against llama.cpp, vLLM and TabbyAPI before each phase
  that touches accounting is closed.

## 3. The inference queue

Every model call in Core goes through `internal/inference`. That covers agent turns,
compaction, titles, dream steps and chat-model emulation of decisions. Sentry makes
its own calls to its own endpoints; see `sentry.md` 9.4.

```
call := queue.Acquire(ctx, Need{Endpoint, Priority, SessionID, Tokens})  // blocks
defer call.Release()
provider.Stream(ctx, request with call.SlotHint())
```

- **Concurrency.** A pool admits `slots` leases at once; further calls wait. A
  `shared` pool (section 1), the usual kind, also admits a call only while its
  running calls fit the cache with this one: each counted at its predicted prompt
  plus maximum output, with a prefix shared between sessions counted once where
  the engine caches prefixes. This keeps sessions from preempting or failing each
  other mid-generation. A pool whose `kv` is `unknown` is admitted as `shared` with
  the per-request window as its size (section 1). With no size at all, only slots
  limit admission, and Activity shows engine preemptions where the backend
  reports them.
- **Priorities**, highest first:
  1. `interactive`: the home session answering a message the user sent.
  2. `foreground`: a subagent started or messaged from an interactive turn in the
     last 10 minutes, or one the user is watching.
  3. `background`: routines, wakeups, other subagents.
  4. `maintenance`: dreaming, titles, idle compaction, index work. Runs only when
     the pool has been idle for `maintenance_idle_seconds` (default 120).
- **Preemption happens at call boundaries.** A waiting higher-priority call gets the
  next free lease. Generations already running are not cancelled by default, since
  cancelling throws away their prefill. The setting `preempt_running: true` cancels a
  running background or maintenance call when an interactive call is waiting and
  no slot is free. The cancelled call is retried later and counts as a retry.
- **Aging.** A waiting call gains one priority level per `aging_seconds` (default
  300) so background work cannot starve.
- **Upgrade.** A run's priority is set per call, not per run. When a user message
  joins a home turn running at `background` (a report, an Inbox answer, a wakeup)
  through the steering queue, the run's next call is `interactive`.
- **Slot affinity, llama.cpp only.** With `slots > 1`, a session keeps the slot it
  last used (LRU map) and the request carries `id_slot`, so its prefix stays in that
  slot's KV cache. This is the only backend-specific cache feature in the first
  version. Slot save and restore to disk, and anything like LMCache, are deferred
  (`PLAN.md`, open questions).
- **No compute budget.** Priorities and aging keep background work from hurting
  interactive latency.
- **Visibility.** Queue wait is recorded per call (`queue_ms`). Activity shows what
  holds each pool and what waits behind it.

## 4. Context layout

Every agent request is assembled in one place (`agent/context.go`, kept), in this
order. Each layer is a byte-stable prefix of everything after it:

```
system      identity and rules of the agent definition            stable for the definition's life
tools       the definition's fixed tool list, sorted, byte-stable schemas
preamble    one entry written when the session starts and replaced only by compaction:
              user instructions (global, then project)
              pinned memories, then the memory index (global, then project)
              skills catalog, plugin catalog
              the agents Eika can delegate to, with the model classes allowed for each
              project brief, or the subagent's brief for a subagent session
              the user's time zone and locale
summary     after a compaction only: the compaction summary (section 11)
messages    append-only conversation
              each user-side message carries a <context> block at its end:
              current time, channel, notices (subagent reports, questions, memory
              or skill changes since the preamble)
```

Rules (these come from Manus's context-engineering notes and Hermes's heartbeat
design):

- Nothing in system or tools changes between compactions. Editing an agent
  definition applies to new sessions, and to an existing one when it next compacts.
  For the home session, which never ends, compaction is the only point where its
  tool list can grow, so a phase that adds a tool reaches it at the next compaction.
- The preamble is stored as an entry (kind `preamble`), so a resumed or restarted
  session sends exactly the bytes it sent before. Live memory is never re-read
  into an old prefix. Changes since then show up as notices at the tail.
- There is no clock in the prefix. Time goes into each user-side message, to the
  minute.
- JSON serialisation is deterministic, as `provider/openai` already guarantees. Tool
  order is sorted.
- Tools never appear or disappear mid-session. Plugins and skills are reached through
  fixed generic tools (`skills-and-plugins.md`), so installing one never breaks a
  live prefix.
- Untrusted content enters only as a tool result, wrapped by Sentry in
  `<untrusted source="...">` boundaries after scanning (`sentry.md` 6).
- **Prefix-stability test** (`make check`): for a scripted sequence of turns,
  including notices, approvals, a restart and a resume, each request's serialised
  prefix is a byte-prefix of the next, except across a compaction. It is the
  invariant that keeps prefill cheap, and it is cheap to test.

Large tool output is spilled. Output over the tool's budget (today 32 KiB head and
tail for bash) is written whole to `/home/eika/.eika/outputs/<call id>.txt` on the
computer, and the result says where. The model can read the rest with `bash` or
`file_read` instead of the harness keeping it in context.

## 5. The loop

`internal/agent` keeps its structure: `Run` -> `turn` -> model call -> tool calls,
plus queues, pending-message restore, terminal writes on a detached context,
retries, overflow handling, cut-off handling and the Recorder. Changes:

| Change | Detail |
|---|---|
| `Options.Executor` becomes `Options.Computer` | A handle with the executor plus computer id and directory. Tools that need none still mark themselves `Standalone` |
| `Options.Gate` | Sentry client. `dispatch` asks `Gate.Decide` before every tool call and `Gate.Ingest` on every result from an untrusted source. The model is never told about the gate: a deny returns a normal `Blocked: <reason>` error result (`sentry.md` 4), and an `ask` parks the call, which looks like a slow tool (below). The loop stays standard |
| `Options.Clock` | Injected time for the `<context>` block and for tests |
| `Options.Limits` | Max model calls per run (home 40, subagent 200), identical consecutive tool calls (a warning notice in the result from the 3rd, never a stop), wall time per subagent run (default 24 h), stream stall timeout |
| Stall timeout | No bytes for `stall` seconds aborts the attempt as retryable. Before the first token the limit scales with predicted prefill: `30 s + predicted_prefilled / measured_prefill_rate x 2`, since 200k tokens of dense prefill can take minutes |
| Parking | A tool waiting on an approval or on an answer blocks its goroutine, as `ask_user` does today. It holds no inference lease, since leases cover model calls only. Run state becomes `waiting`, so Activity shows it |
| Durable questions and approvals | The `ask_user` broker, Eika's Inbox questions and the approval wait persist their pending items (a `questions` row; approvals live in Sentry), so a restart does not lose them |
| Resume after restart | On start, subagent, routine and dream runs that were `running` or `waiting` resume from their last resumable entry with a notice ("Eika restarted at <time>; continue"). A tool call whose approval is still pending is re-dispatched and Sentry returns the same approval (approvals are keyed by session and tool-call id). A non-read call is marked `dispatched` before it runs; one that was dispatched and has no result is never run again, and gets the result "Eika restarted while this ran; its outcome is unknown. Check before retrying.", so a send cannot happen twice. An interrupted home reply is not resumed: the transcript gets a notice and the user's message stays answerable |
| Tool-call ids | The loop mints its own id for every tool call and keeps the backend's id only for the wire. Local servers reuse ids (`call_0`, empty, per-response counters), and approvals, entries and the dispatched marker need a unique key |
| Images in tool results | Screenshots reach a vision-capable model as a user message with the image right after the tool result, under either API, since neither takes images in a tool result. A model without `image_input` gets the text and a note in place of each image. This resolves the item `DECISIONS.md` lists as deferred |
| Compaction | Rebuilt for the home session: deterministic sections, a rolling summary, and idle compaction (section 11). Subagent, routine and dream sessions keep today's Pi compaction with their definition's prompts |
| Reasoning replay | `preserve_thinking` gains the `chat_template_kwargs` form where a template takes it there |

Parallel tool calls stay sequential in execution. The first version adds no
batching.

## 6. Agents

An agent definition replaces a profile. Built-in definitions live in Go with their
prompts embedded as Markdown (`internal/agentdef/prompts/*.md`). The user can
override the prompt, class, limits and tool list in Settings, stored as an
`agents` row that names the built-in it overrides. The user can also add agents of
their own.

| Field | Meaning |
|---|---|
| `name`, `description` | Identity. The description is what the main agent sees when it delegates |
| `class` | Model class. The user sets it for every agent, so `eika`, `browser` and `dreamer` included |
| `classes` | For `worker` and `researcher`: the classes Eika may choose from with `subagent_start {class}`, set by the user. `class` is the default when Eika names none |
| `prompt` | System prompt body |
| `tools` | Fixed, sorted list of built-in tool names |
| `access` | Booleans that Sentry and the preamble read: `memory` (none, read, write), `private_data` (may hold B), `external_effects` (may cause C), `computer` (none, project, home) |
| `skills`, `plugins` | Which catalogs its preamble lists: all, none, or a list |
| `limits`, `compaction` | As in section 5 |
| `report` | How a subagent of this definition reports back: `always`, `on_completion`, `silent` |

Built-ins:

| Agent | Class | Tools | Access | Role |
|---|---|---|---|---|
| `eika` | main | memory_search, memory_read, memory_write, memory_forget, history_search, history_read, skill_use, skill_file, subagent_start, subagent_message, subagent_status, subagent_cancel, inbox_ask, project_list, project_open, project_create, project_update, routine_create, routine_list, routine_update, routine_delete, wake_me, plugin_find, plugin_call, web_search, request_secret | memory write, private data, external effects, no computer | Converses, remembers, plans, delegates |
| `worker` | main (allowed: `main`, `deep`) | bash, file_read, file_write, file_edit, web_search, web_fetch, skill_use, skill_file, memory_search (project), report | memory read (it reports what it learned, and Eika saves it), private data, external effects via Sentry, computer: project | Does work on a computer |
| `researcher` | fast (allowed: `fast`, `main`) | web_search, web_fetch, report | no memory, no private data, no effects | Reads the web and returns findings |
| `browser` | main (vision if available) | browser_* (`browser.md`), report, ask_user | no memory; private data through logged-in sites; effects gated per action | Drives the browser |
| `dreamer` | main | memory_list, memory_read, memory_write, memory_merge, memory_archive, history_search, history_read, dream_report | memory write, no network, no effects | Consolidates memory (`memory.md` 7) |

- The main agent has no `bash`, so its context stays small and its prefix steady.
  Work on a computer goes to `worker`.
- Eika's prompt tells it to decide for itself. When a small detail is missing, it
  makes the most reasonable assumption, says what it assumed in its reply or
  report, and carries on. When it is unclear what the user wants, in a way that
  changes the result, it asks. The prompt also cautions it about irreversible
  actions (deleting, sending, paying, overwriting without a copy): it takes a
  safer route where one exists and is more ready to ask about intent before
  taking one. This is guidance to the model only. It does not feed any Sentry
  decision. Subagent briefs carry the same instruction, and a subagent's
  `ask_user` is for the same case.
- The main agent has no `ask_user`. It talks to the user in the home session, so a
  question is simply its reply. When the user may not be watching, it uses
  `inbox_ask` (section 9). Subagents keep `ask_user`, which goes to Eika, not to
  the user.
- `file_read`, `file_write` and `file_edit` come back. They are thin tools over the
  executor, added because small local models edit files far more reliably with
  them than with heredocs. Today's `DECISIONS.md` reasons for bash-only applied
  to a coding agent on a frontier model.

## 7. Sessions

The session tree, entries, runs, steering and follow-up queues, and compaction
entries are kept. Changes:

- `sessions` gains these columns:
  - `kind`: `home`, `subagent`, `routine` or `dream`. It replaces
    `user`, the only kind left.
  - `agent`: the definition name.
  - `name`: a subagent's handle (`plumber`, `flights-lisbon`), unique under its
    home session.
  - `project_id`: nullable.
  - `computer_id`: nullable.
  - `parent_session_id`: the delegator. Migration `0002` dropped the column
    with forks; this adds it back with this meaning.
  - `state`: `idle`, `running`, `waiting`, `failed` or `cancelled`, set by code.
  - `brief`: a subagent's first brief.
  - `result`: a subagent's latest report.
  - `sensitive` and `suspicious`: Sentry's labels, mirrored for the UI
    (`sentry.md` 6).
  - `last_active_at`.
  It loses `computer_id`, `tools`, `profile_id` and `overrides`.
- Entry kinds gain `preamble` (the snapshot above) and `notice` (harness text shown
  to the user, also sent as part of the next user-side `<context>`). User and
  assistant entries in the home session record their `channel`.
- **One home session.** The user works with Eika the way they would with one
  person: a single conversation that never ends, while Eika keeps track of many
  lines of work through its subagents (section 9). The home session is created at
  setup. Compaction keeps it within its window, and history recall brings back
  anything compacted (section 11).
- **Every channel feeds it.** Web first; Discord or Matrix later
  (`interface.md` 6). Each user-side `<context>` names the channel the message came
  from, and the reply goes back to that channel. Proactive messages (reports,
  wakeups, routine results) go to the setting `notify_channel`. A message that
  arrives while a run is going uses the steering and follow-up queues, as today.
- **Room for more later.** The home session is found by `(owner, name)`, not
  assumed unique, so further home sessions (another person, another persona) need
  no schema change. There is one for now.
- **No direct sessions.** The user talks only to Eika. Subagent, routine and dream
  sessions can be watched live, read-only, with a cancel button
  (`interface.md` 1). Steering goes through Eika, which calls `subagent_message`,
  so Eika's own view of its work stays accurate.
- **Branching.** The tree stays in storage, but the UI is linear, with no rewind or
  fork.
- **Pagination.** `GET .../entries?before=<id>&limit=` serves history in pages, since
  the home session grows without end. Replay since an entry id stays as it is.

## 8. Projects

A project is a long-lived home for one area of the user's life or work: "Kitchen
renovation", "Job search", "Taxes 2026", "Eika itself". It holds:

| Field | Meaning |
|---|---|
| `name`, `slug`, `description` | Identity. The description is the one line the main agent's preamble lists |
| `status` | `active`, `paused`, `done` or `archived`. Only active projects appear in the home preamble |
| `instructions` | User-written text. Agents read it and never write it |
| `private` | Set by the user. Reading the project's files or memories marks a session `sensitive` (`sentry.md` 6) |
| `computer_id`, `directory` | Where its files live. The default is `/home/eika/projects/<slug>` on the `home` computer. A project can ask for a computer of its own (another image, stricter egress) |
| `agent` | Default agent for sessions started in it (usually `worker`) |
| `skills`, `plugins` | Which ones its sessions list |
| `created_by` | `user` or `eika` |

Memory has a project scope (`memory.md`). A pinned project memory named `status`
is the living summary that agents keep current. Routines can belong to a project.

### How Eika uses projects

This works the way a person uses Claude Code: open the project, hand work to a
subagent with a brief, and come back to that subagent later.

- `project_list` returns active projects with their one-liner and status.
- `project_open {project}` returns the description, instructions, pinned project
  memories, the project's subagents (name, agent, state, the first line of the
  last report), and routines. Opening a project changes nothing in the main
  agent's prefix: the answer is a tool result.
- `subagent_start {..., project}` starts a subagent in the project. Its preamble
  carries the project brief, its instructions and its memory index, so the
  subagent gets the project's context without the main agent carrying it.
- `project_create` and `project_update` let Eika create projects and change
  descriptions and status, but not instructions. Creating a project needs no
  approval, since it changes nothing outside Eika.

The user sees the same projects in the UI, edits their instructions, and can watch
any of their subagents (`interface.md` 1).

## 9. Subagents

Eika keeps track of several lines of work the way subagents traditionally work: it
hands each one to a subagent with a brief, the subagent works in its own context,
and it reports back. Two things differ from one-shot subagents such as Claude
Code's Task tool or Eika's old `spawn_agent`, both because Eika is one long-lived
session, and local contexts can be scarce:

- **Asynchronous by default.** `subagent_start` returns at once. A blocking spawn
  would hold the main context idle for the whole run, and on a setup with few
  slots it would evict that context and re-prefill it afterwards. `wait: true`
  exists for short work whose answer is needed in the same turn.
- **Persistent and named.** A subagent keeps its session after it reports. A later
  `subagent_message` to the same name resumes it in its own context, so "plumber"
  or "flights-lisbon" is also the thread of that work. There is no separate thread
  concept.

```
eika (home)  --subagent_start plumber-->  subagent session (worker, project Kitchen)
  replies "on it", the turn ends              queued at foreground priority
                                              runs; may park on approvals and questions
                                              ends with report {summary, outputs, follow_ups}
home session  <--notice "plumber (worker) reported: <summary>"--
  ...days later...
eika  --subagent_message plumber "get a second quote"-->  same session resumes
```

| Tool (Eika's) | Behaviour |
|---|---|
| `subagent_start {agent, name, brief, class?, project?, report?, wait?}` | Starts a named subagent and returns its name. `class` must be one the agent allows (the preamble lists them), else an error names the allowed ones. A name already in use returns that subagent's status instead, so Eika messages it rather than starting a duplicate |
| `subagent_message {subagent, text}` | Steers a running subagent through its steering queue, or resumes an idle, failed or cancelled one with a new message in its own context (its compaction summary and tail; the slot cache where it still holds). To a subagent waiting on a question, the text is the answer |
| `subagent_status {subagent?}` | Without a name, the board: each subagent's name, agent, project, state, last activity, the first line of its last report, and anything it waits on. With a name: its brief, full last report, pending questions, approvals and wakeups |
| `subagent_cancel {subagent}` | Stops its current run. It stays resumable |
| `inbox_ask {question, options?, subagent?}` | Puts a question to the user in the Inbox and returns at once. The answer arrives as a notice and starts a background home turn. Eika uses it rarely: for a subagent's question it cannot answer itself that is about unclear user intent, or when the user is not in the chat |

Inside a subagent, `report {summary, outputs[], follow_ups[]}` ends the current
assignment.

- **State is set by code, never by the model.** `running`; `waiting` (on a
  question, an approval or a wakeup, with the reason); `idle` after a report;
  `failed`; `cancelled`.
- **Reports.** A run that ends without calling `report` is sent one message in its
  own session: "Draft your report now with the report tool." If it still does not
  call `report`, its final message becomes the report, as the removed spawner's
  `summaryOf` did (`PLAN.md` log, "phase 0 notes for later phases").
  Every report becomes a notice in Eika's
  next `<context>`: "plumber (worker) reported: <summary>; follow-ups: …". With
  `report: always`, a background turn on the home session tells the user.
- **Questions** a subagent asks with `ask_user` go to Eika. The subagent parks
  (`waiting`), the question becomes a notice, and a background home turn starts.
  Eika answers with `subagent_message`, or asks the user in the Inbox with
  `inbox_ask` and relays the answer. Eika decides what the user needs to see.
- **Approvals** a subagent needs go to the Inbox through Sentry, not through Eika.
  To the subagent this is a slow tool call. The board shows it as waiting on the
  user, and no notice is sent.
- **Wakeups** a subagent sets with `wake_me` resume that subagent, not the home
  session.
- **The board** is a deterministic section of every home compaction summary
  (section 11), and `subagent_status` gives it live between compactions.
  A subagent idle for `subagent_board_days` (default 30) leaves the board. It
  stays resumable by name and searchable with `history_search {subagent}`.
- **Labels.** A report from a subagent whose session holds `sensitive` carries
  that label into the home session (`sentry.md` 6).
- **Limits.** Depth 1 by default: Eika starts subagents, and subagents do not. The
  setting `subagent_max_depth` allows 2 for `worker -> researcher`; a nested
  subagent belongs to its parent, not to Eika's board. At most
  `subagent_max_running` subagents are runnable at once (default 4). They still
  execute one at a time on a one-slot pool. The reserve-under-lock logic of
  the removed spawner carries over.
- **Abort and shutdown** carry over from the removed spawner. Aborting a home reply does
  not cancel its subagents, since they outlive the turn that started them. Halt
  stops everything. `Shutdown` stops runs, and durable runs resume on start
  (section 5).

## 10. Computers

The `computer` package and the `home` computer are built (`ARCHITECTURE.md`,
"Sandboxes"). What is still to come:

- `home` holds project directories and spilled outputs.
- **Per-run egress identity.** Every `exec` carries `HTTP(S)_PROXY` credentials
  that Sentry issued for that run, so the proxy attributes traffic to the session
  rather than the container. Processes a run starts in the background keep that
  run's identity; its credentials stay valid until the computer stops. They are
  attribution, not authentication: the agent can read its own environment.
  eikad already accepts environment per exec.
- Computers join `eika_sandbox_internal` only. The `open` egress mode, which
  bypasses the proxy, is removed. A permissive allowlist (`*`) remains, so traffic
  still passes Sentry, and its blocklist and halt still apply.
- Skills are mounted read-only at `/skills`; see `skills-and-plugins.md`.
- Plugin cells are computers too, labelled `eika.role=plugin`. No agent tool can
  target them.

## 11. Compaction and recall

The home session never ends, so compaction and recall carry most of Eika's
continuity. Three tiers, from most to least curated:

| Tier | Holds | Lifetime |
|---|---|---|
| Memory (`memory.md`) | Curated facts, preferences, people, procedures | Until changed or archived |
| Compaction summary | The state of the conversation and of Eika's work | Rewritten at each compaction |
| History | Every entry of every session, verbatim; the session tree already keeps them | Forever, searchable |

The design combines Pi's compaction, which Eika runs today (an LLM summary in fixed
sections, updated in place at each compaction), with ideas from Pi-VCC, an
algorithmic compactor for Pi: deterministic sections, cuts at turn boundaries, a
brief transcript with references, and search over the compacted history. Code
writes what can be read from rows. The model writes only what needs judgement.
References lead back to the originals.

### The summary

```
<summary as_of="2026-11-03 21:40" entries="#1-#5321" compactions="14">
[Subagents]      deterministic, from session rows: open subagents, then those that
                 reported since the last compaction. Name (agent, project), state,
                 last activity, the first line of the last report
[Ledger]         deterministic, since the last compaction: projects created or
                 changed, memories saved, routines and wakeups set, questions and
                 approvals still open
[Conversation]   LLM, rolling update (below)
[Recent turns]   deterministic brief transcript of the end of the compacted span,
                 about 80 lines; tool calls as one-liners with (#id) references
footer           "history_search and history_read #<id> recover anything compacted"
</summary>
```

`[Conversation]` uses Pi's rolling update: the first compaction writes it, and each
later one folds the newly compacted messages into the previous version
(`UpdatePrompt`, kept). The format is tuned for an assistant rather than a coding
agent:

```
## Commitments and open loops   promises Eika made, what waits on the user, with dates
## People and context
## Preferences and corrections   only what memory does not hold yet
## Decisions                     and why
## Recent topics                 by subagent or project where one applies
## Next
```

Its rules: absolute dates, exact names, numbers and ids, subagents by name, and
nothing the board, the ledger or memory already says.

Chapters were considered: one summary per compacted span, written once and frozen,
with a growing list of chapter titles. They bound how often a fact is rewritten,
but they need the current state kept outside the summary, and they add a concept.
Rolling comes first; chapters are a deferred option if the compaction evals show
drift. Every compaction entry keeps its own summary, so earlier versions stay
readable through `history_read {compaction}`.

### When

- **Idle compaction is the normal path.** The home session is compacted at
  `maintenance` priority when it is over `compact_idle_fill` (default 60%) of its
  effective window and nothing important has run for `compact_idle_minutes`
  (default 30). Important means any run at `interactive`, `foreground` or
  `background` priority, anywhere in Core: a user turn, a running subagent, a
  routine. The user's next message does not pay for it.
- The threshold (window minus reserve) and the overflow retry stay as safety nets,
  checked before every model call as today.
- Budgets are shares of the effective window rather than fixed token counts. The
  target after a compaction is about 30 to 35% of the window, since each
  compaction costs one full re-prefill and fewer, larger ones are cheaper.
- **The cut** is at a user-turn boundary, keeping at least one whole turn and up to
  `keep_recent_share` (default 15%) of the window. Pi's split-turn summary stays as
  the fallback for a single turn larger than that. The main agent has no `bash`, so
  its turns are small and splits are rare.

### How

Two steps follow, on one cached prefix. There is no memory flush: Eika saves a
memory with `memory_write` when it learns something worth keeping
(`memory.md` 4), so a compaction has no backlog to rescue.

1. **Summary update**, in the cached form `summary.go` uses today: the request as
   last sent up to the cut, plus the update prompt, so only the prompt is
   prefilled. The transcript form and the reasoning fallback stay as they are.
2. **Assembly.** Code builds the deterministic sections from rows at that moment,
   writes the compaction entry, and rebuilds the preamble. The rebuild picks up
   memory and agent-definition changes, including new tools (section 4).

A compaction never blocks the user and never fails for want of a model. If step 1
fails, the compaction proceeds with the deterministic sections and the previous
`[Conversation]` unchanged, and a maintenance retry later folds in the span it
missed, read back from history.

Subagent, routine and dream sessions keep Pi's compaction as it is today, with
their agent definition's prompts.

### History recall

- **Index.** `entries.tsv`, Postgres full-text search over user and assistant text,
  tool-call names and arguments, notices and compaction summaries, plus `pg_trgm`
  for regular expressions and identifiers. Both ship with Postgres. Tool results
  are indexed apart and searched only on request. Subagent sessions are indexed
  too.
- **`history_search {query, since?, until?, subagent?, project?, include_results?, page?}`.**
  Words or a regular expression. Ranked by `ts_rank_cd`, newest first on ties.
  Five hit groups per page; each hit shows the neighbouring turn as context, with
  the entry id, time, and the session or subagent name. Without a query, it lists
  the entries just before the oldest one still in context.
- **`history_read {entry | around, subagent?, compaction?, limit?, full?}`.**
  A window of messages, untruncated with `full`. `compaction: N` returns that
  compaction's summary.
- Every `(#id)` in a summary, a notice or a search result is an argument for
  `history_read`.
- **Labels carry over.** A hit drawn from a private source sets `sensitive` on the
  session (`sentry.md` 6). A hit from an untrusted tool result comes back inside
  the `<untrusted>` wrapper it was ingested with.

### Tests

- A golden test of the deterministic sections, built from fixture rows.
- A compaction whose summary call fails still compacts, and the retry folds the
  span in later.
- History search over fixture sessions: ranking, regular expressions, paging,
  scopes, labels.
- The prefix-stability test covers the home session across compactions: system,
  tools and preamble stay byte-identical when nothing they draw on changed.
- `evals/compaction` (opt-in): a scripted multi-week home history, questions asked
  after N compactions, with and without the history tools, recorded per model.
