# Compaction

The target for compaction in every session: what a summary holds, who writes each
part, what happens to the messages that are kept, and when the work is done.
`docs/PLAN.md` phase 13 turns it into items. `ARCHITECTURE.md` "Compaction"
describes what is built today, and `runtime.md` 11 the home session's summary
format, idle rule and history tools, which this design builds on.

Compaction today is Pi's: a model rewrites one structured summary at each
compaction, and about 20,000 recent tokens stay verbatim. Eika asks for the
summary on the run's own cached prefix, which stays.

Four things drive the design:

- **Code writes what rows hold, the model writes what needs judgement.** The
  user's words and the list of tool calls are in the store. Passing them through
  a model costs output tokens and can change them.
- **Generation is the cost the user feels.** The cached form already makes the
  summary request cheap to read. What remains is the summary's output, at the
  speed of a local model, and one re-prefill of the new context.
- **A compaction is the one moment the prefix is free to change.** Everything
  that edits old messages belongs there and nowhere else.
- **A compaction must not fail a run.** A record built by code is always
  available.

## 1. What is weak today

| Weakness | Effect |
|---|---|
| The whole summary is rewritten by the model at every compaction, under "preserve all existing information" | The user's requests are paraphrased, and each rewrite can drop a constraint. Nothing bounds the summary's size |
| The summary's output budget is 80% of the reserve, thinking included | Up to about 13,000 output tokens while the user waits. No compaction records how long it took |
| A cut-off summary is used as far as it goes; a failed summary after an overflow fails the run | A session can continue on half a summary, or stop |
| Kept messages are never edited | One large tool result can take the whole kept budget, and a turn larger than the budget needs a second summary call |
| Compaction runs only at the threshold, inside a run | The user waits for it. A session whose cache is already lost is re-read at full size |
| No tool reads compacted entries, and no eval compares methods | A lost fact is gone for the agent, and no change here can be measured |
| The prompts are a coding agent's | Sections such as Progress and Blocked fit a task, not an assistant's conversation |

## 2. The summary

One message, as today, in the same `<summary>` wrapper. It has two layers.

```
<summary as_of="2026-11-13 14:02" entries="#1-#412" compactions="3">
[Requests]
#3    11-12 09:14  Move the importer to the new schema. Keep the old CLI flags…
#9    11-12 09:31  Don't touch migrations 0001-0007.
… 11 messages, #14-#236
#251  11-13 10:02  The dry run is too slow, see if batching helps
#398  11-13 13:40  ok, ship it behind the flag
[Activity]
… 148 calls, #4-#310
#322  bash  make check                                  exit 1, ~3,200 tokens
#331  web_fetch ×3  pkg.go.dev/database/sql …           ~7,400 tokens
#390  bash  go test ./internal/importer -run Batch      ok, ~400 tokens
[State] as of #412
## Goal
…
history_search and history_read recover any entry above by its #id.
</summary>
```

### The record

`[Requests]` and `[Activity]` are written by code from the session's rows before
the cut. They are computed from the rows each time, never from the previous
summary, so nothing in them drifts.

- **`[Requests]`** holds user messages: the first ones (pinned, up to a quarter
  of the section's budget) and then the newest that fit. Each has its entry id
  and time, without its `<context>` block, clipped at 80 tokens with a mark. The
  gap between the two groups is one line with a count and an id range. Images
  are `[image]`. Notices and other harness messages are left out.
- **`[Activity]`** holds tool calls, one line each: entry id, tool name, the
  arguments as `callText` renders them clipped at 120 characters, then the
  outcome (error or not, the result's size). Consecutive calls of one tool with
  short arguments share a line. The newest that fit are listed and the rest are
  one counted line.
- A session type may add sections from its own rows. The home session adds
  `[Subagents]` (`runtime.md` 11).

Each section has a budget of 2% of the context window, at least 800 and at most
3,000 tokens. Both numbers are starting values for `evals/compaction`.

### The state

`[State]` is the model's part: what the rows do not say.

- For task sessions: Goal, Constraints, Decisions with their reasons, Findings
  (what was learned from tool results, with exact values, paths and messages),
  Work (done, in progress, blocked), Next.
- The home session uses the sections of `runtime.md` 11.
- Agent definitions may replace the prompts, as today.

The prompt shows the record that will stand above the state and says not to
repeat it. It asks for at most 1,200 words. The update prompt keeps what is
still true and needed, removes what is finished, and no longer asks to preserve
everything: the record and the history tools hold what is dropped.

The request is the cached form of `summary.go`, with the transcript form behind
it, as today. Its output budget is 4,096 tokens, thinking included.
`evals/compaction` decides which reasoning effort is tried first.

### When the model's part fails

A state is accepted when it is complete (not cut off), holds text and no tool
call, and begins with a heading. Otherwise the other effort is tried, then the
transcript form. If none gives an accepted state:

- the record is written as usual, which covers the whole span;
- the previous state stays as it was, under `[State] as of #<its last entry>`,
  or is absent on a first compaction;
- the compaction succeeds, with `method: record`.

The record's range and the state's `as of` then differ, which tells the agent
where to use the history tools. A cut-off state is never used.

## 3. The cut

- Budgets are shares of the context window. The kept tail is up to
  `keep_recent_share` (default 15%), and the context after a compaction is aimed
  at 30 to 35% of the window. The reserve stays a token count.
- The cut is at a user-turn boundary and keeps at least one whole turn.
- Sizes are taken after stubbing (section 4), so a turn full of old tool results
  usually fits. The split-turn summary stays for a turn that still does not.

## 4. Stubs in the kept tail

Tool results are most of a context, and most of an old result is never read
again. At a compaction the kept messages are rebuilt anyway, so shrinking them
costs no cache.

- Walking back from the newest message, tool results are kept whole until they
  have used half of the kept budget.
- Before that point, a result over 1,000 tokens becomes a stub: its first 20 and
  last 10 lines, its size, and how to get it back.

```
[bash result #381, ~4,200 tokens. First 20 and last 10 lines follow;
history_read #381 returns all of it.]
```

- A `web_fetch` result's stub names the call to repeat instead, since the page
  cache restores it (`web-fetch.md` 9).
- The compaction entry holds each stub's text, keyed by the entry it replaces.
  `session.Messages` applies them when it folds the path, so a replayed session
  sends the same bytes whatever the rules become later. A stub lasts until its
  message is compacted away.
- Calls, assistant text and user messages are never stubbed.

Results are never stubbed between compactions. Each replacement ends the cached
prefix at that message, and the oldest results sit near the start of the
context, so a stub pass re-reads nearly as much as a compaction and saves less.

## 5. When

The cost of a compaction has two parts that need not happen together: writing
the state needs the old prefix in the cache, and applying it costs a re-prefill
of the new context.

- **Prepare while warm.** When a run ends with the session over
  `compact_prepare_fill` (default 50%) of its window, a `maintenance` call
  writes the state for the cut as it stands, through the queue's idle gate. The
  affinity book (`inference/affinity.go`) says whether the session's prefix is
  still held; if it is not, nothing is prepared. The result is stored unapplied
  and the fold ignores it.
- **Apply when cold.** At the start of a run, a prepared compaction is applied
  if the session's prefix is no longer held. The call was going to read the
  whole context anyway, and now reads a third of it.
- **Apply at the threshold.** A call that passes window minus reserve applies a
  prepared compaction at once, with everything after its cut kept, when that
  fits. No summary is generated while the user waits.
- **Otherwise, as today.** With nothing prepared, the threshold and the overflow
  retry write the summary inside the run.

A warm session under the threshold is left alone: its next call costs nothing
to read.

A prepared compaction is dropped and written again when the messages after its
cut pass the kept budget. Where the harness cannot tell what the endpoint holds
(one that reports no slots, or a remote API), a prepared compaction is applied
after `compact_idle_minutes` without an important run, which is the idle rule
of `runtime.md` 11.

## 6. Recall

`history_search` and `history_read` (`runtime.md` 11) are offered to every
agent, scoped to its own session, with the home session also reaching its
subagents. They are in the tool list from the start, so a first compaction does
not change the prefix.

- Entry ids are shown as `#id` in the record, in stubs and in search hits.
- `history_read` returns any entry by id, tool results included, in full with
  `full`. Tool results are read by id and are still not searched.

## 7. What a compaction records

The `compaction` entry gains:

- `method`: `hybrid` or `record`;
- `state` and `state_as_of`, apart from the full `summary` that is sent;
- `stubs`, by entry id;
- `prepared_at` and `applied_at`;
- wall time of the summary calls, split into queue wait, prefill and generation;
- the tokens the first call after it read uncached.

The compaction card shows the method, the times and the count of stubs.

## 8. Evals

`evals/compaction` runs on `make live`, on the bench's chat model.

- **Sessions.** Recorded paths of three kinds: a coding task on `bash`, research
  with `web_fetch`, and an assistant conversation with preferences and
  commitments. Each is long enough to compact three times.
- **Probes.** Questions whose answers lie in the compacted span, each with the
  strings a right answer contains: an early constraint from the user, the
  reason for a decision, an exact value from a tool result, an artefact (a path
  or URL), and what comes next. Scoring is by those strings, with no judge
  model.
- **Runs.** The model answers each probe from the compacted context, once
  without the history tools and once with them. A drift run compacts five times
  and probes the first span.
- **Measures.** Probe score, context tokens after, summary output tokens, wall
  time, and uncached tokens on the next call.
- **Variants.** Today's method, the record alone, and the hybrid; then the
  hybrid with stubs, with each reasoning effort first, and with the record shown
  in the prompt or only described.

`make check` covers the code: golden tests of the record, the stub rule, the
fold with stubs, the acceptance check, and each path of section 5 on a fake
queue.

What ships:

- The hybrid replaces today's summary if its probe score is not lower and its
  wall time is.
- Stubs ship if the probe score with the history tools is not lower.
- The numbers of sections 2 to 5 are set from the report.

## 9. Not built

- **A state the model only appends to.** The model would write new decision and
  finding lines and code would merge them, so only Work and Next are rewritten.
  It returns if the drift run shows the rolling update losing what it held.
- **The record alone for a cold session.** A session over the prepare fill with
  nothing prepared could compact with `method: record` at no model cost. It
  returns if the eval shows the record alone scoring close to the hybrid with
  the history tools.
- **Chapters**, and the retry that folds in a span whose state failed
  (`DEFERRED.md`).
- **Tool-specific record lines.** The line is the call and its outcome for every
  tool.
