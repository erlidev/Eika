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
| `api` | `responses` or `chat`: which wire protocol Core speaks to the endpoint (below). Chosen at probe time, and the user can override it |
| `slots` | Concurrent requests the pool accepts. Default: 1 for a local backend, 8 for a cloud one |
| `capabilities` | Probe result (jsonb): how the endpoint counts tokens (`chat`, `text`, `none`), whether it reports cache metrics, whether it takes slot hints, the per-slot context it reported |

The probe (`POST /api/providers/probe`, and again on every Core start) reads the
endpoint's own description where it has one:

| Backend | Identify by | Window per slot | Exact counting | Cache metric |
|---|---|---|---|---|
| llama.cpp | `GET /props` | `default_generation_settings.n_ctx` (per slot; `-np` divides `-c` unless `--kv-unified`) | `POST /apply-template` then `POST /tokenize` | `timings.cache_n`, and `prompt_tokens_details.cached_tokens` where present |
| vLLM | `GET /version` plus `/v1/models` | `max_model_len` | `POST /tokenize` with `messages` (and `tools`, `chat_template_kwargs`) | `usage.prompt_tokens_details.cached_tokens` (needs `--enable-prompt-tokens-details`) |
| SGLang | `GET /get_server_info` | `context_length` | `/tokenize` where the version has it | `cached_tokens` in usage where reported |
| TabbyAPI | `GET /v1/model` | `parameters.max_seq_len` | `POST /v1/token/encode` (text, or messages with the template) | none known; shown as unknown |
| Cloud | `/v1/models` | listing fields | none | `prompt_tokens_details.cached_tokens` |

### Responses or Chat Completions

Eika prefers OpenAI's Responses API. It is used statelessly (`store: false`, the
full input sent on every call, reasoning items passed back as the server returned
them). Its typed items for reasoning, tool calls and tool outputs round-trip
thinking and tool calls without the `reasoning_content` and chat-template
conventions that each Chat Completions backend handles a little differently. Chat
Completions stays as the fallback and as the only choice for endpoints that lack
the route.

- **Default.** `responses` for llama.cpp. For any other endpoint, `responses` only
  when the probe's conformance check passes: a streamed tool-call round trip with a
  reasoning item, and usage with cache metrics. Otherwise `chat`. The user can
  override per provider.
- **One provider interface.** Both protocols implement `provider.Provider`, so the
  loop, accounting and compaction do not see which one is in use. Exact token
  counting (section 2) works from the equivalent chat-format request, which either
  provider can build.
- **Verified, not assumed.** Support differs by backend and version, so each
  backend's `responses` behaviour is checked against a live server in phase 1 and
  its fixtures recorded. A backend whose support is partial stays on `chat` by
  default.

Every row in the table above is verified against a live server in phase 1, and fixtures
of each response shape are recorded. Where a version lacks a route, the capability
is `none`, and the runtime falls back as section 2 describes.

The effective window of a model is the smaller of its row's `context_window` and
the per-slot context the probe reported. A model whose configured window is larger
than its slot gets a warning in Settings. This is the commonest cause of silent
truncation on llama.cpp with `-np` above 1.

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

A class with no model falls back along `deep -> main` and `fast -> main`. `decision`
falls back to a chat model emulating the protocol (`sentry.md` 9.3). The UI shows
which classes share an endpoint, since a shared endpoint means they contend for
the same slots.

`model_classes` replaces `utility_models`. The session title task moves to `fast`.
For chat classes the fallback is deliberate: a missing `fast` model must not
disable titles. Titles are still skipped when `fast` would fall back onto a
`deep` cloud model.

Naming alternatives that were considered: high/medium/low; System 2/System 1;
thinker/worker/scout. `deep`/`main`/`fast` describe what each class is for and
read clearly in config.

## 2. Exact token accounting

Knowing exactly how much context is in use matters more than any cache optimisation.
Today the next request is estimated from the last measured call plus 4 bytes per
token. That figure is a guess. It can be off by 30% or more for code, CJK text and
JSON, and it says nothing about template overhead.

### Terms

Every model call records these, in a renamed `model_calls` table (formerly
`model_requests`):

| Field | Source | Meaning |
|---|---|---|
| `prompt_tokens` | `usage.prompt_tokens` | Tokens the server tokenised for this request, template included. Ground truth |
| `cached_tokens` | backend field (see the table above) | Part of the prompt reused from cache. NULL when unknown, never 0 by assumption |
| `prefilled_tokens` | `prompt - cached`, or `timings.prompt_n` | Tokens actually processed. NULL when cached is unknown |
| `completion_tokens` | usage | Generated tokens, reasoning included |
| `reasoning_tokens` | `completion_tokens_details.reasoning_tokens`, else a tokenisation of the reasoning text | Part of the completion that was reasoning |
| `predicted_prompt_tokens`, `prediction` | the runtime | What the runtime expected before sending, and how: `exact`, `anchored` or `estimated` |
| `window` | effective window | The window this call was judged against |
| `queue_ms`, `prefill_ms`, `decode_ms` | inference queue, timings | Wait before the call, prompt phase, generation phase |
| `endpoint`, `slot`, `class`, `agent`, `priority` | runtime | Attribution |

The window fill after a call is `prompt_tokens + completion_tokens`: what the
KV cache held at the end. The context meter shows this number, and it is exact.

One rule is checked in phase 1 against each backend: whether `prompt_tokens`
includes cached tokens. llama.cpp's `prompt_n` counts processed tokens only and
`cache_n` the reused ones. The accounting code reads each backend's fields by its
documented meaning and never sums fields of unknown meaning.

### Predicting the next request

Before every call the runtime needs the size of the request it is about to send: to
decide on compaction, to lower max output to the room left, and to show the
meter. It uses the best method the endpoint offers:

1. **Exact** (`exact`). The endpoint renders and tokenises the request itself:
   vLLM `/tokenize` with messages and tools, or llama.cpp `/apply-template` followed
   by `/tokenize`. This adds one HTTP round trip and a CPU tokenisation, a few tens
   of milliseconds at 200k tokens, which is negligible beside prefill. Results are
   cached per session by prefix hash, so only the new tail is tokenised again
   whenever the endpoint supports text tokenisation.
2. **Anchored** (`anchored`). This is for endpoints that can tokenise text but not
   render a chat request. The last measured `prompt_tokens` is the anchor. Each
   message added since is tokenised exactly as text, and per-message template
   overhead is added: a per-role constant learned from earlier calls as measured
   total minus tokenised content. Reasoning the template drops from earlier turns
   is subtracted. Whether a template drops it is learned the same way, from
   measurements.
3. **Estimated** (`estimated`). This is for endpoints with no tokenizer route
   (TabbyAPI without messages support, cloud APIs). It uses the anchor plus bytes
   divided by a per-endpoint, per-content-class ratio learned from that endpoint's
   history, starting at 4 until there is data. It is shown with a tilde, and the
   compaction reserve is widened by the ratio's observed error.

After every call the runtime logs `drift = predicted - measured`. An `exact`
prediction with any drift is a bug and appears in the Context inspector. Persistent
drift on `anchored` retrains its constants.

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
marked approximate.

### Where it shows

- Status bar meter: the last call's window fill out of the effective window, the
  cached tokens of that call, and the predicted next request with its method.
- Context panel: the parts with exact or approximate markers, and the remainder line.
- Activity and Settings: per-day prefill seconds and tokens, cache-hit rate
  (`cached / prompt` over calls that report it), background compute used against
  its budget.

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

- **Concurrency.** A pool admits `slots` leases at once; further calls wait.
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
| Resume after restart | On start, subagent, routine and dream runs that were `running` or `waiting` resume from their last resumable entry with a notice ("Eika restarted at <time>; continue"). A tool call whose approval is still pending is re-dispatched and Sentry returns the same approval (approvals are keyed by session and tool-call id). An interrupted home reply is not resumed: the transcript gets a notice and the user's message stays answerable |
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
| `worker` | main (allowed: `main`, `deep`) | bash, file_read, file_write, file_edit, web_search, web_fetch, skill_use, skill_file, memory_search (project), report | memory read, private data, external effects via Sentry, computer: project | Does work on a computer |
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
    user/fork/agent.
  - `agent`: the definition name.
  - `name`: a subagent's handle (`plumber`, `flights-lisbon`), unique under its
    home session.
  - `project_id`: nullable.
  - `computer_id`: nullable.
  - `parent_session_id`: the delegator; kept.
  - `state`: `idle`, `running`, `waiting`, `failed` or `cancelled`, set by code.
  - `brief`: a subagent's first brief.
  - `result`: a subagent's latest report.
  - `sensitive` and `suspicious`: Sentry's labels, mirrored for the UI
    (`sentry.md` 6).
  - `last_active_at`.
  It loses `workspace_id`, `tools`, `profile_id` and `overrides`.
- Entry kinds gain `preamble` (the snapshot above) and `notice` (harness text shown
  to the user, also sent as part of the next user-side `<context>`). They lose
  `commit_sha`. User and assistant entries in the home session record their
  `channel`.
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
  fork. Fork-with-workspace is gone.
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
  call `report`, its final message becomes the report, as `summaryOf` does today.
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
  today's spawner carries over.
- **Abort and shutdown** carry over from the spawner. Aborting a home reply does
  not cancel its subagents, since they outlive the turn that started them. Halt
  stops everything. `Shutdown` stops runs, and durable runs resume on start
  (section 5).

## 10. Computers

The `workspace` package becomes `computer`. It keeps:

- Docker lifecycle, confinement, limits and usage
- the idle stopper
- the eikad copy-in and the executor client
- the `PUT /environment` path

It drops clone, push, fetch, hub grants, worktrees and previews.

- The `home` computer is created at setup from the sandbox image, with a persistent
  volume mounted at `/home/eika`. It holds project directories and spilled outputs.
- **Per-run egress identity.** Every `exec` carries `HTTP(S)_PROXY` credentials
  that Sentry issued for that run, so the proxy attributes traffic to the session
  rather than the container. Processes a run starts in the background keep that
  run's identity. eikad already accepts environment per exec.
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
