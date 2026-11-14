# Eika as a personal assistant: design

This folder holds the target design that turns Eika from a coding harness into a
persistent, always-on personal assistant that runs locally. `docs/PLAN.md` turns it
into phases and checklist items. `ARCHITECTURE.md` and `DECISIONS.md` describe what
is built today. When a phase lands, the parts of these documents it built move
there, and these documents keep only what is not built yet.

`PLAN.md` was cut down on 2026-11-01. Work that was cut is in `docs/DEFERRED.md`.
Its design stays here, marked deferred, as the target if it returns: most of
`browser.md`, sections 8 and 9 of `sentry.md`, and dreaming in `memory.md`.

| Document | Covers |
|---|---|
| `README.md` | Goals, principles, vocabulary, topology, threat model, and what is kept or removed |
| `runtime.md` | Remaining context layout and loop changes, agents, the home session, projects, subagents, durability, compaction and history recall |
| `sentry.md` | The permission authority: policy, what asks, ingest and the injection screen, approvals and grants, egress, halting; deferred: vault, surrogates, review, monitoring |
| `memory.md` | Instructions, memories, the index, search, episodic recall, screened writes, dreaming |
| `skills-and-plugins.md` | Agent Skills and MCP plugins |
| `scheduler.md` | Routines and wakeups |
| `browser.md` | The browser container, the browser agent, live view and takeover |
| `web-fetch.md` | What `web_fetch` shows the model, reading further by line, section and search, conversion to Markdown, site readers, and the reader that answers a question about a page |
| `compaction.md` | Compaction in every session: the record code writes, the state the model writes, stubs in the kept tail, preparing a compaction while the cache is warm, recall for every agent, and `evals/compaction` |
| `interface.md` | The web app, mobile, notifications, external chat channels, remote access |
| `ui.md` | The web app's design: stack, visual language, shell, each page and its components, performance budgets, and how presence, a Live2D avatar and live speech fit in |

Phase 1's implemented runtime is in
[Architecture](../ARCHITECTURE.md#endpoints-models-and-classes), with reasons in
[Decisions](../DECISIONS.md#agent-loop-and-providers).

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
   outside the model, by Sentry, in code no agent can reach. The model never holds a
   real credential.
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
- **Safe without asking.** Safety comes from what runs on its own: a screen on what
  agents read, capability per agent, hard blocks and halt. An approval the user
  gives without reading protects nothing, so exactly two things ask, and each has
  a way to stop (`sentry.md` 5, "What asks").
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
  checking in. Every question is a cost to the user's day: reading, running
  commands and remembering never ask, "always" turns one answer into many, and
  Eika's own questions are rare (`sentry.md` 5).
- **Sentry is invisible to the model.** No prompt, tool description or notice
  mentions it, and the agentic loop is the standard one. A blocked action is a
  generic `Blocked: <reason>` result (`sentry.md` 4).
- **The rule of two, by capability** (after Meta, 2025). A context that holds
  private data and reads untrusted content must not cause an external effect
  without a human in the loop. Eika holds it by which agent has which tools, not by
  labels: only `eika` has memory and plugins, and it cannot run commands or fetch;
  workers and researchers can, and hold only their brief. Plugin tools that are not
  read-only ask. Untrusted content is screened and wrapped where it enters
  (`sentry.md` 6).
- **One conversation.** The user works with Eika in a single home session that
  never ends, like working with one person. Eika keeps several lines of work going
  through named, persistent subagents the user can watch but not talk to directly.
- **Deterministic first, models second.** Policy and secret detection are code,
  and no model decides an action. The one classifier, the injection screen, removes
  suspect text on the way in and is never the only barrier.
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
Sentry      The permission authority, a package in Core (internal/sentry): policy,
            ingest, approvals, grants, audit, halt.
Guard       The service that runs the injection screen's model (guard/).
Computer    A sandbox container plus volume running eikad (formerly a workspace).
            Eika's default computer is `home`.
Plugin      An MCP server. Stdio plugins run in a shared plugin computer no agent
            can execute in.
Skill       An Agent Skills folder (SKILL.md plus resources), installed by the user.
Memory      One Markdown file with frontmatter. Only Eika reads and writes them.
Instructions  Text only the user writes: "About me", standing rules, per-project
            instructions. Agents read it and never write it.
Agent       A definition: name, model class, prompt, fixed tool list.
            Built in: eika, worker, researcher.
Model class deep, main, fast (chat models); decision (System One, unused for now).
            Each class names one model row.
Endpoint    A provider base URL with its hardware pool and slot count.
Session     A conversation (the existing session tree). Kinds: home, subagent,
            routine.
Home        The one session the user talks to Eika in, from every channel. It
            never ends; compaction and history recall keep it going.
Subagent    A named, persistent session Eika delegates a line of work to. It
            reports back, and a later message resumes it in its own context.
Project     A long-lived home for related sessions: instructions, a directory on
            a computer, routines.
Routine     A scheduled job. A wakeup is an agent resuming its own session later.
Run         One execution of the loop on a session.
Action      Anything Sentry decides on. A grant is a capability, an approval a
            pending user decision, a halt a full stop.
Ingest      What every untrusted result passes before a model reads it:
            redaction, the screen, the `<untrusted>` wrapper.
Inbox       The UI for approvals, elicitations and pending memories.
Activity    The UI for watching subagents and routine runs.
```

## Topology

```
                     phone / desktop browser (PWA)
                               |  HTTPS (Tailscale serve or a reverse proxy)
                               v
  +------------------------ eika (Core) ---------------------------+
  | API + UI, event bus, agent runtime, inference queue,           |
  | memory (git + index), skills, scheduler, projects,             |
  | Sentry (gate, approvals, grants, audit), MCP pool,             |
  | egress proxy :3128                                             |
  | docker.sock: creates computers                                 |
  +-----+------------------+---------------------+-----------------+
        |                  |                     | eikad HTTP (exec, files)
        v                  v                     v
  postgres 16        LLM endpoints        computers (eika-cx-<id>)
  schema core        (host.docker.        eika_sandbox_internal network
  searxng             internal, cloud)    HTTP(S)_PROXY back to Core
                                          the plugin computer (stdio MCP)
```

- Only Core publishes a port, on loopback.
- Every network path out of a computer goes through the proxy.
- Model calls go from Core to the endpoints directly. The user chose the
  endpoints, and model keys live in Core.

The Sentry, Browser and Guard containers are part of the deferred design
(`DEFERRED.md`).

## Threat model

The adversary is content: web pages, emails, plugin output, downloaded files and
anything else a model reads that the user did not write. It tries to make an agent
exfiltrate data, take actions the user did not ask for, poison memory, or escalate
its own permissions. A secondary risk is the model misbehaving on its own:
over-reaching scope, being careless with destructive actions, or deceiving.

Defences, in the order they apply:

| Layer | Where | What it stops |
|---|---|---|
| Capability per agent | Core, agent definitions | An agent cannot call a tool it was not given: only `eika` has memory and plugins, and the researcher has no private data |
| Sentry decide | Before every action | Plugin effects the user has not approved, credentials and canaries in outbound arguments, actions while halted |
| Egress proxy | Network-enforced | Traffic that bypasses the gate: blocklisted hosts and private addresses are refused, and halt cuts everything. It does not judge other hosts (caveat below) |
| Ingest | On untrusted results, and on reports to `eika` | Secrets and one-time codes reaching a model; text the screen flags as an injection is removed; content arrives wrapped as data |
| Memory screen | On memory writes | Poisoned memories reaching the auto-loaded index |
| Canary | Before every action | An agent that went looking for keys: the fake key in a tool call halts Eika |
| Halt switch | UI | Everything, immediately |

Deferred layers, in `DEFERRED.md`: vault and surrogates, the isolated reviewer, the
monitor, and TLS interception for canaries read from the environment.

Current caveats:

- Core holds the Docker socket, which is root-equivalent on the host, and Sentry and
  every third-party credential live in Core's process. A compromised Core is out of
  scope. A Docker socket proxy and Sentry's own container return together
  (`DEFERRED.md`).
- The screen misses planted instructions: about half on InjecAgent and more on
  BIPIA (`sentry.md` 6). Wrapping marks content as data, and the model can still be
  talked out of that.
- A session that follows an instruction the screen missed can do what its agent's
  tools allow without asking, such as writing its own files or reaching a new host.
  A worker can send out its project's files, and `eika` can put private data into
  a researcher's brief (`sentry.md` 6, "What this does not stop"). Plugin tools
  that are not read-only still ask.
- `bash` output and file reads are not screened, so a page read with `curl` is not.
- A subagent's report reaches the main agent screened but unwrapped, and can carry
  misleading data. The memory screen checks what Eika saves from it.
- Egress is not judged per connection. A program already running, or a script the
  agent wrote earlier and starts with an innocent-looking command, can reach any
  public host not on the blocklist (`sentry.md` 10).

## Kept, changed, removed

| Area | Today | Becomes |
|---|---|---|
| `agent` loop, compaction, retries, overflow, queues | Pi port, well tested | Kept, with the additions in `runtime.md`. Home compaction gains deterministic sections, idle runs and history recall (`runtime.md` 11) |
| `provider`, `provider/openai`, timings | OpenAI-compatible streaming | Kept. Adds the Responses API as the preferred protocol, backend probing, exact token counting, cache metrics, slot hints, the System One kind |
| `tool`, registry, `ask_user` broker | Stateless tools, in-memory broker | Kept. A subagent's questions go to Eika, who asks the user in chat when needed |
| `event` bus, WebSocket, replay | Topics global, computer, session | Kept. Adds topics `inbox` and `activity`; drops `computer` |
| `computer`, `executor`, `eikad` | Built in phase 0 (`ARCHITECTURE.md`) | Kept. A shared plugin computer is added (`runtime.md` 10) |
| `egress`, `netguard` | Proxy in Core | Kept in Core, with the `open` mode removed (`sentry.md` 10) |
| `secret` | Seals everything in Core | Kept |
| `mcp` | Pool in Core | Kept. Agents reach it through `plugin_find` and `plugin_call`; stdio servers move from computers to the plugin computer |
| `search`, `fetch` | Core tools | Kept. They check the blocklist and halt, and results are wrapped |
| `session` tree, entries, runs | Branching tree | Kept. Adds `preamble` and `notice` entry kinds and session kinds; the UI shows it linear |
| `profiles` | Run configurations | Replaced by agent definitions |
| `utility` models | Task-to-model map | Replaced by model classes |
| Subagents | Removed in phase 0 | Named, persistent, asynchronous subagents (`runtime.md` 9) |
| Frontend | Chats workbench, without the coding UI | New mobile-first shell; client, stream, reducer, Markdown, composer, settings and MCP screens kept |

## Decisions this design rests on

These came out of the design discussion. Each is recorded in `DECISIONS.md` when
the phase that implements it lands.

- Sentry is a package in Core until the Docker socket proxy gives a separate process
  something to protect (`DECISIONS.md`).
- Exact token accounting comes first. It is verified on llama.cpp; vLLM, SGLang,
  TabbyAPI and cloud endpoints are built from their documentation and verified when
  one is on the bench. Backend-specific cache tricks are limited to what is cheap:
  slot affinity and reading cache metrics.
- Agents never write skills. Eika writes memories, which the gate checks.
- One home session for the user, across every channel, with no direct sessions.
  The user watches subagents read-only and steers them through Eika. More home
  sessions can come later without a schema change.
- Lines of work are named, persistent subagents, not a separate thread concept.
- Home compaction is Pi's rolling update plus deterministic sections in the style
  of Pi-VCC, with `history_search` and `history_read` over every entry. Chapters
  (frozen per-span summaries) are deferred.
- There are no labels. Two things ask (a plugin tool that is not read-only, and a
  new routine), and everything else is allowed or blocked by rule.
- Sentry is invisible to the model by default.
