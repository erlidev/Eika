# Memory

Memory is what makes Eika the same assistant from one day to the next. Getting it
wrong is worse than having none. A poisoned or stale memory is believed and acted
on, and an index that sprawls costs prefill on every session. The design follows
where the field converged in 2026:

- plain Markdown files
- a small always-loaded index
- on-demand reads and search
- agent writes during the session
- consolidation in the background ("dreaming")
- a screen on writes against poisoning

Each idea is credited where it appears. The approaches compared were Claude Code
auto memory, Codex Memories, Letta Context Repositories, OpenClaw and Hermes.

Dreaming (section 7) is deferred (`DEFERRED.md`). Memory is global, and only `eika`
reads and writes it: briefs carry what a subagent needs.

## 1. Layers

| Layer | Written by | Loaded | Authority |
|---|---|---|---|
| Instructions: "About me", standing rules, per-project instructions | User only, in the UI | Whole, in the preamble | Followed. Not enforcement: anything that must hold is enforced by Sentry |
| Pinned memories (core profile) | Eika and the user; pinning is the user's | Bodies in full, in the preamble, capped | Recall |
| Memory index | Generated from memories | One line per memory, in the preamble, capped | Recall pointer |
| Memories | Eika and the user | On demand: `memory_read`, `memory_search` | Recall |
| Compaction summary | Code and the main agent, at each compaction ([Architecture](../ARCHITECTURE.md#history-recall)) | In the home session's context | Working state |
| Episodic history | The sessions themselves | On demand: `history_search`, `history_read` ([Architecture](../ARCHITECTURE.md#history-recall)) | Evidence |

Instructions and memory are kept apart on purpose. OpenAI advises treating
memories as a recall layer, not as the only source for rules that must always
apply, and Anthropic gives the same advice. Agents may suggest an instruction
change in a reply. They have no tool that writes instructions.

## 2. Storage

Memories are files, and files are the source of truth:

```
/var/lib/eika/memory/            git repository, Core volume
  <slug>.md
```

- **git.** Every change is a commit authored as the agent and session, or the
  user. History, diff and revert come free; Letta and Codex do the same. One
  writer goroutine in Core serialises writes.
- **Index in Postgres.** `core.memories` mirrors frontmatter plus
  `body_tsv tsvector`, `state` and `content_sha`.
  File and row change in one operation: write the file,
  commit, then upsert the row. A crash between steps is repaired at start-up by
  `Writer.Reconcile`, which re-indexes from the files whose SHA differs and holds uncommitted outside edits pending until the P5-05 screen exists.
  `make memory-reindex` rebuilds the whole index from files.
- **No vector database and no embedding model.** Search is Postgres full-text
  search.

### File format

```markdown
---
name: prefers-metric-units
description: Use when giving measurements, recipes or weather; the user wants metric.
type: preference
source: session:01J9... entry:01J9...
created: 2026-10-29T14:03:00Z
updated: 2026-10-29T14:03:00Z
expires:
pinned: false
state: active
---
The user asked for metric units after a recipe in cups (2026-10-29).
Exceptions: none so far.
```

| Field | Rule |
|---|---|
| `name` | Slug, `^[a-z0-9][a-z0-9-]{0,63}$`, unique; also the file name |
| `description` | The "when it is relevant" note, 20 to 200 characters. It is the index line, so it must say when to use the memory, not restate it |
| `type` | `user` (who the user is), `preference`, `person`, `fact`, `procedure` |
| `source` | Where it was learned; links back in the UI |
| `expires` | Optional date for facts that go stale ("the user is travelling until ...") |
| `state` | `active`, `pending` or `archived`; tracked in the file for reindexing |
| `pinned` | Loaded in full. At most 12; the total is capped (section 3) |
| body | At most 2 KiB. Absolute dates only. One subject per memory |

## 3. The preamble: index and pinned bodies

Eika's preamble entry ([Architecture](../ARCHITECTURE.md#context-layout-and-prefix-stability)) includes these, built once when the session
starts and rebuilt at compaction:

```
## About the user and their rules      <- instructions (user-written)
...
## Memory
Pinned:
<body of each pinned memory, ordered by type then name>

Index (memory_read <name> for details, memory_search for anything not listed):
- prefers-metric-units: Use when giving measurements, recipes or weather; ...
- partner-name-sam: Use when the user mentions their partner or plans for two; ...
...
(41 more memories; search to find them)
```

- **Budget.** Pinned plus index 3,000 tokens, counted exactly ([Architecture](../ARCHITECTURE.md#agent-core)).
- **Selection** when over budget: pinned first, then the most recently updated. The
  rest is summarised as a count. Codex's silent head-and-tail truncation is the
  failure to avoid: an index Eika cuts is always cut with a visible count.
- **Order** is deterministic: by type, then name, never by score. This keeps the
  same memories in the same bytes, so two compactions share as much prefix as
  possible. A memory past its `expires` date drops out at the next rebuild.
- Only `state = active` memories appear in the index or as pinned. `pending`
  memories are searchable, with a marker, but never auto-loaded. OpenClaw's rule
  is that untrusted content cannot launder itself into the always-loaded core.
- Memories written during a session do not change its preamble. The tail notice
  names each change since the snapshot (saved, edited, archived, purged) in the
  next `<context>`, by name only, one notice per message; it never wakes an idle
  home or sends a push (P5-10).

## 4. Tools

Every write goes to Sentry as a `memory_write` action first.

| Tool | Arguments | Behaviour |
|---|---|---|
| `memory_search` | `query`, `type?`, `limit?` (10) | FTS rank. Returns name, description and the first 200 characters |
| `memory_read` | `name` | Full file |
| `memory_write` | `name`, `description`, `type`, `body`, `expires?`, `replace?` | Creates, or replaces when `replace` is true. A name collision without `replace` returns the existing memory so the agent merges instead. The write is checked by the rules of section 6 |
| `memory_forget` | `name`, `reason` | Archives the memory (moves it to `state = archived`, file kept in history). The user's "forget X" maps here; the UI can purge |
| `history_search`, `history_read` | See [Architecture](../ARCHITECTURE.md#history-recall) | Search and read every past entry, verbatim |

Size limits are enforced by the tool, which returns a normal error the model can
act on ("description must say when to use this memory, 20 to 200 characters").

### When agents write

The main agent's prompt carries the write policy. It is derived from what Claude
Code saves and skips:

- **Save**:
  - who the user is
  - preferences and corrections, including "don't do X again"
  - people and relationships
  - commitments and dates
  - decisions and why
  - where things live
  - recurring procedures the user describes
- **Skip**:
  - anything already in instructions or another memory (update that one instead)
  - one-off details of a finished task
  - anything derivable from a project's files
  - secrets: Sentry rejects those anyway
- **Absolute dates**, and "as of" wording for facts that change.

Other prompts:

- Save at once. When the user states a preference, a decision is made or a
  durable fact turns up, the agent calls `memory_write` in that turn rather than
  later. There is no flush before compaction, so nothing waits to be saved.
- Only `eika` writes memories. Subagents have no memory tools: what they learn goes
  in their report, and Eika decides what to save. One writer keeps the write policy
  in one prompt, and a report that carries injected text meets the screen twice: on
  its way to Eika, and when Eika saves from it.

## 5. Recall

Retrieval is layered: the index in context, then keyword search. Agents decide when
to search. The research is clear that an agent with file and search tools does as
well as or better than an automatic top-k injection. On LongMemEval-V2, Codex with no
special memory beat the strongest RAG baseline, and Letta's filesystem agent beat
Mem0's graph variant on LoCoMo. So nothing is injected automatically.

## 6. Poisoning

A memory in the index is read at the start of every session, so a planted one is
the attack that lasts. A write never asks the user. It is checked in this order:

| Check at write time | Result |
|---|---|
| The body or description holds a credential pattern | The write is denied, with a reason |
| The injection screen scores the body at or over the threshold (`sentry.md` 6) | `pending`: in the Inbox, searchable with a marker, never in the index |
| Otherwise | `active` |

There is no provenance field. An earlier design looked for instruction-shaped words
outside a span quoted from the user, which sent paraphrased preferences to pending
and passed an instruction that avoided the words. The screen is a classifier
trained on that question. An ordinary rule the user states ("always use metric")
must not go pending, and the phase's live suite checks that it does not; if it
does, memory writes get a threshold of their own. They did: the live suite saw
about 6% of ordinary rules score 0.9 or more, so a memory write goes pending at
`memory_screen_threshold` (default 0.99), a setting apart from the ingest
threshold. The guard cannot separate the two entirely: a rule like "never send a
message without showing me the draft" scored 0.996, as high as most planted
instructions, so a rare ordinary write can still be pending (the Inbox accepts it).

The Inbox shows pending memories with their source session. The user accepts,
edits or rejects, and nothing is pushed: a pending memory can wait.

This is the second screen a planted instruction meets. The first is ingest, on the
page or plugin result it came from, and on the report that carried it to Eika.

## 7. Dreaming

Deferred (`DEFERRED.md`). This section is written for the fuller memory it was
designed with: use counts, a project scope, provenance and a `sensitivity` field. The design
below is the target if it returns.

Dreaming is consolidation in the background. Agents that only append to memory
degrade over time, which is the consensus finding of 2026. Eika's dream is a
routine (`scheduler.md`) running agent `dreamer` at `maintenance` priority:
nightly by default, when the pool is idle and at least `dream_min_entries`
(default 200) new entries have accumulated since the last dream, or as soon as
possible when an index is over budget. Entries are counted rather than sessions,
since the home session never ends.

The phases follow Claude Code's auto-dream, with Codex's usage ranking:

1. **Orient.** List memories per scope with use counts, ages and states, and
   read the previous dream report.
2. **Gather.** `history_search` and `history_read` over history since the last
   dream, including the home session's compaction summaries. Look for corrections ("no, I meant"), decisions, new facts about people
   and dates, and contradictions with existing memories.
3. **Consolidate.**
   - Merge duplicates (`memory_merge`).
   - Rewrite relative dates as absolute ones.
   - Supersede contradicted facts. The new memory records what it replaced.
   - Sharpen descriptions that do not say when the memory applies.
   - Split memories that cover two subjects.
4. **Prune.**
   - Archive memories past `expires`.
   - Archive memories unused for `memory_unused_days` (default 60) whose
     provenance is `inferred`.
   - Never archive `user` provenance without asking.
   - Keep each index under its budget.
5. **Report.** Write `.dreams/<date>.md` with every change and why, commit
   everything as one commit "dream <date>", and post a one-line notice in the home
   session ("Tidied memory: merged 3, archived 5, 1 needs your review").

Guardrails:

- The dreamer has no network and no egress identity. It cannot touch vault,
  instructions or Sentry settings.
- Its writes are `memory_write` actions like anyone's, with the same scans and
  provenance rules, so instruction-shaped text it carries over from history goes
  `pending`. History it reads from private sources makes its session
  `sensitive`.
- Deleting or rewriting a `user`-provenance memory becomes a proposal in the
  Inbox, not a change. The setting `dream_mode` is `auto` (default, applies
  everything else) or `propose` (everything waits for review).
- A dream is one commit, so "undo last dream" is a revert, offered in the UI.
- The `decision` model, when set, does the cheap classification before an LLM
  turn is spent: which pairs to consider merging, and whether a fact contradicts
  another. That is Jev-Mem's typed write path.

## 8. UI

The Memory page, described in [UI](ui.md#8-projects-memory-schedules-plugins-skills):

- filter by type and state
- search
- edit in a Markdown editor with frontmatter fields as a form
- pin, archive, purge
- the pending queue

The Instructions editor sits beside it. Git keeps each memory's history, with no UI
for it yet.

## 9. Evaluation

A live test runs the poisoning scenario: a web page instructing the agent to remember
something, where the expected outcome is a pending, never indexed memory. The fuller
`evals/memory` suites are deferred.
