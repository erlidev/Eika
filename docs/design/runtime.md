# Runtime: remaining work

Phase 1 to 3 runtime behavior is in [Architecture](../ARCHITECTURE.md#agent-core),
with its choices in [Decisions](../DECISIONS.md#agent-loop-and-providers).
This document holds later and deferred changes.

## Remaining inference work

- Foreground will mark a subagent started or messaged from an interactive turn
  in the last ten minutes, or one the user watches. Routines, wakeups and other
  subagents will use background priority.
- Activity will show each pool's running and waiting calls, daily prefill
  seconds and tokens, cache-hit rate over calls with reported counts, and
  compute by priority. Settings aggregates remain to be built.
- Additional backend and cache-mode live verification, disk slot save/restore
  and LMCache remain [deferred](../DEFERRED.md#phase-1-built-kept-and-not-verified-further).

## 4. Context layout

Later phases extend the stored preamble after global and project instructions:
first pinned memories, then the memory index, each in global then project scope;
then the skills and plugin catalogs. The snapshot is refreshed at compaction,
not on every run. Memory and skill changes since it was written arrive as
notices at the tail. Skills and plugins use fixed generic tools, so installing
one does not change a live tool prefix. Sentry wraps untrusted tool results in
`<untrusted source="...">` boundaries after scanning. Extend the prefix-stability
test with notices and approvals as they land.

Large tool output will spill to `/home/eika/.eika/outputs/<call id>.txt` on the
computer, with its path in the result. The model can read it with `bash` or
`file_read`. Bash output is currently truncated in the middle, not stored.
`file_read` is capped at 32 KiB and has no offset; decide its range parameter
in the package that adds spilling.

## 5. The loop

The current loop, limits, clock, stall guard, tool-call ids and reasoning replay
are described in [Architecture](../ARCHITECTURE.md#agent-core). The remaining
changes are:

| Change | Detail |
|---|---|
| `Options.Executor` becomes `Options.Computer` | A handle with the executor plus computer id and directory. Tools that need none still mark themselves `Standalone` |
| `Options.Gate` | The Sentry gate. `dispatch` asks `Gate.Decide` before every tool call and `Gate.Ingest` on every result from an untrusted source. The model is never told about the gate: a deny returns a normal `Blocked: <reason>` error result (`sentry.md` 4), and an `ask` either waits briefly, which looks like a slow tool, or returns `Queued` (`sentry.md` 7). The loop stays standard |
| Parking | An approval waits without holding an inference lease; Activity shows the waiting state |
| Approvals | Approvals are durable rows (`sentry.md` 7). A subagent's `ask_user` waits in memory, since a restart ends the run that asked |
| After a restart | Startup recovery is implemented; see [Architecture](../ARCHITECTURE.md#startup-recovery). Pending approvals will expire when durable approvals are added. Routines will record interruption when routine runs are added |
| Images in tool results | Deferred with the browser (`DEFERRED.md`) |

## 6. Agents

The target tool lists include shipped tools for context. Each later phase adds
only its own tools; the current lists are in
[Architecture](../ARCHITECTURE.md#agent-definitions).

| Agent | Class | Target tools | Role |
|---|---|---|---|
| `eika` | main | memory_search, memory_read, memory_write, memory_forget, history_search, history_read, skill_use, subagent_start, subagent_message, subagent_status, subagent_cancel, project_list, project_open, project_create, project_update, routine_create, routine_list, routine_update, routine_delete, wake_me, plugin_find, plugin_call, web_search | Converses, remembers, plans and delegates. Only Eika has memory and plugins; it has no computer |
| `worker` | main | bash, file_read, file_write, file_edit, web_search, web_fetch, skill_use, report, ask_user | Works on a computer in a project directory; Eika saves what its report teaches it |
| `researcher` | fast | web_search, web_fetch, report, ask_user | Reads the web and reports; no private data or effects |

The browser and dreamer agents remain deferred. Their target roles and tools are
specified in their respective design documents.

## 7. Sessions

Routine sessions and external channels remain to be built. Channel messages will
feed home, record the channel on user and assistant entries, carry it in context,
and receive replies on that channel;
see [Interface](interface.md#6-channels).

## 8. Projects

Routines will be able to belong to a project and appear when it is opened.

## 9. Subagents

Sentry will screen reports and questions and mark removed text as
`[removed:suspect]`. Halt will stop all subagents. Approvals and wakeups will add
waiting reasons; a wakeup resumes the subagent that set it. Detailed `subagent_status`
will include pending approvals and wakeups. If a subagent has a tool that asks,
Sentry routes the approval to Inbox rather than through Eika. The tool waits,
and the board says it is waiting on the user.

## 10. Computers

The `computer` package and the `home` computer are built (`ARCHITECTURE.md`,
"Sandboxes"). What is still to come:

- Spill large tool output to files on `home`; the model needs a way to read ranges.
- Computers join `eika_sandbox_internal` only. The `open` egress mode, which
  bypasses the proxy, is removed. A permissive allowlist (`*`) remains, so traffic
  still passes the proxy, and the blocklist and halt still apply.
- Skills are mounted read-only at `/skills`; see `skills-and-plugins.md`.
- Stdio plugins share one computer labelled `eika.role=plugin`. No agent tool can
  target it.

## 11. Compaction and recall

[Compaction](compaction.md) describes the later changes to summaries, cuts,
preparation and history recall. Memory is described in [Memory](memory.md).

Budgets are intended to be shares of the effective window. The intended budget
after compaction is 30–35% of the effective window: each
compaction costs a full re-prefill, so fewer, larger compactions are cheaper.
This is a target to measure, not a guarantee of the current implementation.
There is no memory flush at compaction: Eika uses `memory_write` as it learns
something worth keeping. Assembly builds deterministic sections from rows and
refreshes the preamble, picking up memory and agent-definition changes, including
new tools. The summary update uses the cached prefix and falls back to the
transcript form and reduced reasoning as needed.
