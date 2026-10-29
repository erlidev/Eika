# Eika as a personal assistant: design

This folder holds the target design that turns Eika from a coding harness into a
persistent, always-on personal assistant that runs locally. `docs/PLAN.md` turns it
into phases and checklist items. `ARCHITECTURE.md` and `DECISIONS.md` describe what
is built today. When a phase lands, the parts of these documents it built move
there, and these documents keep only what is not built yet.

| Document | Covers |
|---|---|
| `README.md` | Goals, principles, vocabulary, topology, threat model, and what is kept or removed |
| `runtime.md` | Model classes, endpoints, exact token accounting, the inference queue, context layout, the loop, agents, sessions, projects, delegation, durability |
| `sentry.md` | The permission authority: policy, grants, approvals, taint, vault, surrogates, egress, scanning, review, monitoring, halting |
| `memory.md` | Instructions, memories, the index, search, episodic recall, writes and provenance, dreaming |
| `skills-and-plugins.md` | Agent Skills and MCP plugins |
| `scheduler.md` | Routines, wakeups, triggers, and background compute |
| `browser.md` | The browser container, the browser agent, live view and takeover |
| `interface.md` | The web app, mobile, notifications, external chat channels, remote access |

## Goals

1. **One assistant you message like a person.** It runs on hardware you own and keeps
   working while you are away. It remembers what matters, and it asks before it does
   anything consequential.
2. **Local first.** It is designed for a single GPU running one model at a time,
   with a context window of 256k tokens at most, where prefill is the dominant cost.
   Cloud endpoints remain optional for any model class.
3. **Safe by structure, not by prompt.** Every consequential action is decided
   outside the model, by Sentry, a separate process. The model never holds a real
   credential.
4. **Transparent.** You can read and edit every memory, skill, rule, grant, approval
   and model request in the UI. Token counts are measured, never guessed.
5. **Keeps the sandbox work.** Docker sandboxes, eikad, the egress proxy, sealed
   secrets and the MCP client carry over. The coding-specific layer does not.

Non-goals for now: multiple users, payments, voice, a native mobile app, and agents
that write their own skills.

## Principles

- **Eika proposes, Sentry disposes.** Sentry is the only grantor of tool calls,
  plugin calls, memory writes and network egress. Approvals are capabilities that
  the user creates in the UI. They are never conversational.
- **The prefix is sacred.** Context is append-only. Anything dynamic goes at the
  tail. A session's tools never change. Cache hit rate is the main efficiency metric.
- **Measured, not estimated.** Context size, cache reuse, prefill and decode come
  from the endpoint. An estimate is labelled as one wherever it is shown.
- **Small always-loaded context, everything else on demand.** This applies to the
  memory index, the skills catalog, the plugin catalog and tool schemas.
- **Sequential by default.** One GPU means one active context. Background work
  yields to the user at every model-call boundary.
- **The rule of two** (Meta, 2025). Within one context an agent should not combine
  (A) untrusted input, (B) private data and (C) external effects without a human in
  the loop. Sentry tracks A and B per session and gates C.
- **Deterministic first, models second.** Policy, taint and secret detection are
  code. Classifiers and reviewer models escalate; they are never the only barrier.
- **Files the user can read.** Memories and skills are Markdown on disk, versioned
  in git and indexed in Postgres.
- **The repository's rules still hold.** `AGENTS.md` and `STYLE_GUIDE.md` apply
  unchanged: one way to do things, the standard library first, tests with every
  change, and `make check` as the gate.

## Vocabulary

```
Eika        The whole system, and the persona of the main agent.
Core        The `eika` container (cmd/eika): API, UI, agent runtime, memory,
            skills, scheduler, projects. Holds the Docker socket and model keys.
Sentry      The `eika-sentry` container (cmd/eika-sentry): policy, grants,
            approvals, vault, surrogates, egress proxy, plugin host, scanning,
            review, monitor, audit. Holds every third-party credential.
Computer    A sandbox container plus volume running eikad (formerly a workspace).
            Eika's default computer is `home`.
Browser     The `eika-browser` container: Chromium plus `browserd`, reachable only
            from Sentry.
Guard       Optional `eika-guard` container serving local classifiers and
            decision models on CPU.
Plugin      An MCP server. Remote plugins are connected by Sentry; stdio plugins run
            in a plugin cell, a computer no agent can execute in.
Skill       An Agent Skills folder (SKILL.md plus resources), installed by the user.
Memory      One Markdown file with frontmatter, scope global or project:<slug>.
Instructions  Text only the user writes: "About me", standing rules, per-project
            instructions. Agents read it and never write it.
Agent       A definition: name, model class, prompt, fixed tool list, access profile.
            Built in: eika, worker, researcher, browser, dreamer.
Model class deep, main, fast (chat models); decision (System One); embedding.
            Each class names one model row.
Endpoint    A provider base URL with its hardware pool and slot count.
Session     A conversation (the existing session tree). Kinds: home, direct, task,
            routine, dream.
Task        A session started by delegation, with a lifecycle state and a result.
Project     A long-lived home for related sessions: instructions, memory scope, a
            directory on a computer, routines, defaults.
Routine     A scheduled or triggered job. A wakeup is an agent resuming its own
            session later.
Run         One execution of the loop on a session.
Action      Anything Sentry decides on. A grant is a capability, an approval a
            pending user decision, an alert a finding, a halt a full stop.
Taint       Session labels: untrusted (A) and sensitive (B).
Surrogate   A placeholder string that stands for a vault secret.
Inbox       The UI for approvals, questions from tasks, secret requests and alerts.
Activity    The UI for tasks, routine runs and dreams.
```

## Topology

```
                     phone / desktop browser (PWA)
                               |  HTTPS (Tailscale serve or a reverse proxy)
                               v
  +------------------------ eika (Core) ---------------------------+
  | API + UI, event bus, agent runtime, inference queue,           |
  | memory (git + index), skills, scheduler, projects, channels    |
  | docker.sock: creates computers, plugin cells, browser          |
  +-----+--------------------+-------------------+-----------------+
        | unix socket         | HTTP              | eikad HTTP
        | (decide, scan,      | (model calls)     | (exec, files)
        |  plugins, browser)  v                   v
        |               LLM endpoints        computers (eika-cx-<id>)
        v               (host.docker.        eika_sandbox_internal network
  +------------- eika-sentry -------------+          | HTTP(S)_PROXY, per-run identity
  | policy, grants, approvals, taint,     |<---------+
  | vault (own key, own schema),          |<------ plugin cells (stdio MCP)
  | egress proxy :3128, plugin host,      |<------ eika-browser (all traffic)
  | scanners, reviewer, monitor, audit    |------> eika-browser control (only Sentry)
  +-----+--------------+------------------+------> internet (netguard)
        |              |
        v              v
  eika-guard       postgres 16: schema core (role eika_core)
  (optional)                    schema sentry (role eika_sentry)
                   searxng
```

- Only Core publishes a port, on loopback.
- Every network path out of a computer, a plugin cell or the browser goes through
  Sentry's proxy.
- Model calls go from Core to the endpoints directly. The user chose the
  endpoints, and model keys live in Core.
- Core has no route to the browser's control port and no access to Sentry's schema
  or key.
- Sentry has no Docker socket. Core registers each container it creates with
  Sentry, and Sentry issues that container its proxy identity.

## Threat model

The adversary is content: web pages, emails, plugin output, downloaded files and
anything else a model reads that the user did not write. It tries to make an agent
exfiltrate data, take actions the user did not ask for, poison memory, or escalate
its own permissions. A secondary risk is the model misbehaving on its own:
over-reaching scope, being careless with destructive actions, or deceiving.

Defences, in the order they apply:

| Layer | Where | What it stops |
|---|---|---|
| Capability per agent | Core, agent definitions | An agent cannot call a tool it was not given; the browser agent has no memory, the researcher no private data |
| Sentry decide | Sentry, before every action | Unapproved effects, hand-off actions, actions while halted, rule-of-two violations |
| Taint | Core labels, Sentry rules | Escalation from contexts holding untrusted content |
| Egress proxy | Sentry, network-enforced | Exfiltration from code in a computer, the browser or a plugin |
| Vault and surrogates | Sentry | Credential theft; a surrogate resolves only for its bound domains |
| Ingress scanning | Sentry, on untrusted results | Obvious injections, secrets and one-time codes reaching a model |
| Isolated reviewer | Sentry, for `review` actions | Injected instructions steering an action; the reviewer never sees the untrusted content |
| Memory provenance | Core and Sentry | Poisoned memories reaching the auto-loaded index |
| Monitor and canaries | Sentry, asynchronous | Misbehaviour patterns; any canary in egress halts everything |
| Halt switch | UI, Sentry | Everything, immediately |

Accepted residual risks, documented rather than solved:

- Core holds the Docker socket, which is root-equivalent on the host. A compromised
  Core is out of scope. A Docker socket proxy is listed as hardening in phase 15.
- Classifiers are bypassable by adaptive attacks. They escalate; they do not block
  on their own.
- A summary from a quarantined agent can still carry misleading data into the main
  agent. Taint carries along with it.

## Kept, changed, removed

| Area | Today | Becomes |
|---|---|---|
| `agent` loop, compaction, retries, overflow, queues | Pi port, well tested | Kept, with the additions in `runtime.md` |
| `provider`, `provider/openai`, timings | OpenAI-compatible streaming | Kept. Adds backend probing, exact token counting, cache metrics, slot hints, the System One kind |
| `tool`, registry, `ask_user` broker | Stateless tools, in-memory broker | Kept. The broker becomes durable, and questions from tasks go to the Inbox |
| `event` bus, WebSocket, replay | Topics global, workspace, session | Kept. Adds topics `inbox` and `activity`; drops `workspace` |
| `workspace`, `executor`, `eikad` | Coding sandboxes | Renamed to computers. Worktree, clone and push code removed; per-exec environment kept |
| `egress`, `netguard` | Proxy in Core | Moved into Sentry; identity per run instead of per workspace |
| `secret` | Seals everything in Core | Kept in Core for model keys; Sentry has its own vault with its own key |
| `mcp` | Pool in Core | Reused by Sentry's plugin host; stdio servers move from workspaces to plugin cells |
| `search`, `fetch` | Core tools | Kept. Fetch goes through Sentry's proxy, and results are scanned |
| `session` tree, entries, runs | Branching tree | Kept. Adds `preamble` and `reminder` entry kinds and session kinds; commit SHAs removed |
| `profiles` | Run configurations | Replaced by agent definitions |
| `utility` models | Task-to-model map | Replaced by model classes |
| `subagent` spawner | Git worktrees | Replaced by task delegation |
| `projects` (git), hub, worktrees, merge, diff, commit, push, previews, `contextfile` | Coding | Removed. "Project" is redefined |
| Frontend | Dense three-pane workbench | New mobile-first shell; client, stream, reducer, Markdown, composer, settings and MCP screens kept |
| Migrations 0001 to 0017 | Incremental | Squashed into a new baseline (fresh start) |

## Decisions this design rests on

These came out of the design discussion. Each is recorded in `DECISIONS.md` when
the phase that implements it lands.

- The coding UI is removed. Sandboxes stay as Eika's computers, and git projects,
  worktrees, the hub, and the files, terminal and diff panels go.
- The database starts fresh from a new baseline migration.
- Sentry is its own binary and container, with its own Postgres schema, role and
  key file.
- llama.cpp, TabbyAPI/exllamav3 and vLLM/SGLang must all work. Exact token accounting
  comes first. Backend-specific cache tricks are limited to what is cheap: slot
  affinity and reading cache metrics.
- "Jev-style System One" means a separate model kind that takes state plus typed
  questions and returns calibrated probabilities. The backends are hosted Jev, local
  Laya (`laya-serve`, Jev-compatible), or a chat model emulating it with constrained
  output.
- Agents never write skills. They write memories, which Sentry gates.
