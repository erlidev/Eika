# Runtime: context, loop, agents, sessions, projects

This document holds runtime work for later phases and deferred features.
Phase 1's implemented endpoints, model classes, token accounting, inference
queue, context block and loop guards are in
[Architecture](../ARCHITECTURE.md#endpoints-models-and-classes).
Their reasons are in [Decisions](../DECISIONS.md#agent-loop-and-providers).
Sections 1 to 3 moved there; the remaining numbers stay unchanged so references
to later sections remain valid.

## Remaining inference work

- Agents use model classes and set priority per call. Foreground marks a
  subagent started or messaged from an interactive turn in the last ten minutes,
  or one the user watches. Routines, wakeups and other subagents use background.
  Subagent runs take foreground or background from the turn that starts them
  (P2-13), and a user message joining a background home turn raises its next
  call to interactive (P2-15); the ten-minute decay and watching are still to
  come.
- Activity shows each pool's running and waiting calls, per-day prefill seconds
  and tokens, cache-hit rate over calls with reported counts, and compute by
  priority. Settings aggregates are still to come.
- Additional backend and cache-mode live verification, disk slot save/restore
  and LMCache remain [deferred](../DEFERRED.md#phase-1-built-kept-and-not-verified-further).

## 4. Context layout

Later phases extend the current context with agent definitions and a stored
preamble, in this order. Each layer stays unchanged while messages are added:

```
system      identity and rules of the agent definition            stable for the definition's life
tools       the definition's fixed tool list, sorted, byte-stable schemas
preamble    one entry written when the session starts and replaced only by compaction:
              user instructions (global, then project)
              pinned memories, then the memory index (global, then project)
              skills catalog, plugin catalog
              the agents Eika can delegate to
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
- Time is already stored at the tail of each user-side message. Phase 2 adds
  channel and notices to that block; the current format is in
  [Architecture](../ARCHITECTURE.md#context-layout-and-prefix-stability).
- Tools never appear or disappear mid-session. Plugins and skills are reached through
  fixed generic tools (`skills-and-plugins.md`), so installing one never breaks a
  live prefix.
- Untrusted content enters only as a tool result, wrapped by Sentry in
  `<untrusted source="...">` boundaries after scanning (`sentry.md` 6).
- Extend the existing prefix-stability test with notices and approvals as they
  land. The current test is described in
  [Architecture](../ARCHITECTURE.md#context-layout-and-prefix-stability).

Large tool output will be spilled to
`/home/eika/.eika/outputs/<call id>.txt` on the computer, with its path in the
result. The model can read it with `bash` or the planned `file_read` tool.
Current bash output is truncated in the middle; it is not stored in a file.
`file_read` is capped at 32 KiB and has no offset, so reading spilled output
needs a range parameter, to be decided in the package that adds spilling.

## 5. The loop

The current loop, limits, clock, stall guard, tool-call ids and reasoning replay
are described in [Architecture](../ARCHITECTURE.md#agent-core). The remaining
changes are:

| Change | Detail |
|---|---|
| `Options.Executor` becomes `Options.Computer` | A handle with the executor plus computer id and directory. Tools that need none still mark themselves `Standalone` |
| `Options.Gate` | The Sentry gate. `dispatch` asks `Gate.Decide` before every tool call and `Gate.Ingest` on every result from an untrusted source. The model is never told about the gate: a deny returns a normal `Blocked: <reason>` error result (`sentry.md` 4), and an `ask` either waits briefly, which looks like a slow tool, or returns `Queued` (`sentry.md` 7). The loop stays standard |
| Parking | A tool waiting on an approval or on an answer blocks its goroutine, as `ask_user` does today. It holds no inference lease, since leases cover model calls only. Run state becomes `waiting`, so Activity shows it |
| Approvals | Approvals are durable rows (`sentry.md` 7). A subagent's `ask_user` waits in memory, since a restart ends the run that asked |
| After a restart | Startup recovery is implemented; see [Architecture](../ARCHITECTURE.md#startup-recovery). Pending approvals will expire when durable approvals are added. Routines will record interruption when routine runs are added |
| Images in tool results | Deferred with the browser (`DEFERRED.md`) |
| Compaction | Rebuilt for the home session: deterministic sections, a rolling summary, and idle compaction (section 11). Subagent and routine sessions keep today's Pi compaction with their definition's prompts |

## 6. Agents

Built-in definitions, class and prompt overrides, and per-agent compaction prompts
are implemented; see [Architecture](../ARCHITECTURE.md#agent-definitions).
Runtime adoption is implemented; Settings is still planned. The table below is
the target tool list; each package adds its tools only when they are implemented. There are
no user-defined agents for now (`DEFERRED.md`).

| Field | Meaning |
|---|---|
| `name`, `description` | Identity. The description is what the main agent sees when it delegates |
| `class` | Model class, which the user sets for every agent |
| `prompt` | System prompt body |
| `tools` | Fixed, sorted list of built-in tool names |
| `limits`, `compaction` | As in section 5 |

Built-ins:

| Agent | Class | Tools | Role |
|---|---|---|---|
| `eika` | main | memory_search, memory_read, memory_write, memory_forget, history_search, history_read, skill_use, subagent_start, subagent_message, subagent_status, subagent_cancel, project_list, project_open, project_create, project_update, routine_create, routine_list, routine_update, routine_delete, wake_me, plugin_find, plugin_call, web_search | Converses, remembers, plans, delegates. Holds private data, and is the only agent with memory and plugins. No computer |
| `worker` | main | bash, file_read, file_write, file_edit, web_search, web_fetch, skill_use, report, ask_user | Does work on a computer, in a project's directory. What it learns goes in its report, and Eika saves it |
| `researcher` | fast | web_search, web_fetch, report, ask_user | Reads the web and returns findings. No private data and no effects |

The `browser` and `dreamer` agents come back with their phases (`DEFERRED.md`).

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
  question is simply its reply, and a reply to a background turn sends a push.
  Subagents keep `ask_user`, which goes to Eika, not to the user.
- `file_read`, `file_write` and `file_edit` come back. They are thin tools over the
  executor, added because small local models edit files far more reliably with
  them than with heredocs. Today's `DECISIONS.md` reasons for bash-only applied
  to a coding agent on a frontier model.

## 7. Sessions

The session tree, entries, runs, steering and follow-up queues, and compaction
entries are kept. Changes:

- `sessions` gains these columns:
  - `kind`: `home`, `subagent` or `routine`. It replaces `user`, the only kind
    left.
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
  - `last_active_at`.
  It keeps nullable `computer_id`; it loses `tools`, `profile_id` and `overrides`
  only after their callers move and the upgrade preserves old data.
- Entry kinds gain `preamble` (the snapshot above) and `notice` (harness text shown
  to the user, also sent as part of the next user-side `<context>`). User and
  assistant entries in the home session record their `channel`.
- **One home session.** The user works with Eika the way they would with one
  person: a single conversation that never ends, while Eika keeps track of many
  lines of work through its subagents (section 9). The home session is ensured on every start, including token-only setup. Compaction keeps it within its window, and history recall brings back
  anything compacted (section 11).
- **Every channel feeds it.** Web first; Discord or Matrix later
  (`interface.md` 6). Each user-side `<context>` names the channel the message came
  from, and the reply goes back to that channel. Proactive messages (reports,
  wakeups, routine results) appear in the home session and send a push. A message
  that arrives while a run is going uses the steering and follow-up queues, as today.
- **Room for more later.** The home session is found by `(owner, name)`, not
  assumed unique, so further home sessions (another person, another persona) need
  no schema change. There is one for now.
- **No direct sessions.** The user talks only to Eika. Subagent and routine
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
| `directory` | Where its files live, by default `/home/eika/projects/<slug>` on the `home` computer |

A project's living summary is a `STATUS.md` in its directory, which its workers keep
current. Routines can belong to a project.

### How Eika uses projects

This works the way a person uses Claude Code: open the project, hand work to a
subagent with a brief, and come back to that subagent later.

- `project_list` returns active projects with their one-liner and status.
- `project_open {project}` returns the description, instructions, `STATUS.md`, the
  project's subagents (name, agent, state, the first line of the last report), and
  routines. Opening a project changes nothing in the main agent's prefix: the answer
  is a tool result.
- `subagent_start {..., project}` starts a subagent in the project. Its preamble
  carries the project brief and its instructions, so the subagent gets the project's
  context without the main agent carrying it.
- `project_create` and `project_update` let Eika create projects and change
  descriptions and status, but not instructions. Creating a project needs no
  approval, since it changes nothing outside Eika.

The user sees the same projects in the UI, edits their instructions, and can watch
any of their subagents (`interface.md` 1).

## 9. Subagents

The session lifecycle service (P2-12), the delegation tools with their run
binding (P2-13), reports and limits (P2-14) and report notices (P2-15) are
implemented; see [Architecture](../ARCHITECTURE.md#subagent-lifecycle) and
[Notices](../ARCHITECTURE.md#notices). Question relay is documented under [Subagent questions](../ARCHITECTURE.md#subagent-questions).

Eika keeps track of several lines of work the way subagents traditionally work: it
hands each one to a subagent with a brief, the subagent works in its own context,
and it reports back. Two things differ from one-shot subagents such as Claude
Code's Task tool or Eika's old `spawn_agent`, both because Eika is one long-lived
session, and local contexts can be scarce:

- **Asynchronous by default.** `subagent_start` returns at once. A blocking spawn
  would hold the main context idle for the whole run, and on a setup with few
  slots it would evict that context and re-prefill it afterwards.
- **Persistent and named.** A subagent keeps its session after it reports. A later
  `subagent_message` to the same name resumes it in its own context, so "plumber"
  or "flights-lisbon" is also the thread of that work. There is no separate thread
  concept.

```
eika (home)  --subagent_start plumber-->  subagent session (worker, project Kitchen)
  replies "on it", the turn ends              queued at foreground priority
                                              runs; may wait on approvals and questions
                                              ends with report {summary, outputs, follow_ups}
home session  <--notice "plumber (worker) reported: <summary>"--
  ...days later...
eika  --subagent_message plumber "get a second quote"-->  same session resumes
```

| Tool (Eika's) | Behaviour |
|---|---|
| `subagent_start {agent, name, brief, project?}` | Starts a named subagent and returns its name. A name already in use returns that subagent's status instead, so Eika messages it rather than starting a duplicate |
| `subagent_message {subagent, text}` | Resumes an idle, failed or cancelled subagent with a new message in its own context (its compaction summary and tail; the slot cache where it still holds). To a subagent waiting on a question, the text is the answer. A running one answers that it is running, and Eika can cancel it or wait for its report |
| `subagent_status {subagent?}` | Without a name, the board: each subagent's name, agent, project, state, last activity, the first line of its last report, and anything it waits on. With a name: its brief, full last report, pending questions, approvals and wakeups |
| `subagent_cancel {subagent}` | Stops its current run. It stays resumable |

Inside a subagent, `report {summary, outputs[], follow_ups[]}` ends the current
assignment.

- **State is set by code, never by the model.** `running`; `waiting` (on a
  question, an approval or a wakeup, with the reason); `idle` after a report;
  `failed`; `cancelled`.
- **Reports.** A run that ends without calling `report` is sent one message in its
  own session: "Draft your report now with the report tool." If it still does not
  call `report`, its final message becomes the report, as the removed spawner's
  `summaryOf` did (`PLAN_LOG.md`, "phase 0 notes for later phases").
  Every report becomes a notice in Eika's next `<context>`: "plumber (worker)
  reported: <summary>; follow-ups: …", and starts a background turn on the home
  session, where Eika tells the user what matters. A report and a question pass
  the injection screen on the way (`sentry.md` 6), since a subagent may have read
  something hostile; a removed part shows as `[removed:suspect]`.
- **Questions** a subagent asks with `ask_user` go to Eika. The subagent parks
  (`waiting`), the question becomes a notice, and a background home turn starts.
  Eika answers with `subagent_message`, or asks the user in its reply and relays the
  answer. Eika decides what the user needs to see.
- **Approvals.** No built-in subagent has a tool that asks (`sentry.md` 5). If one
  ever does, the approval goes to the Inbox through Sentry, not through Eika: a
  slow tool call to the subagent, and "waiting on the user" on the board.
- **Wakeups** a subagent sets with `wake_me` resume that subagent, not the home
  session.
- **The board** is a deterministic section of every home compaction summary
  (section 11), and `subagent_status` gives it live between compactions.
- **Limits.** Eika starts subagents, and subagents do not. At most
  `subagent_max_running` subagents are runnable at once (default 4). They still
  execute one at a time on a one-slot pool. The reserve-under-lock logic of
  the removed spawner carries over.
- **Abort and shutdown** carry over from the removed spawner. Aborting a home reply does
  not cancel its subagents, since they outlive the turn that started them. Halt
  stops everything. `Shutdown` stops runs (section 5).

## 10. Computers

The `computer` package and the `home` computer are built (`ARCHITECTURE.md`,
"Sandboxes"). What is still to come:

- `home` holds project directories and spilled outputs.
- Computers join `eika_sandbox_internal` only. The `open` egress mode, which
  bypasses the proxy, is removed. A permissive allowlist (`*`) remains, so traffic
  still passes the proxy, and the blocklist and halt still apply.
- Skills are mounted read-only at `/skills`; see `skills-and-plugins.md`.
- Stdio plugins share one computer labelled `eika.role=plugin`. No agent tool can
  target it.

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
algorithmic compactor for Pi: a deterministic section, cuts at turn boundaries, and
search over the compacted history. Code writes what can be read from rows. The model
writes only what needs judgement.

### The summary

The board and the rolling `[Conversation]` are built (P2-20); see
[Architecture](../ARCHITECTURE.md#context-layout-and-prefix-stability). The board's
heading states its time. The tag's `as_of`, `entries` and `compactions` attributes
and the footer come with the history tools (P2-23, P2-24), which they point to.
The cut at user-turn boundaries and the snapshot refresh of preamble, prompt and
tools are built too (P2-21; [Architecture](../ARCHITECTURE.md#context-layout-and-prefix-stability)).
Idle compaction (P2-22) is still to come.

```
<summary as_of="2026-11-03 21:40" entries="#1-#5321" compactions="14">
[Subagents]      deterministic, from session rows: open subagents, then those that
                 reported since the last compaction. Name (agent, project), state,
                 last activity, the first line of the last report
[Conversation]   LLM, rolling update (below)
footer           "history_search and history_read recover anything compacted"
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
nothing the board or memory already says.

Chapters were considered: one summary per compacted span, written once and frozen,
with a growing list of chapter titles. They bound how often a fact is rewritten,
but they need the current state kept outside the summary, and they add a concept.
Rolling comes first. `[Ledger]`, `[Recent turns]` and `(#id)` references return if
real use shows the summary losing commitments (`DEFERRED.md`).

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

A compaction never blocks the user. If step 1 fails at idle, the session stays as it
is and the next idle period tries again. The threshold and the overflow retry stay
as safety nets.

Subagent and routine sessions keep Pi's compaction as it is today, with their agent
definition's prompts.

### History recall

- **Index.** `entries.tsv`, Postgres full-text search over user and assistant text,
  tool-call names and arguments, notices and compaction summaries. Subagent sessions
  are indexed too. Tool results are not.
- **`history_search {query, since?, until?, subagent?, project?, page?}`.**
  Ranked by `ts_rank_cd`, newest first on ties. Five hit groups per page; each hit
  shows the neighbouring turn as context, with the entry id, time, and the session
  or subagent name. Without a query, it lists the entries just before the oldest one
  still in context.
- **`history_read {entry | around, subagent?, limit?, full?}`.**
  A window of messages, untruncated with `full`.

### Tests

- A golden test of the `[Subagents]` section, built from fixture rows.
- A compaction whose summary call fails leaves the session as it was.
- History search over fixture sessions: ranking, paging and scopes.
- The prefix-stability test covers the home session across compactions: system,
  tools and preamble stay byte-identical when nothing they draw on changed.
