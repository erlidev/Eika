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
- provenance gating against poisoning

Each idea is credited where it appears. The approaches compared were Claude Code
auto memory, Codex Memories, Letta Context Repositories, OpenClaw and Hermes.

## 1. Layers

| Layer | Written by | Loaded | Authority |
|---|---|---|---|
| Instructions: "About me", standing rules, per-project instructions | User only, in the UI | Whole, in the preamble | Followed. Not enforcement: anything that must hold is enforced by Sentry |
| Pinned memories (core profile, project `status`) | Agents and user; pinning is the user's or the dreamer's | Bodies in full, in the preamble, capped | Recall |
| Memory index | Generated from memories | One line per memory, in the preamble, capped | Recall pointer |
| Memories | Agents and user | On demand: `memory_read`, `memory_search` | Recall |
| Compaction summary | Code and the main agent, at each compaction (`runtime.md` 11) | In the home session's context | Working state |
| Episodic history | The sessions themselves | On demand: `history_search`, `history_read` (`runtime.md` 11) | Evidence |

Instructions and memory are kept apart on purpose. OpenAI advises treating
memories as a recall layer, not as the only source for rules that must always
apply, and Anthropic gives the same advice. Agents may suggest an instruction
change in a reply. They have no tool that writes instructions.

## 2. Storage

Memories are files, and files are the source of truth:

```
/var/lib/eika/memory/            git repository, Core volume
  global/<slug>.md
  projects/<project-slug>/<slug>.md
  .dreams/<date>.md              dream reports
```

- **git.** Every change is a commit authored as the agent and session, or the
  user. History, diff and revert come free; Letta and Codex do the same. One
  writer goroutine in Core serialises writes.
- **Index in Postgres.** `core.memories` mirrors frontmatter plus
  `body_tsv tsvector`, `state`, `use_count`, `last_used_at` and `content_sha`.
  File and row change in one operation: write the file,
  commit, then upsert the row. A crash between steps is repaired at start-up by
  `memory.Reconcile`, which re-indexes from the files whose SHA differs.
  `make memory-reindex` rebuilds the whole index from files.
- **No vector database and no embedding model.** Search is Postgres full-text
  search.

### File format

```markdown
---
name: prefers-metric-units
description: Use when giving measurements, recipes or weather; the user wants metric.
type: preference
scope: global
sensitivity: normal
provenance: user
source: session:01J9... entry:01J9...
created: 2026-10-29T14:03:00Z
updated: 2026-10-29T14:03:00Z
expires:
pinned: false
---
The user asked for metric units after a recipe in cups (2026-10-29).
Exceptions: none so far.
```

| Field | Rule |
|---|---|
| `name` | Slug, `^[a-z0-9][a-z0-9-]{0,63}$`, unique in its scope; also the file name |
| `description` | The "when it is relevant" note, 20 to 200 characters. It is the index line, so it must say when to use the memory, not restate it |
| `type` | `user` (who the user is), `preference`, `person`, `fact`, `project`, `decision`, `procedure`, `feedback` (corrections and confirmed approaches), `reference` (where something lives), `event` (dated) |
| `scope` | `global` or `project:<slug>` |
| `sensitivity` | `normal` or `private` (health, finance, other people's details). `private` marks a session that reads it `sensitive` (`sentry.md` 6) |
| `provenance` | `user` (the user stated it), `inferred` (an agent concluded it), `external` (flagged as possibly derived from untrusted content; section 6) |
| `source` | Where it was learned; links back in the UI |
| `expires` | Optional date for facts that go stale ("the user is travelling until ...") |
| `pinned` | Loaded in full. At most 12 global and 6 per project; the total is capped (section 3) |
| body | At most 2 KiB. Absolute dates only. One subject per memory |

## 3. The preamble: index and pinned bodies

Each session's preamble entry (`runtime.md` 4) includes these, built once when
the session starts and rebuilt at compaction:

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
(41 more memories in scope; search to find them)
```

- **Budgets.** Global pinned plus index 3,000 tokens. Project pinned plus index
  2,000 tokens. Counted exactly (`runtime.md` 2).
- **Selection** when over budget: pinned first, then by `score = use_count
  weight x recency decay + type weight`. The rest is summarised as a count.
  Codex's silent head-and-tail truncation is the failure to avoid: an index Eika
  cuts is always cut with a visible count, and an over-budget index schedules a
  dream.
- **Order** is deterministic: by type, then name, never by score. This keeps the
  same memories in the same bytes, so two sessions started a day apart share as
  much prefix as possible.
- Only `state = active` memories with `provenance` `user` or `inferred` appear in
  the index or as pinned. `external` memories are searchable but never
  auto-loaded. OpenClaw's rule is that untrusted content cannot launder itself
  into the always-loaded core.
- Memories written during a session do not change its preamble. The tail notice
  says "memory saved: <name>". A memory another session changed shows as
  "memory updated since this session started: <name>" in the next `<context>`.

## 4. Tools

Every write goes to Sentry as a `memory_write` action first.

| Tool | Arguments | Behaviour |
|---|---|---|
| `memory_search` | `query`, `scope?` (global, project, all), `type?`, `limit?` (10) | FTS rank plus a small boost for pinned, recently used and frequently used memories. Returns name, description and the first 200 characters. Counts as a use only when followed by a read |
| `memory_read` | `name`, `scope?` | Full file. Increments `use_count` and sets `last_used_at` |
| `memory_write` | `name`, `description`, `type`, `body`, `scope?`, `sensitivity?`, `expires?`, `replace?` | Creates, or replaces when `replace` is true. A name collision without `replace` returns the existing memory so the agent merges instead. Sets provenance from the session's labels (section 6) |
| `memory_forget` | `name`, `reason` | Archives the memory (moves it to `state = archived`, file kept in history). The user's "forget X" maps here; the UI can purge |
| `history_search`, `history_read` | See `runtime.md` 11 | Search and read every past entry, verbatim. `eika` and the dreamer only |

The dreamer has `memory_list`, `memory_merge {names, into}` and `memory_archive`
besides. Size limits are enforced by the tool, which returns a normal error the
model can act on ("description must say when to use this memory, 20 to 200
characters").

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
- Subagents write project-scope memories only, and only of type `project`,
  `decision`, `procedure` or `reference`. The definition's `access.memory`
  enforces this, and Sentry denies the rest.

## 5. Recall

Retrieval is layered: the index in context, then keyword search. Agents decide when to search. The research is clear that an agent with
file and search tools does as well as or better than an automatic top-k
injection. On LongMemEval-V2, Codex with no special memory beat the strongest RAG
baseline, and Letta's filesystem agent beat Mem0's graph variant on LoCoMo.

One automatic step is kept, because it is cheap:

- When a user message arrives in the home session, Core runs
  `memory_search` on it in the background.
- Up to 3 matches that are not pinned and not in the index excerpt are added to
  that message's `<context>` block as names and descriptions only, never bodies.
- It is a hint at the tail. The prefix stays untouched.
- It uses no model, unless a `decision` model is set to filter relevance
  (Jev-Mem's `evidence_sufficient` idea).

## 6. Provenance and poisoning

The write path decides provenance mechanically, never by asking the model:

Sessions are not labelled for having read untrusted content (`sentry.md` 6), so
provenance comes from what the write contains and who makes it, checked in this
order:

| Condition at write time | Provenance | State |
|---|---|---|
| The session is `suspicious`, the injection screen hits the body, or the body has instruction-shaped text ("always", "ignore", "you must") that does not quote the user | `external` | `pending`: in the Inbox review queue, searchable with a marker, never in the index |
| The content quotes the user's message in this turn (checked by substring) | `user` | `active` |
| A subagent writes `type = procedure` or `feedback` | `inferred` | `pending`, since procedures steer future behaviour |
| Otherwise | `inferred` | `active` |

The instruction-shape check runs on every agent write, not only on flagged
ones. A real preference ("always use metric") passes because the user said it,
so it quotes the user's message. Secret redaction runs on every write too, and a
hit denies it.

The Inbox's memory queue shows pending memories with their source. The user
accepts (provenance becomes `user`), edits, or rejects. The dreamer may merge
pending memories with each other but never promotes them.

**Forgetting a source.** Every memory records its source. Disconnecting a plugin or
deleting a project offers "also forget memories learned from it". Duet keeps what
it learned until a full reset; Eika can do better because provenance is per file.

## 7. Dreaming

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

The Memory page, described in `interface.md`:

- filter by scope, type, provenance, state and sensitivity
- search
- edit in a Markdown editor with frontmatter fields as a form
- pin, archive, purge
- per-file history (git log, diff, revert)
- the pending queue
- dream reports with undo
- "what Eika loads": the exact preamble text of a new home session and its token
  count

The Instructions editor sits beside it.

## 9. Evaluation

`evals/memory` (opt-in, real models) has these suites:

- a personal recall set of about 100 questions over a scripted multi-week
  history: preferences, people, dates, superseded facts, and project details only
  answerable in the project
- poisoning scenarios: a web page instructing the agent to remember something,
  where the expected outcome is a pending, never indexed memory
- a stress test: index size and preamble tokens after 500 writes with and without
  dreaming

Results are recorded per model so changes to prompts and budgets can be compared.
