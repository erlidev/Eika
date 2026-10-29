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
  - `browser/`: Playwright, Readability, Turndown
  - `guard/`: Python, with a classifier runtime and `laya-serve`
  - `pgvector` image (phase 9, optional)
  - Possibly a list virtualiser (phase 3, only if measurement says so)
  - Possibly a Web Push helper (phase 3, only if the RFC 8291 code would be
    unreasonable with `crypto/ecdh`)

  Nothing else is planned. Cron parsing, Aho-Corasick and X25519 are written with
  the standard library.
- **Frontend and backend items of one phase may proceed in parallel.** A UI item
  never ships ahead of the API it uses. The mock harness follows the contract.

## Phase map

```
0 Groundwork ─ 1 Inference & accounting ─ 2 Agents, sessions, projects, tasks ─ 3 Web shell
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
- [ ] Add model kinds (`chat`, `decision`, `embedding`) and the setting
      `model_classes` (`deep`, `main`, `fast`, `decision`, `embedding`) with its
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
      - `Options.Limits`: max calls, a repeated-call detector, wall time
      - a stall timeout scaled by predicted prefill
- [ ] Prefix-stability test: across scripted turns, a restart and a resume, each
      request's serialised prefix is a byte-prefix of the next, except across a
      compaction.
- [ ] Account background compute (prefill plus decode seconds per day, by
      priority). Add the settings `background_budget` and `quiet_hours`; the
      scheduler enforces them in phase 7.
- [ ] Add a `make live` target and an `evals/` skeleton: a README and a runner
      over scripted scenarios against `EIKA_LIVE_ENDPOINT`, reporting drift,
      cache-hit rate and prefill seconds.

**Exit:** `make live` shows zero drift on `exact` against llama.cpp and vLLM, and a
stated bound on TabbyAPI. With two sessions on a one-slot pool, an interactive
message is answered before a waiting background call. The meter's numbers match the
server's logs.

## Phase 2: Agents, sessions, projects, tasks

The main agent, the home session, projects with their new meaning, and delegation.
Design: `design/runtime.md` sections 4 to 10.

- [ ] Add `internal/agentdef`:
      - built-ins `eika`, `worker`, `researcher` (and `browser` and `dreamer`
        stubs registered later), with prompts embedded as Markdown
      - an `agents` override table
      - per-agent compaction prompts
      Profiles are removed: the feature, its API and UI, and the configuration
      layering they fed. The Settings page for agents reuses the profile editor's
      fields.
- [ ] Session columns: `kind` (home, direct, task, routine, dream), `agent`,
      `project_id`, `computer_id`, `channel`, `state`, `brief`, `result`, `taint`.
      Entry kinds gain `preamble` and `notice`. Unused columns and kinds are
      removed.
- [ ] Preamble builder: global and project instructions, the active-projects list,
      the project or task brief, and the session's start date, time zone and
      locale. Store it as an entry, and rebuild it only at compaction. Memory,
      skills and plugins add their sections in their own phases.
- [ ] Instructions: global "About me" and rules, plus project instructions.
      Stored as rows and edited in the UI. No agent tool writes them.
- [ ] Create the web channel's home session at setup, and add
      `GET /api/home`. Retire chats as a separate concept: a direct session with
      no project replaces them.
- [ ] Add projects: table, API and the tools `project_list`, `project_open`,
      `project_create` and `project_update`. The default directory is
      `/home/eika/projects/<slug>` on `home`.
- [ ] Add the file tools `file_read`, `file_write` and `file_edit` over the
      executor, with limits like bash's. Record in `DECISIONS.md` why they are
      back.
- [ ] Add `internal/task`:
      - the tools `task_start`, `task_status`, `task_message` (steer, or resume a
        finished task) and `task_cancel`
      - the `report` tool and a report written when a task ends without one
      - the limits `task_max_depth` and `task_max_active`
      - abort and shutdown
      Reuse the spawner pieces noted in phase 0.
- [ ] Notices: a task result becomes a notice in the delegator's next `<context>`.
      `report: always` starts a background turn on the home session.
- [ ] Durable questions: a `questions` table. `ask_user` from a task goes to topic
      `inbox`, and the home session gets a "waiting on you" notice.
- [ ] Durable runs: task and routine runs resume after a restart with a notice.
      Interrupted home and direct replies end with a notice instead.
- [ ] Paginated entries: `GET /api/sessions/{id}/entries?before=&limit=`.
- [ ] Idle compaction at maintenance priority.
- [ ] Topics `inbox` and `activity`, and the events `task.started`,
      `task.updated`, `task.finished`, and `question.asked` on `inbox`.
- [ ] Transitional UI so the phase can be used before phase 3: the sidebar lists
      the home session, then tasks by state; questions show in their session.

**Exit:** in the home session, "look into X and keep notes in a project for it"
should:
- create the project
- start a task that runs while the home chat stays responsive
- bring the result back as a notice

Restarting Core mid-task resumes the task. The prefix-stability test covers home
and task sessions.

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
      - delegation cards and notices
      - on touch, Enter makes a newline and a send button sends
      - the markdown field verified on iOS Safari and Android Chrome, with a
        fallback if it misaligns
- [ ] Activity page: tasks and their states, a task view (the session view reused)
      with steer and cancel, and the pool and queue strip.
- [ ] Inbox page: questions and elicitations as cards (the existing forms
      generalised), a badge, live updates on `inbox`.
- [ ] Projects pages: list, detail, instructions editor, sessions, start a direct
      session with any agent.
- [ ] Reorganise Settings:
      - providers and models
      - model classes, agents, instructions
      - computer (limits and egress)
      - notifications, appearance, account
      - developer: the inspector and model calls with drift
- [ ] Notifications: an in-app list and toasts.
- [ ] Web Push: VAPID keys sealed in Core, a subscription per device, a minimal
      payload, and delivery for questions and task results. Record the dependency
      decision in the log.
- [ ] PWA: manifest, icons, and a service worker that caches the shell only.
- [ ] Setup gains a "reach Eika from your phone" step. Document Tailscale serve
      in the README.
- [ ] ESLint rule: a feature imports another feature only through its
      `index.ts` (from `REFACTOR_PLAN.md` phase 3).
- [ ] Mock harness: a `World` for home and task sessions, the Inbox and projects;
      scenarios; phone baselines for every page; a touch emulation pass of the
      composer spec.
- [ ] Remove the transitional UI and what is left of the workbench.

**Exit:** on a phone over Tailscale:
1. Install the PWA.
2. Chat with Eika.
3. Watch a task in Activity.
4. Answer the task's question from a push notification.

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
      Fail closed: side-effecting tools return "Sentry unavailable" and the run
      parks.
- [ ] Policy:
      - hard rules
      - the defaults table by effect and taint
      - user rules (storage, matching, presets)
      Every table row is a unit test.
- [ ] Taint: session labels A and B set by ingest sources and private sources,
      cleared by compaction as specified, and mirrored into `sessions.taint`.
- [ ] Approvals:
      - durable, keyed by action, with wait and expiry
      - grouping of related approvals
      - user-token verification through `SELECT` on `core.auth_sessions`
      - Sentry's event stream relayed by Core on `inbox`
      Resuming a run gets the same approval back.
- [ ] Grants: the scopes, an in-memory cache with invalidation, and a revoke UI.
- [ ] Inbox approval cards (action, destination, recipients, content and diff,
      reason, labels, the answers) and push for every approval.
- [ ] Move `internal/egress` and `internal/netguard` into Sentry:
      - principals API
      - per-run proxy credentials in each `exec`'s environment
      - the `open` mode removed
      - `ask` per connection with a 60 s hold
      `web_fetch` and the hosted search backends go through the proxy as the
      calling run.
- [ ] Ingest v1, deterministic: pattern-based secret redaction and
      `<untrusted>` wrapping. Add the system-prompt line about untrusted content.
- [ ] Outbound "never share" tier: credentials, Luhn-valid card numbers, account
      numbers, ID formats.
- [ ] Halt: an always-visible button, resume, and Core's reaction (abort runs, a
      scheduler pause hook, a banner).
- [ ] Audit log and its UI timeline. Retention setting.
- [ ] Settings pages for Sentry rules and grants.
- [ ] Benchmarks: `decide` p99 under 2 ms, proxy overhead under 1 ms.
- [ ] `make smoke` covers Sentry: a scripted model triggers an `ask`, and the smoke
      test answers it through the user API.
- [ ] Update the security section of the style guide: Core holds model keys; Sentry
      holds third-party credentials; computers get proxy identities and nothing
      else.

**Exit:** a run in a session holding web content tries to `curl` a host it has
not used before. It parks, and an approval arrives on the phone. Approving lets it
continue; denying gives the agent a reason it can act on. Halt stops everything
in one press. The benchmarks pass.

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
- [ ] Add `history_search` and `history_read` (an FTS index over entry text).
- [ ] Recall hint: up to 3 FTS matches, names and descriptions only, in the
      `<context>` of each home or direct user message.
- [ ] Pre-compaction memory flush in home, direct and project sessions.
- [ ] Write policy in the `eika` prompt. Task agents are limited to the project
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
- [ ] Quiet hours apply to delivery. Urgency comes from `fast` for now. The
      background budget is enforced, and deferrals are shown.
- [ ] Shipped routines: Morning brief (disabled), Check-in (disabled, read-only
      grant).
- [ ] Schedules UI and Activity integration (runs, wakeups, budget).

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
- [ ] Outbound "needs consent" tier, with the user's own request counting as
      consent.
- [ ] Reviewer: isolated state, its questions and thresholds, a 5 s cut-off that
      becomes `ask`. Turn on the `review` verdict.
- [ ] Use the screen when a skill is installed and when a routine is created.
- [ ] Notification urgency `needs_attention_now` on `decision`.
- [ ] Settings warns when `decision` shares a pool with `main` without a spare
      slot.
- [ ] Hosted Jev may not see `sensitive` state unless the user opts in.
- [ ] `evals/sentry`: the innocent, injected and over-reach suites. Record attack
      success, utility and approval rate per model.

**Exit:**
- Injected content on a fixture page is flagged and wrapped.
- On the eval set, the reviewer allows submits that match the user's intent and
  asks on the others.
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
- [ ] Optional hybrid search: the `pgvector` image, the `embedding` class,
      embedding on write, hybrid ranking behind a setting. Measure against FTS
      alone on the recall suite before making it the default.
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
- [ ] Canaries planted at setup. A minimal tripwire halts on any canary in an
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

- [ ] `browser/` service on Playwright:
      - the command set
      - pruned interactive snapshots with refs, budgets, deltas and masking
      - `find` and `read`
      - tabs
      - the download quarantine
      - non-GET requests reported to Sentry
      Unit tests run against fixture pages.
- [ ] Container, networks, profile volume, hardening and compose.
- [ ] Sentry relay:
      - `browser` actions
      - form-submit decisions
      - hand-off on account creation, password changes, payments and terms
      - surrogate typing bound to origins
      - the per-site list
- [ ] `browser` agent definition, its tools, and the one-task browser lock.
- [ ] Images in tool results: a screenshot reaches a vision model as a user message
      after the tool result. Add the `DECISIONS.md` entry, which replaces the
      deferred note.
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
- An injected review on a fixture page fails to redirect the task.

## Phase 13: Monitor and auto-halt

Design: `design/sentry.md` sections 9 and 11.

- [ ] Core sends turns, reasoning excerpts and tool calls to `/v1/observe`.
- [ ] The full list of deterministic tripwires.
- [ ] Trajectory checks with `decision`, triggered and sampled. The reasoning
      review setting (off by default).
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
- **Several browser identities and profiles:** after phase 12, if needed.

## Log

Newest first. One entry per session: date, items done (commit hashes), the
`make check` and `make live` results, anything skipped and why, and findings for
later items.

- 2026-10-29: plan and `docs/design/` written from the design discussion. No items
  started except the `REFACTOR_PLAN.md` banner. Decisions taken in that
  discussion:
  - remove the coding UI and keep the sandboxes as computers
  - start from a fresh database
  - give Sentry its own container
  - support llama.cpp, TabbyAPI and vLLM or SGLang, with exact token accounting
    before backend-specific caching
