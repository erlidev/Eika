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
- agents, sessions, projects and delegation

## 1. Endpoints, models and classes

### Endpoints

A provider row (kept from today) gains these fields:

| Field | Meaning |
|---|---|
| `backend` | Detected at probe time: `llamacpp`, `vllm`, `sglang`, `tabby`, `openai`, `other`. The user can override it |
| `pool` | Hardware group name. Endpoints in one pool share hardware and are serialised against each other. Default: the provider name |
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

Every row in this table is verified against a live server in phase 1, and fixtures
of each response shape are recorded. Where a version lacks a route, the capability
is `none`, and the runtime falls back as section 2 describes.

The effective window of a model is the smaller of its row's `context_window` and
the per-slot context the probe reported. A model whose configured window is larger
than its slot gets a warning in Settings. This is the commonest cause of silent
truncation on llama.cpp with `-np` above 1.

### Model kinds and classes

A model row gains `kind`: `chat` (today's rows), `decision` (System One, see
`sentry.md` section 9) or `embedding`.

Agents and harness tasks never name a model. They name a class, and the setting
`model_classes` maps each class to a model row:

| Class | Kind | Used for |
|---|---|---|
| `deep` | chat | Hard reasoning, dreaming, and anything an agent definition marks deep. May be a cloud model |
| `main` | chat | The main agent and most tasks |
| `fast` | chat | Titles, summaries for notifications, the reviewer when no decision model is set, routine pre-checks |
| `decision` | decision | Sentry's reviewer and monitor, injection screening, routing, "is this worth a notification" |
| `embedding` | embedding | Hybrid memory search (optional; phase 9) |

A class with no model falls back along `deep -> main` and `fast -> main`. `decision`
falls back to a chat model emulating the protocol (`sentry.md` 9.3). An unset
`embedding` disables vector search. The UI shows which classes share an endpoint,
since a shared endpoint means they contend for the same slots.

`model_classes` replaces `utility_models`. The session title task moves to `fast`.
The decision that a task with no model does not run still holds for `embedding`.
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
  1. `interactive`: a home or direct session where the user sent the message being
     answered.
  2. `foreground`: a task started from an interactive turn in the last 10 minutes,
     or one the user has open.
  3. `background`: routines, wakeups, other tasks.
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
- **Background budget.** The settings `background_budget` (prefill plus decode
  seconds per day) and `quiet_hours` limit routines; maintenance has its own
  budget. A routine that would exceed the budget is deferred and says so in
  Activity.
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
              project brief, or the task brief for a task session
              the date the session started, and the user's time zone and locale
messages    append-only conversation
              each user-side message carries a <context> block at its end:
              current time, channel, notices (task results, approvals resolved,
              memory or skill changes since the preamble, a halt lifted)
```

Rules (these come from Manus's context-engineering notes and Hermes's heartbeat
design):

- Nothing in system or tools changes during a session. Editing an agent
  definition applies to new sessions, and to an existing one only when it compacts.
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
| `Options.Gate` | Sentry client. `dispatch` asks `Gate.Decide` before every tool call and `Gate.Ingest` on every result from an untrusted source. A deny returns a normal error result naming the reason. An `ask` parks the call (below) |
| `Options.Clock` | Injected time for the `<context>` block and for tests |
| `Options.Limits` | Max model calls per run (home 40, task 200), max identical consecutive tool calls (warn at 3, stop at 5), wall time per task (default 2 h), stream stall timeout |
| Stall timeout | No bytes for `stall` seconds aborts the attempt as retryable. Before the first token the limit scales with predicted prefill: `30 s + predicted_prefilled / measured_prefill_rate x 2`, since 200k tokens of dense prefill can take minutes |
| Parking | A tool waiting on an approval or on the user blocks its goroutine, as `ask_user` does today. It holds no inference lease, since leases cover model calls only. Run state becomes `waiting`, so Activity shows it |
| Durable questions and approvals | The `ask_user` broker and the approval wait persist their pending items (a `questions` row; approvals live in Sentry), so a restart does not lose them |
| Resume after restart | On start, task, routine and dream runs that were `running` or `waiting` resume from their last resumable entry with a notice ("Eika restarted at <time>; continue"). A tool call whose approval is still pending is re-dispatched and Sentry returns the same approval (approvals are keyed by session and tool-call id). Interrupted home and direct replies are not resumed: the transcript gets a notice and the user's message stays answerable |
| Images in tool results | Screenshots reach a vision-capable model as a user message with the image right after the tool result. Chat Completions accepts images only in user messages. This resolves the item `DECISIONS.md` lists as deferred |
| Pre-compaction flush | Before compacting a home, direct or project session, one call on the same prefix asks the agent to save durable memories (OpenClaw's pattern). The cache makes it cheap. Then the compaction runs |
| Idle compaction | A session over 60% of its window that has been idle for 5 minutes, with its pool idle too, is compacted at `maintenance` priority, so the user's next message does not pay for it |
| Compaction prompts per agent | The summary format is coding-tuned today. Each agent definition gets its own; the home prompt keeps commitments, open threads, people and decisions |
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
| `class` | Model class |
| `prompt` | System prompt body |
| `tools` | Fixed, sorted list of built-in tool names |
| `access` | Booleans that Sentry and the preamble read: `memory` (none, read, write), `private_data` (may hold B), `external_effects` (may cause C), `computer` (none, project, home) |
| `skills`, `plugins` | Which catalogs its preamble lists: all, none, or a list |
| `limits`, `compaction` | As in section 5 |
| `report` | How a task by this agent reports back: `always`, `on_completion`, `silent` |

Built-ins:

| Agent | Class | Tools | Access | Role |
|---|---|---|---|---|
| `eika` | main | memory_search, memory_read, memory_write, memory_forget, history_search, skill_use, skill_file, task_start, task_status, task_message, task_cancel, project_list, project_open, project_create, project_update, routine_create, routine_list, routine_update, routine_delete, wake_me, plugin_find, plugin_call, web_search, request_secret, ask_user (direct sessions only) | memory write, private data, external effects, no computer | Converses, remembers, plans, delegates |
| `worker` | main | bash, file_read, file_write, file_edit, web_search, web_fetch, skill_use, skill_file, memory_search (project), report | memory read, private data, external effects via Sentry, computer: project | Does work on a computer |
| `researcher` | fast or main | web_search, web_fetch, report | no memory, no private data, no effects | Reads the web and returns findings |
| `browser` | main (vision if available) | browser_* (`browser.md`), report, ask_user | no memory; private data through logged-in sites; effects gated per action | Drives the browser |
| `dreamer` | deep | memory_list, memory_read, memory_write, memory_merge, memory_archive, history_search, history_read, dream_report | memory write, no network, no effects | Consolidates memory (`memory.md` 7) |

- The main agent has no `bash`, so its context stays small and its prefix steady.
  Work on a computer goes to `worker`.
- `file_read`, `file_write` and `file_edit` come back. They are thin tools over the
  executor, added because small local models edit files far more reliably with
  them than with heredocs. Today's `DECISIONS.md` reasons for bash-only applied
  to a coding agent on a frontier model.

## 7. Sessions

The session tree, entries, runs, steering and follow-up queues, and compaction
entries are kept. Changes:

- `sessions` gains these columns:
  - `kind`: `home`, `direct`, `task`, `routine` or `dream`. It replaces
    user/fork/agent.
  - `agent`: the definition name.
  - `project_id`: nullable.
  - `computer_id`: nullable.
  - `parent_session_id`: the delegator; kept.
  - `channel`: for home sessions.
  - `state`: `idle`, `running`, `waiting`, `done`, `failed` or `cancelled`.
  - `brief`: for a task.
  - `result`: a task's final report.
  - `taint`: Sentry's labels, mirrored for the UI.
  It loses `workspace_id`, `tools`, `profile_id` and `overrides`.
- Entry kinds gain `preamble` (the snapshot above) and `notice` (harness text shown
  to the user, also sent as part of the next user-side `<context>`). They lose
  `commit_sha`.
- **Home sessions.** There is one home session per channel (web first). It never
  ends: compaction keeps it within its window, and memory carries what matters
  across compactions. The web channel's home session is created at setup. Each
  channel keeps its own messages while memory is shared, which is how ChatGPT
  dots and Muse handle multiple channels.
- **Branching.** The tree stays in storage, but the home UI is linear. Rewind
  (moving the head back) stays for direct sessions. Fork-with-workspace is gone.
- **Pagination.** `GET .../entries?before=<id>&limit=` serves history in pages, since
  a home session grows without end. Replay since an entry id stays as it is.

## 8. Projects

A project is a long-lived home for one area of the user's life or work: "Kitchen
renovation", "Job search", "Taxes 2026", "Eika itself". It holds:

| Field | Meaning |
|---|---|
| `name`, `slug`, `description` | Identity. The description is the one line the main agent's preamble lists |
| `status` | `active`, `paused`, `done` or `archived`. Only active projects appear in the home preamble |
| `instructions` | User-written text. Agents read it and never write it |
| `computer_id`, `directory` | Where its files live. The default is `/home/eika/projects/<slug>` on the `home` computer. A project can ask for a computer of its own (another image, stricter egress) |
| `agent` | Default agent for sessions started in it (usually `worker`) |
| `skills`, `plugins` | Which ones its sessions list |
| `created_by` | `user` or `eika` |

Memory has a project scope (`memory.md`). A pinned project memory named `status`
is the living summary that agents keep current. Routines can belong to a project.

### How Eika uses projects

This works the way a person uses Claude Code: open the project, start or resume a
session, give it a brief, and come back later.

- `project_list` returns active projects with their one-liner and status.
- `project_open {project}` returns the description, instructions, pinned project
  memories, open and recent sessions (title, agent, state, the first line of each
  result), and routines. Opening a project changes nothing in the main agent's
  prefix: the answer is a tool result.
- `task_start {agent, brief, project?, report?}` starts a task session in the
  project. Its preamble carries the project brief, its instructions and its memory
  index, so the task gets the project's context without the main agent carrying it.
- `task_message {task, text}` steers a running task, or resumes a finished one
  with a new message. The task's own context comes back, from its compaction
  summary and tail, and the slot cache is used where it still holds.
- `project_create` and `project_update` let Eika create projects and change
  descriptions and status, but not instructions. Creating a project needs no
  approval, since it changes nothing outside Eika.

The user sees the same projects in the UI and can open any session to talk to its
agent directly (a `direct` session), or start one themselves.

## 9. Delegation and results

```
eika (home)  --task_start-->  task session (worker, project P)
  replies "started", the turn ends       queued at foreground priority
                                         runs; may park on approvals and questions
                                         ends with report {summary, outputs, follow-ups}
home session  <--notice "task T finished: <summary>"--
  next user-side context carries it; with report=always, a background
  turn on the home session tells the user
```

- **Asynchronous by default.** `task_start` returns at once. With one slot, a
  blocking spawn would evict the main context for the whole task and re-prefill
  it afterwards. `wait: true` exists for short tasks where the answer is needed in
  the same turn.
- A task ends by calling `report {summary, outputs[], follow_ups[]}`. A task that
  ends without calling `report` gets one written from its last message, as
  `summaryOf` does today.
- Questions a task asks (`ask_user`) and approvals it needs go to the Inbox, not
  the home transcript. The home session gets a notice that task T is waiting on
  the user.
- **Limits.** Depth 1 by default: home starts tasks, and tasks do not start tasks.
  The setting `task_max_depth` allows 2 for `worker -> researcher`. At most
  `task_max_active` tasks are runnable at once (default 4). They still execute one
  at a time on a one-slot pool. The reserve-under-lock logic of today's spawner
  carries over.
- Abort and cancel carry over from the spawner: cancelling a session cancels its
  tasks, and `Shutdown` stops tasks that outlive their parent.

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
  still passes Sentry and taint rules still apply.
- Skills are mounted read-only at `/skills`; see `skills-and-plugins.md`.
- Plugin cells are computers too, labelled `eika.role=plugin`. No agent tool can
  target them.
