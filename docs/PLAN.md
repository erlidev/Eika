# Plan: Eika as a persistent local assistant

This plan turns Eika from a coding harness into the always-on personal assistant
described in `docs/design/`. Read `docs/design/README.md` first. It defines the
vocabulary every item below uses.

Agents work through this plan over many sessions. Take the first unchecked item
in the lowest open phase. Read the design section it cites, do it, tick it, and
log anything you learned at the bottom. A phase is closed when every item is
ticked and its exit check passes.

## Ground rules

`AGENTS.md` and `docs/STYLE_GUIDE.md` win on any conflict.

- **Behaviour changes are the point**, unlike `REFACTOR_PLAN.md`. The rules of
  `AGENTS.md` still apply to every one of them:
  - Tests with the change.
  - Docs current in the same change: `ARCHITECTURE.md`, `DECISIONS.md`,
    `EXTENDING.md`, `docs/api/`.
  - `make contract` when a wire type changes.
  - `make smoke` when compose, a Dockerfile, `deploy/` or `server.Run` wiring
    changes.
  - `make visual-update` only after looking at every changed baseline.
- **One item, one branch.** It may span several commits, and each one passes
  `make check`. Split an item here rather than landing a huge diff.
- **The design is the target, not scripture.** When the implementation finds a
  better way, change the design document in the same change and say why in the
  commit body. A design decision that lands becomes a `DECISIONS.md` entry.
- **Close a phase properly.**
  - Move what it built from `docs/design/` into `ARCHITECTURE.md` and
    `DECISIONS.md`, leaving the design docs only what is still to come.
  - Run `make check` and `make smoke`.
  - Do the exit check by hand, the way a user would, with `npm run shot` for UI.
  - Write a log entry.
- **Accounting phases run `make live`** (from phase 1) against at least llama.cpp
  and one of vLLM or TabbyAPI before they close, and the log records the result.
- **Dependencies.** Each needs the one-line justification `AGENTS.md` requires.
  The ones this plan expects:
  - `browser/`: Patchright (a Playwright fork), Readability, Turndown
  - `guard/`: Python, with a classifier runtime and `laya-serve`
  - Possibly a list virtualiser (phase 3, only if measurement says so)
  - Possibly a Web Push helper (phase 3, only if the RFC 8291 code would be
    unreasonable with `crypto/ecdh`)

  Nothing else is planned. Cron parsing, Aho-Corasick and X25519 are written with
  the standard library.
- **Frontend and backend items of one phase may proceed in parallel.** A UI item
  never ships ahead of the API it uses. The mock harness follows the contract.

## Phase map

```
0 Groundwork ─ 1 Inference & accounting ─ 2 Agents, home, projects, subagents ─ 3 Web shell
                                                                                   │
            ┌──────────────────────────────────────────────────────────────────────┘
            4 Sentry v1 ─ 5 Memory ─ 6 Skills ─ 7 Scheduler ─ 8 System One & scanning
                                                                   │
            ┌──────────────────────────────────────────────────────┘
            9 Dreaming ─ 10 Vault & surrogates ─ 11 Plugins via Sentry ─ 12 Browser
                                                                            │
            13 Monitor & auto-halt ─ 14 Channels ─ 15 Hardening & operations
```

Why this order:

- Accounting and the inference queue (phase 1) underlie every model call.
- Sentry (phase 4) lands before any capability that reaches the outside world on
  the user's behalf: email-like plugins, the browser.
- Memory (phase 5) comes early so it accumulates value while the rest is built.
- Scanning (phase 8) precedes plugins and the browser, the two biggest sources of
  untrusted content.
- The vault (phase 10) precedes both of its consumers.

Eika is usable from the end of phase 3.

## Phase 0: Groundwork

A smaller, clean base: the coding layer gone, the new vocabulary in place, and a
fresh schema. Eika still runs chats end to end, now with a computer.
Design: `design/README.md` "Kept, changed, removed".

- [x] Put a banner on `REFACTOR_PLAN.md` saying this plan supersedes it. Carry
      over its phase 3 import-rule test as the next item; drop its comment and
      doc trimming of code this plan rewrites.
- [ ] Add a Go import-boundary test in the style of `contract_test.go`:
      - nothing imports `server`
      - `tool` never imports `workspace` (later `computer`)
      - room for the Sentry rules of phase 4
- [ ] Remove the git layer:
      - `workspace/hub` and `/git/`
      - `Clone`, `CloneAt`, `Push`, `Fetch`, `Mirror`
      - project remote credentials
      - the merge, diff, commit and push routes
      - `session_entries.commit_sha` and `workspaceCommit`
      - fork-with-workspace
- [ ] Remove worktrees (`worktree.go`, `worktree_of`, `Locate`'s worktree case)
      and subagents (`subagent/`, `spawn_agent`, `wait_agents`, `list_agents`,
      `subagent.*` events). Note in the log which spawner pieces phase 2 reuses
      from history: `reserve`, `release`, abort and shutdown, `summaryOf`,
      `describe`.
- [ ] Remove previews, the terminal route and eikad `/pty`. Keep eikad `exec`,
      `files`, `stat`, `list`, `process`, `environment` and `watch`.
- [ ] Remove `contextfile` (AGENTS.md discovery) and `WorkspacePrompt`.
- [ ] Remove git projects and the `projects` table. Projects return with a new
      meaning in phase 2.
- [ ] Rename workspace to computer throughout:
      - package `computer`
      - `/api/computers`
      - event `computer.state`
      - containers `eika-cx-<id>`, label `eika.computer`
      - frontend types
- [ ] Create the `home` computer at setup (persistent volume at `/home/eika`).
      A chat may attach to it, which gives `bash`, so the sandboxed path stays
      exercised until phase 2 replaces chats.
- [ ] Squash migrations into `0001_baseline.sql` in schema `core`. A fresh
      database is expected (decided), so there is no data migration. Update
      `storetest`.
- [ ] Remove the coding UI:
      - features `files`, `terminal`, `changes`, `workspaces`, `projects`
      - the session tree panel and fork UI
      - diff libraries, Monaco and xterm
      - the projects, workspaces and sessions sidebar (chats remain)
      Keep the sandbox limit and egress fields as a Computer settings tab.
      Rebaseline.
- [ ] Rewrite `README.md` for the assistant, and update the layout tables in
      `ARCHITECTURE.md`, `EXTENDING.md`, `docs/api/` and `AGENTS.md`.
- [ ] Add `DECISIONS.md` entries:
      - scope (personal assistant)
      - coding layer removed
      - fresh baseline schema
      - computers instead of workspaces
- [ ] Update `make smoke`: setup, provider, then a chat on the home computer
      running a sandboxed `bash`.
- [ ] Re-measure lines and test counts against the `REFACTOR_PLAN.md` baseline and
      log them.

**Exit:** on a fresh stack, setup creates the home computer, and a chat runs
`bash` in it. `make check` and `make smoke` are green.

## Phase 1: Inference and accounting

Exact token numbers, model classes, and one queue for the GPU. Design:
`design/runtime.md` sections 1 to 5.

- [ ] Responses API provider: a second implementation of `provider.Provider`
      for the stateless Responses API (`store: false`, reasoning items passed
      back). The probe sets `api` per endpoint: `responses` for llama.cpp, and
      for others only when a conformance check passes (streamed tool-call round
      trip with a reasoning item, cache metrics); otherwise `chat`. The user can
      override it. Record fixtures per backend and note in the log which
      backends behave well.
- [ ] Provider probe: detect the backend, the per-slot window and capabilities for
      llama.cpp, vLLM, SGLang, TabbyAPI and generic OpenAI-compatible cloud
      endpoints. Record response fixtures for each. Add the provider columns
      `backend`, `pool`, `slots` and `capabilities`.
- [ ] Parse usage per backend: `prompt_tokens`, cached tokens, `prompt_n` and
      `cache_n`, reasoning tokens. NULL when not reported, never 0. Verify each
      field's meaning against a live server and record it in the log, including
      whether `prompt_tokens` counts cached tokens.
- [ ] Add a token counter with three methods:
      - exact (vLLM `/tokenize` with messages; llama.cpp `/apply-template` plus
        `/tokenize`)
      - anchored (text tokenisation plus learned per-role overhead)
      - estimated (a learned bytes-per-token ratio)
      Cache results per session by prefix hash.
- [ ] Rename `model_requests` to `model_calls`, with the fields of runtime.md 2:
      prediction and method, measured, cached, prefilled, completion, reasoning,
      window, `queue_ms`, attribution. Record `drift` per call.
- [ ] Replace the 4-byte estimate in `fit`, `shouldCompact` and
      `estimateContext` with the counter. Widen the compaction reserve by the
      observed error when the method is `estimated`.
- [ ] Break the context down by part: each part tokenised exactly where possible,
      plus the "template and formatting" remainder, with no scaling. Update the
      Context inspector and the status bar meter: measured fill, cached tokens,
      predicted next request with its method, effective window.
- [ ] Effective window is the smaller of the configured window and the probed
      per-slot context. Settings warns on a mismatch.
- [ ] Add model kinds (`chat`, `decision`) and the setting
      `model_classes` (`deep`, `main`, `fast`, `decision`) with its
      fallbacks. Retire `utility_models`; titles run on `fast`. Replace the
      utility-models UI with a classes UI.
- [ ] Add `internal/inference`:
      - pools, slots and priorities (interactive, foreground, background,
        maintenance)
      - aging, and idle detection for maintenance
      - one lease per model call
      - the `preempt_running` setting
      - `queue_ms`
      Every model call in Core goes through it.
- [ ] Slot affinity on llama.cpp endpoints with `slots > 1` (`id_slot`, an LRU map
      from session to slot).
- [ ] Loop hardening:
      - `Options.Clock` and the `<context>` block, with the time on every
        user-side message
      - `Options.Limits`: max calls, a repeated-call warning (from the 3rd
        identical call, never a stop), wall time (24 h by default)
      - a stall timeout scaled by predicted prefill
- [ ] Prefix-stability test: across scripted turns, a restart and a resume, each
      request's serialised prefix is a byte-prefix of the next, except across a
      compaction.
- [ ] Add a `make live` target and an `evals/` skeleton: a README and a runner
      over scripted scenarios against `EIKA_LIVE_ENDPOINT`, reporting drift,
      cache-hit rate and prefill seconds.

**Exit:** `make live` shows zero drift on `exact` against llama.cpp and vLLM, and a
stated bound on TabbyAPI. With two sessions on a one-slot pool, an interactive
message is answered before a waiting background call. The meter's numbers match the
server's logs.

## Phase 2: Agents, home session, projects, subagents

The main agent, the one home session with its compaction and history recall,
projects with their new meaning, and persistent subagents. Design:
`design/runtime.md` sections 4 to 11.

- [ ] Add `internal/agentdef`:
      - built-ins `eika`, `worker`, `researcher` (and `browser` and `dreamer`
        stubs registered later), with prompts embedded as Markdown
      - an `agents` override table, including each agent's class (the user sets
        it for `eika`, `browser` and `dreamer` too) and, for `worker` and
        `researcher`, the classes Eika may choose from
      - per-agent compaction prompts
      Profiles are removed: the feature, its API and UI, and the configuration
      layering they fed. The Settings page for agents reuses the profile editor's
      fields.
- [ ] Session columns: `kind` (home, subagent, routine, dream), `agent`, `name`,
      `project_id`, `computer_id`, `state`, `brief`, `result`, `sensitive`,
      `suspicious`, `last_active_at`. Entry kinds gain `preamble` and `notice`;
      home entries record their `channel`. Unused columns and kinds are removed.
- [ ] Preamble builder: global and project instructions, the active-projects list,
      the project or subagent brief, the delegable agents with their allowed
      classes, and the user's time zone and locale. Store it as an entry, and rebuild it only at compaction. Memory,
      skills and plugins add their sections in their own phases.
- [ ] Instructions: global "About me" and rules, plus project instructions.
      Stored as rows and edited in the UI. No agent tool writes them.
- [ ] Create the one home session at setup, found by `(owner, name)`, and add
      `GET /api/home`. Retire chats: there are no direct sessions, and the user
      talks only to Eika. Each user-side `<context>` names its channel; replies go
      back to it, and proactive messages go to `notify_channel`.
- [ ] Add projects: table, API and the tools `project_list`, `project_open`,
      `project_create` and `project_update`. The default directory is
      `/home/eika/projects/<slug>` on `home`.
- [ ] Add the file tools `file_read`, `file_write` and `file_edit` over the
      executor, with limits like bash's. Record in `DECISIONS.md` why they are
      back.
- [ ] Add `internal/subagent` (rewritten; runtime.md 9):
      - named, persistent subagent sessions, resumable by name
      - the tools `subagent_start` (with `class`, checked against the agent's
        allowed classes), `subagent_message` (steer, answer a question, or resume
        an idle one in its own context), `subagent_status` (the board, or one
        subagent) and `subagent_cancel`
      - the `report` tool. A run that ends without one is sent a message to
        draft it; if it still does not call `report`, its final message becomes
        the report
      - state set by code: running, waiting (with the reason), idle, failed,
        cancelled
      - the limits `subagent_max_depth` and `subagent_max_running`, and
        `subagent_board_days`
      - abort and shutdown; aborting a home reply leaves its subagents running
      Reuse the spawner pieces noted in phase 0. `eika` has no `ask_user`, but has
      `inbox_ask`. The prompts for `eika` and the brief template say to decide for
      itself on small details and state the assumption, and to ask when it is
      unclear what the user wants in a way that changes the result. The prompt also
      cautions against irreversible actions, as model guidance only.
- [ ] Notices: a report becomes a notice in Eika's next `<context>`, naming the
      subagent. `report: always` starts a background turn on the home session.
- [ ] Durable questions: a `questions` table. `ask_user` from a subagent parks
      it and goes to Eika as a notice plus a background home turn; Eika answers
      with `subagent_message` or asks the user with `inbox_ask`, which goes to
      topic `inbox`.
- [ ] Durable runs: subagent and routine runs resume after a restart with a
      notice. An interrupted home reply ends with a notice instead.
- [ ] Paginated entries: `GET /api/sessions/{id}/entries?before=&limit=`.
- [ ] Home compaction (runtime.md 11):
      - deterministic `[Subagents]`, `[Ledger]` and `[Recent turns]` sections,
        with `(#id)` references
      - `[Conversation]` by rolling update, with an assistant-tuned prompt
      - the cut at user-turn boundaries, keeping `keep_recent_share` of the window
      - idle compaction at maintenance priority (over `compact_idle_fill`, and
        no important run for `compact_idle_minutes`, default 30), with threshold
        and overflow kept as safety nets
      - a failed summary still compacts, and a maintenance retry folds the span in
      - the tool list and preamble rebuilt at compaction
      Subagent, routine and dream sessions keep Pi's compaction with per-agent
      prompts.
- [ ] History recall (runtime.md 11): `entries.tsv` and `pg_trgm` indexes over
      entries, tool calls, notices and compaction summaries (tool results apart);
      the tools `history_search` and `history_read`; the `sensitive` label
      carried by hits from private sources.
- [ ] `evals/compaction`: a scripted multi-week home history, questions after N
      compactions, with and without the history tools.
- [ ] Topics `inbox` and `activity`, and the events `subagent.started`,
      `subagent.updated`, `subagent.reported`, and `question.asked` on `inbox`.
- [ ] Transitional UI so the phase can be used before phase 3: the sidebar lists
      the home session, then subagents by state, each opening a read-only view;
      questions show in the home transcript.

**Exit:** in the home session, "look into X and keep notes in a project for it"
should:
- create the project
- start a named subagent that runs while the home chat stays responsive
- bring its report back as a notice
- resume the same subagent a day later from "follow up on X"

Restarting Core mid-run resumes the subagent. After a forced compaction, Eika
answers a question about something only the compacted span held, using
`history_search`. The prefix-stability test covers the home session across a
compaction, and subagent sessions.

## Phase 3: Web shell

The mobile-first app. Design: `design/interface.md`.

- [ ] Add a touch density under `(pointer: coarse)` (type step, 44 px targets, no
      hover-only controls). Add a style guide section and ESLint rules where they
      apply.
- [ ] App shell: a rail on desktop and a bottom bar on a phone, a route per page,
      `100dvh`, safe areas, and the composer anchored to the visual viewport.
      Leave a slot for the halt button (wired in phase 4).
- [ ] Chat page on the home session:
      - a paginated transcript, with virtualisation decided by measuring a
        5,000-entry session on a phone
      - every channel's messages, marked with their channel
      - Eika's reasoning and tool calls, compaction cards, subagent cards and
        notices
      - on touch, Enter makes a newline and a send button sends
      - the markdown field verified on iOS Safari and Android Chrome, with a
        fallback if it misaligns
- [ ] Activity page: subagents and their states, a read-only live view of each
      (the session view reused, no composer) with cancel, and the pool and queue
      strip. Steering goes through Eika.
- [ ] Inbox page: questions and elicitations as cards (the existing forms
      generalised), a badge, live updates on `inbox`.
- [ ] Projects pages: list, detail, instructions editor, and the project's
      subagents (watch only).
- [ ] Reorganise Settings:
      - providers and models
      - model classes, agents, instructions
      - computer (limits and egress)
      - notifications, appearance, account
      - developer: the inspector and model calls with drift
- [ ] Notifications: an in-app list and toasts.
- [ ] Web Push: VAPID keys sealed in Core, a subscription per device, a minimal
      payload, and delivery for questions and subagent reports. Record the dependency
      decision in the log.
- [ ] PWA: manifest, icons, and a service worker that caches the shell only.
- [ ] Setup gains a "reach Eika from your phone" step. Document Tailscale serve
      in the README.
- [ ] ESLint rule: a feature imports another feature only through its
      `index.ts` (from `REFACTOR_PLAN.md` phase 3).
- [ ] Mock harness: a `World` for the home session and subagents, the Inbox and projects;
      scenarios; phone baselines for every page; a touch emulation pass of the
      composer spec.
- [ ] Remove the transitional UI and what is left of the workbench.

**Exit:** on a phone over Tailscale:
1. Install the PWA.
2. Chat with Eika.
3. Watch a subagent in Activity.
4. Answer one of Eika's questions from a push notification.

## Phase 4: Sentry v1

The permission authority, deterministic. Design: `design/sentry.md` sections 1 to
7, 10, 11 (halt only) and 12 to 15.

- [ ] Add `cmd/eika-sentry` and the `eika-sentry` container. Compose changes:
      - networks `eika_egress` and `eika_browser_ctl` (the latter reserved), and
        the socket volume
      - Postgres roles `eika_core` and `eika_sentry` with schemas `core` and
        `sentry`, set up by an init script
      - Sentry's own migrator
      - `.env.example` documents the new passwords
- [ ] Add `internal/sentry/api` wire types and `docs/api/sentry.md`, generated and
      checked like `contract.json`. Extend the boundary test with the Sentry rules.
- [ ] Core gate: `Options.Gate` in the loop, with `Decide` before every tool call.
      Each built-in tool declares its `effect` and how to read `target`.
      The model is never told about the gate: a block is a `Blocked: <reason>`
      error result, an ask is a slow tool, and no prompt or tool description
      mentions Sentry. Fail closed: side-effecting tools return the generic
      blocked result and the run parks.
- [ ] Policy:
      - hard rules
      - the defaults table by effect and label
      - tool settings and the blocklist
      Every table row is a unit test.
- [ ] Taint: the `sensitive` label set by private sources, by reports from
      `sensitive` subagents and by history hits from private sources; the
      `suspicious` flag (set from phase 8); both cleared by compaction as
      specified and mirrored into `sessions`. Untrusted content sets no label.
      Labels route to the `review` verdict, never straight to the user; until
      the reviewer lands in phase 8, a labelled `review` falls back to `ask`.
- [ ] Approvals:
      - durable, keyed by action, with wait and expiry
      - grouping of related approvals
      - user-token verification through `SELECT` on `core.auth_sessions`
      - Sentry's event stream relayed by Core on `inbox`
      Resuming a run gets the same approval back.
- [ ] Grants: the scopes, an in-memory cache with invalidation, and a revoke UI.
- [ ] Inbox approval cards (action, destination, recipients, content and diff,
      reason, labels, the answers) and a grouped push digest (default delay
      5 minutes), immediate only for hand-offs, secret requests and alerts.
- [ ] Move `internal/egress` and `internal/netguard` into Sentry:
      - principals API
      - per-run proxy credentials in each `exec`'s environment
      - the `open` mode removed
      - `allow` or `deny` per connection (blocklist, netguard, hard rules,
        halt, per-computer allowlist mode); never `ask` or `review`
      `web_fetch` and the hosted search backends go through the proxy as the
      calling run.
- [ ] Ingest v1, deterministic: pattern-based secret redaction and
      `<untrusted>` wrapping. Add the system-prompt line about untrusted content.
- [ ] Outbound "never share" tier: credentials, Luhn-valid card numbers, account
      numbers, ID formats.
- [ ] Halt: an always-visible button, resume, and Core's reaction (abort runs, a
      scheduler pause hook, a banner).
- [ ] Audit log and its UI timeline. Retention setting. Asks per active day in
      Activity, split by source.
- [ ] Settings pages for grants, tool settings and the blocklist.
- [ ] Benchmarks: `decide` p99 under 2 ms, proxy overhead under 1 ms.
- [ ] `make smoke` covers Sentry: a scripted model triggers an `ask`, and the smoke
      test answers it through the user API.
- [ ] Update the security section of the style guide: Core holds model keys; Sentry
      holds third-party credentials; computers get proxy identities and nothing
      else.

**Exit:** a run tries to send an email. It parks, and an approval arrives on the
phone. Approving lets it continue; denying gives the agent a `Blocked: <reason>`
result it can act on, with nothing in its context naming the gate. A run in a
`sensitive` session (it read files of a private project) that runs a `curl`
command falls back to an approval until phase 8, then goes to the reviewer. Halt
stops everything in one press. The benchmarks pass.

## Phase 5: Memory

Design: `design/memory.md` sections 1 to 6, 8 and 9.

- [ ] Add `internal/memory`:
      - file format, parser and validator
      - the git repository at `/var/lib/eika/memory`
      - one writer
      - the `core.memories` index (FTS)
      - `Reconcile` at start-up and `make memory-reindex`
- [ ] Add the tools `memory_search`, `memory_read`, `memory_write` and
      `memory_forget`. Writes are Sentry `memory_write` actions with the
      provenance and state rules of memory.md 6, plus the secret and
      instruction-shaped scans.
- [ ] Preamble: pinned bodies plus the index within exact budgets, in
      deterministic order, with a visible overflow count. An over-budget index
      flags a dream (consumed in phase 9).
- [ ] Notices at the tail for memory changes since the preamble.
- [ ] Recall hint: up to 3 FTS matches, names and descriptions only, in the
      `<context>` of each home user message.
- [ ] Write policy in the `eika` prompt, including saving a memory at once
      instead of later. Subagents are limited to the project
      scope and its types.
- [ ] Memory UI: list, filters, search, editor, pin, archive, purge, history with
      diff and revert, the "what Eika loads" view, and the pending queue in the
      Inbox.
- [ ] Forget by source, for projects. Plugins follow in phase 11.
- [ ] `make backup` and `make restore`: both schemas, the memory repository, both
      key files, with a documented and tested restore. Phase 15 extends it.
- [ ] `evals/memory`: the recall and poisoning suites, recorded per model.

**Exit:**
- Facts told in one home session appear in the next session's preamble.
- A web page that tells the agent to remember something produces only a pending
  memory.
- An edit in the UI shows up in the next session.

## Phase 6: Skills

Design: `design/skills-and-plugins.md` "Skills".

- [ ] Add `internal/skill`:
      - lenient parsing and validation of the specification
      - the store and the `core.skills` rows
      - scopes, and project overriding global
      - install from a folder, a zip, a pasted `SKILL.md`, a git URL (through
        Sentry's proxy, pinned to a commit, updates shown as a diff) or a path on
        `home`
- [ ] Mount `/skills` read-only into computers.
- [ ] Catalog in the preamble, sorted, names only above 40 skills. Add the tools
      `skill_use` (by name or query; repeat activations deduplicated) and
      `skill_file`. Notices for skills installed mid-session. Loaded skill bodies
      re-attached after compaction.
- [ ] `/name` in the composer loads a skill into the next turn.
- [ ] Skills UI: install with the full `SKILL.md` and file list shown, enable,
      scope, assign to agents, warnings. The injection screen is added here in
      phase 8.
- [ ] `EXTENDING.md`: writing a skill for Eika (the spec fields only).

**Exit:** drop a skill zip into the UI and enable it, then ask Eika for something
that needs it. Eika delegates, the task loads the skill with `skill_use`, and it
runs the skill's script on the computer.

## Phase 7: Scheduler

Design: `design/scheduler.md`.

- [ ] Add `internal/schedule`:
      - the tables `routines`, `routine_runs` and `wakeups`
      - a 5-field cron parser with time zones (standard library)
      - a 15 s ticker with `SKIP LOCKED`
      - the `missed` policy, no overlap, pre-flight and the `blocked` state
- [ ] Contexts `fresh`, `continue` and `nudge`; `precheck` commands; `NO_REPLY`;
      the `report` modes.
- [ ] Creating or enabling a routine is a Sentry `schedule` action, and its grant
      has scope `routine`. Recursion guard. Prompt scan at creation (the
      deterministic part now, the classifier in phase 8).
- [ ] `wake_me`, with a per-agent cap.
- [ ] Triggers:
      - webhooks with a path secret
      - file changes (eikad `/watch`)
      - interval watches
      Plugin notifications follow in phase 11.
- [ ] Urgency (`needs_attention_now`) comes from `fast` for now.
- [ ] Shipped routines: Morning brief (disabled), Check-in (disabled, read-only
      grant).
- [ ] Schedules UI and Activity integration (runs, wakeups).

**Exit:** "every weekday at 8, send me the weather and my open tasks" should:
- ask for approval
- run the next morning
- deliver a push

Restarting across a due time follows the `missed` policy.

## Phase 8: System One models and scanning

Design: `design/sentry.md` sections 6 and 9.

- [ ] Add `internal/systemone`: the Jev-protocol client, wire types, thresholds
      and probing. Model kind `decision`.
- [ ] Emulated backend: a chat model with constrained JSON (llama.cpp grammar,
      vLLM guided JSON), with probabilities from logprobs where available.
- [ ] `guard/` sidecar (Python, compose profile `guard`): the Prompt Guard 2
      classifier endpoint (22M or 86M, a setting) and `laya-serve`. Show its
      health in Settings.
- [ ] Ingest:
      - the injection screen (chunking, bounded sampling of large content)
      - the `suspicious` flag
      - alerts
      - one-time code and sign-in link removal for mail, messaging and browser
        sources
- [ ] Reviewer: isolated state capped at 16k tokens (a setting), its questions
      and user-adjustable thresholds, a 5 s cut-off that becomes `ask` (`deny`
      for egress). Turn on the `review` verdict: external sends and deletes in
      every session, and navigation and fetches, browser submits, `eval` and
      `bash` commands in labelled ones.
- [ ] Use the screen when a skill is installed and when a routine is created.
- [ ] Notification urgency `needs_attention_now` on `decision`.
- [ ] Settings warns when `decision` shares a pool with `main` without a spare
      slot.
- [ ] Hosted Jev may not see `sensitive` state unless the user opts in.
- [ ] `evals/sentry`: the innocent, injected and over-reach suites. Record attack
      success, utility and approval rate per model, and the asks per active day
      by source.

**Exit:**
- Injected content on a fixture page is flagged and wrapped.
- On the eval set, the reviewer allows benign actions and denies or asks on
  dangerous ones.
- The numbers are in the log.

## Phase 9: Dreaming

Design: `design/memory.md` section 7.

- [ ] `dreamer` agent:
      - the tools `memory_list`, `memory_merge` and `memory_archive`
      - the phased prompt
      - a report file
      - one commit per dream
      - a home notice
- [ ] Dream routine, shipped enabled. Triggers: sessions since the last dream, an
      over-budget index, idle pool.
- [ ] Guardrails:
      - no egress principal for dream sessions
      - changes to user-provenance memories become Inbox proposals
      - the `dream_mode` setting
      - pending memories are never promoted
- [ ] "Undo last dream" (a revert) in the UI.
- [ ] Pre-classify merge candidates and contradictions with `decision`.
- [ ] Prune by `use_count`, `last_used_at` and `expires`.
- [ ] `evals/memory` stress suite.

**Exit:** after a scripted week of history, a dream:
- merges duplicates
- turns relative dates into absolute ones
- archives expired memories
- proposes a change to a user memory instead of making it
- writes its report

Undo restores everything.

## Phase 10: Vault and surrogates

Design: `design/sentry.md` section 8.

- [ ] Vault entries sealed with Sentry's key. The Vault UI shows names, kinds,
      bound domains and last use, never values. Rotate and delete.
- [ ] Secure entry:
      - the `request_secret` tool and an Inbox form
      - X25519 plus AES-GCM in the browser with WebCrypto
      - Core relays ciphertext only
      A test asserts the value never reaches a Core log, a `model_calls` row or
      an entry.
- [ ] Surrogates: the format, binding by domain and principal, and the resolution
      API for the plugin host and the browser.
- [ ] Redact real vault values on ingest (multi-pattern search).
- [ ] Canaries planted at setup: an OpenAI-shaped API key in the `home`
      environment and a vault-looking file. A minimal tripwire halts on any canary in an
      action's arguments or in egress; phase 13 builds the full monitor.

**Exit:**
- An agent asks for an API token, and the user enters it on the phone.
- The surrogate appears in the agent's context.
- No log, row or response anywhere in Core contains the real value.
- A canary in a `curl` halts Eika.

## Phase 11: Plugins through Sentry

Design: `design/skills-and-plugins.md` "Plugins".

- [ ] Sentry plugin host: move `mcp.Pool` use from Core into Sentry. OAuth runs
      through `/v1/user/*`, with tokens in the vault. Elicitations become Inbox
      items. Core's MCP routes move or proxy to Sentry's user routes.
- [ ] Plugin cells for stdio servers: Core creates and registers the container,
      and Sentry drives `/process`. Each cell has its own egress principal.
- [ ] Catalog in the preamble; `plugin_find` and `plugin_call`; schema validation
      in Sentry; pinned plugin tools in agent definitions.
- [ ] Effects from MCP annotations. The Plugins page shows each tool's verdict and
      the private and trusted flags.
- [ ] Results go through ingest.
- [ ] Plugin notifications and resource changes become scheduler triggers.
- [ ] Forget the memories learned from a plugin when it is disconnected.

**Exit:**
- A calendar MCP is connected with OAuth.
- Eika lists events without asking, and asks before creating one.
- A stdio plugin's environment is unreadable from any computer.

## Phase 12: Browser

Design: `design/browser.md`.

- [ ] `browser/` service on Patchright driving headed Chrome on Xvfb:
      - the command set, including `eval`
      - pruned interactive snapshots with refs, budgets, deltas and masking
      - `find` and `read`
      - tabs
      - the download quarantine
      Unit tests run against fixture pages.
- [ ] Container (pinned Chrome stable on x86-64 Linux, Xvfb),
      networks, profile volume, hardening and compose.
- [ ] A detection check in `evals/browser`: a fixed list of bot-detection test
      pages and captcha-protected fixture sites, run headed against vanilla
      headless Chromium, so a Patchright or Chrome update that regresses shows
      up.
- [ ] Sentry relay:
      - `browser` actions
      - form-submit decisions
      - hand-off on account creation, password changes, payments and terms
      - surrogate typing bound to origins
      - the per-site list
- [ ] `browser` agent definition, its tools, and the one-task browser lock.
- [ ] Images in tool results: a screenshot reaches a vision model as a user
      message after the tool result. Add the `DECISIONS.md` entry, which
      replaces the deferred note.
- [ ] Live view: a screencast relayed through Core with Sentry tickets; the
      Browser page; a phone layout.
- [ ] Takeover: the lock, input relay, the return-control notice, and hand-off
      cards with the live view built in.
- [ ] Moving a download to a computer, as its own action.
- [ ] The `browser.identify` setting.
- [ ] A Docker test with a fixture site, and `evals/browser`.

**Exit:** ask for "three flights to X under $Y". Then:
- A browser task starts.
- The live view works on the phone.
- It logs in through a vault surrogate.
- A sign-up form is handed off.
- Nothing is submitted without approval.
- A simple captcha is attempted by the agent.
- An injected review on a fixture page fails to redirect the task.

## Phase 13: Monitor and auto-halt

Design: `design/sentry.md` sections 9 and 11.

- [ ] Core sends turns, reasoning excerpts and tool calls to `/v1/observe`.
- [ ] The full list of deterministic tripwires.
- [ ] One trajectory check (`malicious_or_dangerous`) with `decision`, run only when a
      tripwire fires or ingest flags an injection. It reads tool results. The
      reasoning review setting (off by default).
- [ ] Severity routing and push. Auto-halt on high severity. Resuming can revoke
      the grants of the halted session.
- [ ] Approval-fatigue suggestions in the Inbox.
- [ ] Decision dataset export (JSONL, Laya format), a fine-tune script outside
      `make check`, and a guide to swapping the result in as the local `decision`
      model.

**Exit:** each scripted misbehaviour scenario raises its expected severity, and a
canary exfiltration halts within one action.

## Phase 14: Channels

Design: `design/interface.md` section 6.

- [ ] The `Channel` interface, now that a second implementation justifies it. Web
      moves behind it.
- [ ] A Discord or Matrix adapter:
      - an allowlist
      - a home session per channel
      - untrusted attachments
      - approvals and secrets delivered as links to the web Inbox
- [ ] ntfy notifications.
- [ ] Settings page for channels.

**Exit:** a message to Eika on Discord gets a reply there, and an approval arrives
as a link to the web Inbox.

## Phase 15: Hardening and operations

- [ ] TLS interception in Sentry's proxy, only for hosts with bound secrets:
      - a Sentry CA in the sandbox image
      - surrogates swapped in headers and bodies
      - surrogates sent to unbound hosts denied with a high alert
- [ ] A Docker socket proxy in front of Core, allowing only container operations on
      Eika-labelled objects.
- [ ] Computer hardening: read-only root filesystem where the image allows it, a
      seccomp profile, user-namespace remap documented.
- [ ] Extend backup and restore to skills, the browser profile (optional) and
      Sentry's vault, with a tested restore on a clean host.
- [ ] Soak test: 72 h always on, with scripted routines and a scripted user. Watch
      memory growth, leaks, queue fairness and prefix-cache hit rate over time.
- [ ] Track eval results over time per model in `evals/results/`.
- [ ] Threat model review against `design/README.md`, and a self pen-test
      checklist run.
- [ ] Final docs: `docs/design/` is empty or holds only deferred items. Delete
      this plan and `REFACTOR_PLAN.md`.

## Open questions and deferred items

Decide these when the phase that needs them starts, and record the outcome in the
log.

- **Class names.** `deep`, `main`, `fast` (proposed). Change them before phase 1
  ships, if at all.
- **KV slot save and restore, LMCache.** Deferred until phase 1's accounting
  shows how much prefill is lost to switching. Measure first.
- **Main agent with `bash`.** Not by default (runtime.md 6). Revisit if delegating
  small commands proves too slow in practice.
- **Check-in routine on by default?** Off at first, until the cost and noise of a
  real run are measured.
- **Hosted Jev.** It is opt-in, and `sensitive` state needs a second opt-in.
- **Payments**, one-time cards for example: out of scope. Payment is always a
  hand-off.
- **Voice and an email address for Eika:** after phase 14.
- **`allowed-tools` as grants, and skill proposals:** see skills-and-plugins.md,
  deferred.
- **Per-value capability tracking (CaMeL):** only if session-level taint proves
  too coarse in the phase 8 and 12 evals.
- **An untrusted-content label.** Dropped (sentry.md 6). Revisit only if the phase
  8 and 12 evals show injected sessions without sensitive data doing real harm
  within the defaults.
- **Reviewer load from labels.** Measure how many label-driven reviews reach the
  user in phase 8, and tune the stricter thresholds.
- **Chapters for home compaction** (frozen per-span summaries with a list of
  titles, runtime.md 11). Deferred until `evals/compaction` shows drift from the
  rolling update.
- **More than one home session** (another person, another persona). The schema
  allows it; nothing else is built.
- **Several browser identities and profiles:** after phase 12, if needed.

## Log

Newest first. One entry per session: date, items done (commit hashes), the
`make check` and `make live` results, anything skipped and why, and findings for
later items.

- 2026-10-29: design revised before any item started. Decisions:
  - Simplifications: no embedding model or hybrid memory search, no background
    compute budget or quiet hours, no start date in the preamble, no
    pre-compaction memory flush (Eika saves memories at once), a warning from
    the 3rd identical tool call and never a stop, 24 h default wall time, and
    idle compaction after 30 minutes with no important run.
  - Tool-result images keep the user-message workaround. The Responses API is
    the preferred protocol, by default for llama.cpp and any endpoint that
    passes the probe.
  - The user sets the class of `eika`, `browser` and `dreamer` (dreamer now
    defaults to `main`), and the classes Eika may pick for `worker` and
    `researcher`.
  - A subagent ending without `report` is asked to draft it, then its final
    message is used. A subagent's questions go to Eika, who asks the user with
    `inbox_ask` when needed.
  - One home session for the user across every channel, with no direct
    sessions. Subagent, routine and dream sessions are watched read-only, and
    steering goes through Eika.
  - Lines of work are named, persistent subagents (`subagent_*` tools replace
    `task_*`), resumable by name, rather than a separate thread concept. `eika`
    loses `ask_user`.
  - Home compaction: Pi's rolling update plus deterministic sections in the
    style of Pi-VCC, cuts at user turns, idle compaction, and `history_search`
    and `history_read` over every entry. These move into phase 2, since the
    home session starts accumulating history there. Chapters are deferred.
  - Sentry is invisible to the model: no prompt or notice names it, the loop
    stays standard, and a block is a generic `Blocked: <reason>` result.
  - Taint is back: `sensitive` plus the evidence-based `suspicious` flag, with
    labels routing to the decision model (the `review` verdict) rather than the
    user, to avoid approval fatigue. Memory
    provenance comes from what a write contains and who makes it (memory.md 6).
  - No outbound health/finance category check; only the deterministic "never
    share" tier. The agent supplies no rationale field. Agents cannot change
    their own permissions by construction, not by a rule.
  - The reviewer runs only in labelled sessions (browser submits, `eval` and
    `bash` included), capped at 16k tokens, thresholds user-adjustable. The
    monitor runs one `malicious_or_dangerous` check, only when something is
    flagged, and may read tool results.
  - One canary API key shaped like an OpenAI key, and the vault-looking file. No
    private-memory canary.
  - Egress never asks the user and is not reviewed: `allow` or `deny` from the
    blocklist, netguard, hard rules, halt and per-computer allowlist mode. A
    labelled session is guarded at the actions that can move data out (navigation,
    fetches, submits, `eval`, `bash`) instead of per connection.
  - The browser is real headed Chrome on Xvfb driven by Patchright, with `eval`
    for JavaScript. Per-request reporting from the browser is dropped. The agent
    attempts captchas itself. Chrome only, with no second engine.
  - Action schema trimmed: `kind` is tool, plugin, egress or browser; `intent`,
    `rationale`, `project` and the `data` labels are gone, replaced by a
    `private?` flag on reads. The reviewer judges the action alone (`dangerous`,
    `risk`, `reversible`), with no intent chain.
  - Eika should ask far less. External sends and deletes go to the reviewer first
    and ask the user only when it is unsure, approvals arrive as a grouped push
    digest, the suggestion for a standing grant comes after 5 approvals, the
    prompts tell the agents to decide small details themselves and ask when user
    intent is unclear, and asks per active day are
    measured. Known gaps are documented as current caveats.
  - User rules removed. Policy is hard rules, defaults by effect and label, tool
    settings, the blocklist and grants. A grant answers an `ask` and never skips
    the reviewer. `suspicious` is a superset of `sensitive`.
- 2026-10-29: plan and `docs/design/` written from the design discussion. No items
  started except the `REFACTOR_PLAN.md` banner. Decisions taken in that
  discussion:
  - remove the coding UI and keep the sandboxes as computers
  - start from a fresh database
  - give Sentry its own container
  - support llama.cpp, TabbyAPI and vLLM or SGLang, with exact token accounting
    before backend-specific caching
