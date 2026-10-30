# Eika as a personal assistant: design

This folder holds the target design that turns Eika from a coding harness into a
persistent, always-on personal assistant that runs locally. `docs/PLAN.md` turns it
into phases and checklist items. `ARCHITECTURE.md` and `DECISIONS.md` describe what
is built today. When a phase lands, the parts of these documents it built move
there, and these documents keep only what is not built yet.

| Document | Covers |
|---|---|
| `README.md` | Goals, principles, vocabulary, topology, threat model, and what is kept or removed |
| `runtime.md` | Model classes, endpoints, exact token accounting, the inference queue, context layout, the loop, agents, the home session, projects, subagents, durability, compaction and history recall |
| `sentry.md` | The permission authority: policy, grants, approvals, taint, vault, surrogates, egress, scanning, review, monitoring, halting |
| `memory.md` | Instructions, memories, the index, search, episodic recall, writes and provenance, dreaming |
| `skills-and-plugins.md` | Agent Skills and MCP plugins |
| `scheduler.md` | Routines, wakeups, triggers, and background compute |
| `browser.md` | The browser container, the browser agent, live view and takeover |
| `interface.md` | The web app, mobile, notifications, external chat channels, remote access |

## Goals

1. **One assistant you message like a person.** It runs on hardware you own and keeps
   working while you are away. It remembers what matters and runs on its own for
   the most part. It asks you rarely, and mainly when it is unclear what you
   want.
2. **Local first.** It fits setups of many sizes and shapes, from one GPU to several
   machines, and assumes the constraints common to local hardware:
   - **Shared context.** A local setup typically has 128k to 256k tokens of unified
     context in total, and running agents take chunks of it. Nothing limits an
     agent to a share: a constrained setup may let one agent use the whole window
     and reload it on every swap to another agent, because enough context matters
     more than avoiding the reload.
   - **Costly prefill.** Prefill dominates the cost of a turn. Each agent keeps a
     fixed prefix (system prompt, tools, preamble, history) and changes it rarely,
     so the engine's cache keeps hitting. Some context swapping between agents is
     unavoidable, and the design keeps it low and measures what it costs.

   Cloud endpoints remain optional for any model class.
3. **Safe by structure, not by prompt.** Every consequential action is decided
   outside the model, by Sentry, a separate process. The model never holds a real
   credential.
4. **Transparent.** You can read and edit every memory, skill, grant, tool setting, approval
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
- **Slots, not assumptions.** The number of contexts that can run at once is a
  property of the endpoint (`slots`), not of the design: one on a small setup,
  many with several GPUs or a large VRAM pool. Work queues for a free slot, and
  background work yields to the user at every model-call boundary, so
  interactive latency holds at any slot count.
- **Autonomous by default.** Eika is meant to run for hours or days without
  checking in. Every question is a cost to the user's day, so the design spends
  its effort on not asking: a decision model judges actions with external effects before
  the user is asked, grants turn one answer into many, approvals reach the user as
  a digest rather than a stream, and Eika's own questions are rare (`sentry.md` 5,
  "Asking less").
- **Sentry is invisible to the model.** No prompt, tool description or notice
  mentions it, and the agentic loop is the standard one. A blocked action is a
  generic `Blocked: <reason>` result (`sentry.md` 4).
- **The rule of two**, narrowed (after Meta, 2025). A context holding sensitive
  data must not cause an external effect without a human or the reviewer in the
  loop. Sentry labels sessions that hold sensitive data and gates effects; labels
  route to the decision model rather than the user, to limit approval fatigue.
  Untrusted content is scanned and wrapped, not labelled, since nearly every
  session reads some (`sentry.md` 6).
- **One conversation.** The user works with Eika in a single home session that
  never ends, like working with one person. Eika keeps several lines of work going
  through named, persistent subagents the user can watch but not talk to directly.
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
Browser     The `eika-browser` container: headed Chrome plus `browserd`, reachable only
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
Model class deep, main, fast (chat models); decision (System One).
            Each class names one model row.
Endpoint    A provider base URL with its hardware pool and slot count.
Session     A conversation (the existing session tree). Kinds: home, subagent,
            routine, dream.
Home        The one session the user talks to Eika in, from every channel. It
            never ends; compaction and history recall keep it going.
Subagent    A named, persistent session Eika delegates a line of work to. It
            reports back, and a later message resumes it in its own context.
Project     A long-lived home for related sessions: instructions, memory scope, a
            directory on a computer, routines, defaults.
Routine     A scheduled or triggered job. A wakeup is an agent resuming its own
            session later.
Run         One execution of the loop on a session.
Action      Anything Sentry decides on. A grant is a capability, an approval a
            pending user decision, an alert a finding, a halt a full stop.
Taint       The session label `sensitive` (holds private data), plus the flag
            `suspicious` (the injection screen found something).
Surrogate   A placeholder string that stands for a vault secret.
Inbox       The UI for approvals, questions from Eika, secret requests and alerts.
Activity    The UI for watching subagents, routine runs and dreams.
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
| Taint | Core labels, Sentry, the reviewer | Private data leaving through an unapproved effect; actions from a session where the injection screen found something |
| Egress proxy | Sentry, network-enforced | Exfiltration from code in a computer, the browser or a plugin |
| Vault and surrogates | Sentry | Credential theft; a surrogate resolves only for its bound domains |
| Ingress scanning | Sentry, on untrusted results | Obvious injections, secrets and one-time codes reaching a model |
| Isolated reviewer | Sentry, for `review` actions | Injected instructions steering an action; the reviewer never sees the untrusted content |
| Memory provenance | Core and Sentry | Poisoned memories reaching the auto-loaded index |
| Monitor and canaries | Sentry, asynchronous | Misbehaviour patterns; any canary in egress halts everything |
| Halt switch | UI, Sentry | Everything, immediately |

Current caveats:

- Core holds the Docker socket, which is root-equivalent on the host. A compromised
  Core is out of scope. A Docker socket proxy is listed as hardening in phase 15.
- Classifiers are bypassable by adaptive attacks. They escalate; they do not block
  on their own.
- A subagent's report can still carry misleading data into the main agent. The
  `sensitive` label carries along with it; untrusted origin does not.
- A session without sensitive data that reads an injected page can do what the
  defaults allow without asking, such as writing its own files or reaching a new
  host (`sentry.md` 6). External sends and deletes still go to the reviewer.
- The reviewer is a model. Sentry sends actions with external effects to it instead of the
  user, so an adaptive attack on the reviewer's inputs matters more (it sees no
  untrusted content, only the action).
- Egress is not judged per connection. A program already running, or a script the
  agent wrote earlier and starts with an innocent-looking command, can reach any
  public host not on the blocklist, and a page script that sends data on its own
  is not caught (`sentry.md` 10, `browser.md` 4).

## Kept, changed, removed

| Area | Today | Becomes |
|---|---|---|
| `agent` loop, compaction, retries, overflow, queues | Pi port, well tested | Kept, with the additions in `runtime.md`. Home compaction gains deterministic sections, idle runs and history recall (`runtime.md` 11) |
| `provider`, `provider/openai`, timings | OpenAI-compatible streaming | Kept. Adds the Responses API as the preferred protocol, backend probing, exact token counting, cache metrics, slot hints, the System One kind |
| `tool`, registry, `ask_user` broker | Stateless tools, in-memory broker | Kept. The broker becomes durable. A subagent's questions go to Eika, who asks the user in the Inbox when needed |
| `event` bus, WebSocket, replay | Topics global, workspace, session | Kept. Adds topics `inbox` and `activity`; drops `workspace` |
| `workspace`, `executor`, `eikad` | Coding sandboxes | Renamed to computers. Worktree, clone and push code removed; per-exec environment kept |
| `egress`, `netguard` | Proxy in Core | Moved into Sentry; identity per run instead of per workspace |
| `secret` | Seals everything in Core | Kept in Core for model keys; Sentry has its own vault with its own key |
| `mcp` | Pool in Core | Reused by Sentry's plugin host; stdio servers move from workspaces to plugin cells |
| `search`, `fetch` | Core tools | Kept. Fetch goes through Sentry's proxy, and results are scanned |
| `session` tree, entries, runs | Branching tree | Kept. Adds `preamble` and `notice` entry kinds and session kinds; commit SHAs removed; the UI shows it linear |
| `profiles` | Run configurations | Replaced by agent definitions |
| `utility` models | Task-to-model map | Replaced by model classes |
| `subagent` spawner | One-shot children in git worktrees | Replaced by named, persistent, asynchronous subagents (`runtime.md` 9) |
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
  questions and returns calibrated probabilities. Eika connects to whatever
  `/v1/systemone` endpoint it is given: hosted Jev, a local server such as
  llama.cpp, Laya (`laya-serve`), or a chat model emulating it with constrained
  output. The recommended local model is vLLM-SR `d3-flash` (`sentry.md` 9,
  "Decision model").
- The injection screen is a dedicated classifier behind whatever endpoint is
  configured. The recommendation is Horizon-Labs `prompt-injection-guard-small`,
  not Prompt Guard 2, which misses injections that are not phrased as jailbreaks,
  and not a System One model, whose answer the screened content can steer
  (`sentry.md` 6).
- Agents never write skills. They write memories, which Sentry gates.
- One home session for the user, across every channel, with no direct sessions.
  The user watches subagents read-only and steers them through Eika. More home
  sessions can come later without a schema change.
- Lines of work are named, persistent subagents, not a separate thread concept.
- Home compaction is Pi's rolling update plus deterministic sections in the style
  of Pi-VCC, with `history_search` and `history_read` over every entry. Chapters
  (frozen per-span summaries) are deferred.
- Taint is only `sensitive`, plus the evidence-based `suspicious` flag, and labels
  route to the decision model, not the user. A label for "has read untrusted
  content" was dropped as too broad.
- Sentry is invisible to the model by default.
