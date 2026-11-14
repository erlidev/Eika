# Decisions

Why Eika is built the way it is. One entry per decision: the choice, then the
reason. Read the relevant section before changing an area; add an entry
before implementing an architectural change (new package, dependency,
persistence shape, or interface). `ARCHITECTURE.md` says how things work;
this file says why, so neither repeats the other.

## Scope

- **Eika is a personal assistant that runs on hardware the user owns.** One
  person talks to it, and it works in sandboxed computers on their behalf.
  Memory, plugins, a scheduler, skills and a permission authority (Sentry) are
  planned in `PLAN.md` and `design/`, and built phase by phase; a browser and
  chat channels are in `DEFERRED.md`. Today there are chats, the `home` computer, a sandboxed `bash`, web
  search and MCP. It is not a coding harness: it is not meant to edit a
  repository, review a diff, or serve a developer's workbench. Not goals for
  now: several users, payments, voice, a native mobile app, and agents that
  write their own skills.
- **Origin.** The agent loop, tools and session trees are a Go port of the
  [Pi coding agent](https://github.com/badlogic/pi-mono) core.
- **Deferred:** everything in `PLAN.md` after phase 0, in its phase order.
- **Extensibility is source-level.** Implement an interface, register it in
  one registry file, rebuild. No runtime plugin loader, no `init()`
  registration.
- **Single user.** One password; an optional `EIKA_AUTH_TOKEN` for scripts.

## What the first version leaves out

The plan grew to 16 phases, and the design to three new containers with services
in Python and TypeScript, before anything past phase 1 was built. On 2026-11-01 it was cut
to what a working assistant needs, and the rest moved to `DEFERRED.md` with a trigger
for each piece.

- **Sentry is a package in Core, not a container.** A separate process protects
  against a compromised Core, which the threat model leaves out of scope, and Core's
  Docker socket can reach Sentry's volumes anyway, so `Core cannot read the vault`
  would hold only for a Core that behaves. The cost was most of phase 4: a second
  binary, roles, schema, migrator, wire contract and event relay, plus a second
  database reset. The separation is kept in code by the boundary test and the `Gate`
  interface. Rejected: keeping the container for defence in depth, since it buys
  little until the Docker socket proxy exists; they return together. This reverses
  the 2026-10-29 decision.
- **The reviewer is deferred, so external effects ask.** Narrowed on 2026-11-09 to
  plugin tools that are not read-only ("Safety without asking", below). This
  reverses the 2026-10-30 decision to land it in phase 4 on the emulated backend. The review moved it there
  so labels would not become a stream of approvals. With fixed labels, a label only
  changes `bash` and `web_fetch` in private projects, and no agent can send anything
  until plugins, so asks stay low and can be counted from `audit` before deciding.
  When it returns it uses `/v1/systemone` alone, since the design's numbers have
  emulation allowing 50% of steered attacks against d3-flash's 17%. The cost is
  real: until then, every external send and delete asks unless an `always` grant
  covers it.
- **Labels are fixed when a session is created.** Superseded on 2026-11-09: there
  are no labels ("Safety without asking", below). Without the reviewer or the browser,
  tracking reads changes almost nothing: `eika` has no `bash` or `web_fetch`, so
  only private-project workers feel a label, and those are known at creation. This
  removes `/v1/labels`, `private?`, `suspicious`, and clearing a label at compaction,
  which was unsound anyway: the rolling summary can still hold what the label was for.
  `web_search` stays `allow` in `eika` sessions, which are always labelled, so a
  private fact can reach a search engine in a query.
- **An `always` grant may cover an external send**, matched to one tool and target.
  It used to be barred from a `sensitive` session. Every `eika` session is `sensitive`
  now, so the bar would have left `once` as the only answer to every send, for ever.
  This stands, without the labels: "always" is how an ask stops.
- **Plugins come before the scheduler and skills.** They are most of what makes an
  assistant useful, and a routine needs them to have anything to report. They reach
  only `eika`, on the pool Core already has, with no plugin host and one shared
  plugin computer rather than a cell per plugin.
- **Deferred whole: the browser, the vault and surrogates, the injection screen and
  Guard, the monitor, dreaming and channels.** The screen came back on 2026-11-09
  ("Safety without asking", below). The browser is the largest single
  build, and search, `web_fetch` and plugins cover most tasks. The vault is only
  needed by the browser and by secrets used from `bash`, and plugin credentials stay
  sealed in Core. The screen is evidence, never a barrier, at 49% on InjecAgent and 22
  to 37% on BIPIA, and it brings a Python service. Channels are first in line to
  return.
- **Web Push gives way to ntfy.** One HTTP POST against VAPID keys, RFC 8291 and a
  subscription per device, and a payload that says the same thing.
- **Restarts end runs rather than resume them.** Named subagents already resume by
  message, so a restart needs only a clean ending: every unanswered call gets "outcome
  unknown", and Eika decides what to continue. This also removes the `dispatched`
  marker, since no call is ever re-dispatched.
- **Questions go through the chat.** A subagent's question reaches Eika as a notice,
  and Eika asks the user in its reply. `inbox_ask` and the `questions` table were a
  second way for Eika to talk to the user. The Inbox moves to phase 4, since nothing
  fills it before approvals. (It is phase 5 since 2026-11-09: pending memories are
  the first thing in it.)
- **Memory keeps Markdown files and git**, against a Postgres-only option that would
  have removed `Reconcile` and the double write. Files the user can read won. Memory is
  global, and only `eika` reads or writes it: subagents get what they need from their
  brief, so there is no project scope, and a project's living summary is a `STATUS.md`
  in its directory.
- **Phase 1's built work stays**, including what would have been cut had it not been
  built (`PLAN_LOG.md`, 2026-11-01). It is finished and tested, and removing it would
  be more work and more risk than keeping it. Only further verification was cut.

## Safety without asking

Decided 2026-11-09, before any of phase 4 was built. The cut above left Sentry
deterministic and made up for the missing reviewer with approvals: a `sensitive`
label on every `eika` session and every private project, and an ask before a
labelled session's `web_fetch` and `bash`. Design: `design/sentry.md`.

- **Approvals are not the safety mechanism.** A user who is asked before every
  fetch and command in a private project approves without reading within a week,
  and then the ask protects nothing and still costs attention. Safety has to come
  from things that run without the user: a screen on what agents read, capability
  per agent, hard blocks, and halt. Rejected: keeping the asks until the reviewer
  returns, since the reviewer was to return only when asks had become annoying,
  which makes the annoyance the plan.
- **Two things ask, and the list is closed.** A plugin tool that is not read-only,
  and creating or enabling a routine. Both are deterministic, both are about an
  effect outside Eika that cannot be undone or an action taken while nobody
  watches, and both stop: "always" for a tool or for one recipient, and once per
  routine. Adding a third needs an entry here that says why no rule or screen could
  do the job.
- **The injection screen is back, in phase 4, and it removes what it flags.** The
  cut deferred it as "evidence, never a barrier". That holds, and evidence is still
  worth having when it costs 55 ms on a CPU, since the alternative on offer was
  nothing or an ask. It runs on every `web_fetch` result, search result and plugin
  result, on exactly the text that enters the context, and on subagent reports on
  their way to `eika`. A window at or over the threshold is replaced by a
  placeholder and kept in a list the user can read. Rejected: flagging without
  removing, which needs something downstream to act on the flag, and that was the
  `suspicious` label and its asks. Rejected: dropping the whole result, since a
  false positive would then cost a page instead of a paragraph. Rejected: a push per
  removal, which is the same fatigue by another road.
- **The screen fails open by default.** With the guard down, content passes
  wrapped and marked `unscanned`, with a banner and one push. An assistant that
  stops reading the web when a sidecar restarts is worse than one that reads it
  with one layer missing for a minute. `screen_required` is the setting for a user
  who wants the other trade.
- **Labels are gone, with private projects.** A label's only effect was an ask.
  `eika`, the agent with the most private data, has no `bash` or `web_fetch` for a
  label to gate, and for a worker the label meant an approval per command. The
  honest replacement for "this project must not leak" is a rule, a computer of its
  own in allowlist mode, which is in `DEFERRED.md` until someone needs it. This
  also removes `sessions.sensitive`, the project `private` field and the `private`
  plugin flag before any of them was built.
- **Reads, commands and egress never ask.** `web_search`, `web_fetch`, `bash`, file
  tools and connections from a computer are `allow` or `deny`. What a user would be
  shown on such a card (a URL, a command) is not something they can judge in the
  two seconds they would give it.
- **A routine's runs need no grant.** The `routine` scope existed to cover a
  labelled routine's asks. A routine now asks once, when it is created or enabled,
  and its runs follow the same rules as any other run.
- **Memory writes are screened, with no provenance field.** The design looked for
  instruction-shaped words ("always", "ignore", "you must") outside a span quoted
  from the user's message in the same turn. A preference Eika paraphrased, or saved
  a turn later, went pending, and each pending memory is a question to the user; a
  planted instruction that avoided the words went active. The screen is a trained
  classifier for the same question. Credentials are still denied by pattern, and a
  pending memory sits in the Inbox without a push. The phase 5 live suite checks
  that ordinary preferences do not go pending.
- **The guard chunks, and it is a plain service.** Windows are counted in the
  model's tokens, so the tokenizer's owner does the splitting: `POST /screen` takes
  whole texts and returns scores with byte offsets. Rejected: text-embeddings-
  inference as the only server, since it truncates each input to one window and
  would put a ModernBERT tokenizer into Core. Its `/predict` shape is still served,
  so it can stand in where it works. CPU by default: it keeps the main GPU's memory
  for the chat model, and a fetch waits longer on the network than on the screen.
- **What this leaves open is written down, and measured.** A worker that reads a
  page with `curl`, a planted instruction the screen misses (about half on
  InjecAgent, more on BIPIA), and an injected `eika` that briefs a researcher to
  fetch a URL with private data in it. `design/sentry.md` 6 lists them,
  `evals/injection` counts them with the screen on and off, and `DEFERRED.md` has
  the rules to add if the counts are bad. None of them is an ask.
- **An ask stores the call and does not stop the run** (2026-11-13). A parked call
  held a run for up to a day and died with a restart, and a routine had nobody
  to answer it. The approval now holds the whole call, Core runs it as stored
  when the user confirms, and the model is told only that it is queued. Rejected:
  asking the model again after approval, since what runs must be what the user
  read.
- **Plugin tools ask once per destination, not once per call** (2026-11-13). The
  list of what asks stays closed at two. A tool with a destination argument is
  `known` by default: a recipient the user named in the conversation, or has
  approved before, does not ask. The cost is that an injected message to someone
  the user already writes to goes out, and the journal is where it shows.
  Rejected: an outbox delay on every send, which slows every honest send to
  cover a rare one.
- **Tripwires act on chains, not events** (2026-11-13). Reads, commands and egress
  still never ask, so something has to watch them. A single event (a `.env` read,
  an upload, a link followed from a page) is ordinary work and alerting on it is
  fatigue again. Sentry records each as a mark and acts only when a source,
  access and a sink join on one run: it blocks that action, holds that run, and
  sends one push. Holding is a state of a run made by its own actions, not a
  label on a session, and it ends with the run. A tripwire ships only if it
  fires on no honest session in `evals/injection`. Rejected: default-allowlist
  egress and a budget on composed fetch URLs, which stop the same attacks by
  refusing honest work every day. Rejected: a model as the monitor on every
  run, for the latency and because text in the trajectory can steer it.
- **File changes are checkpointed, not gated** (2026-11-13). A shadow git commit
  per run makes a destructive command recoverable without a question.
- **Approvals, grants and the Inbox's cards move to the phases that first use
  them**: phase 11 for approvals, phase 5 for the Inbox page. Phase 4 would
  otherwise have built an approval flow with no caller and tested it with a
  test-only tool.
- **The plan's log is its own file**, `PLAN_LOG.md`, and closed phases are a
  paragraph in the plan. Every agent that starts an item reads the plan, and most
  of it had become history.

## The coding layer is removed

- **Phase 0 of `PLAN.md` removed everything that made Eika a coding
  harness.** In the UI: the files, terminal, changes, workspaces and
  projects features, the session tree panel and fork UI, the diff libraries,
  Monaco and xterm, and the sidebar of projects and workspaces; the sidebar
  lists chats. On the server: the git layer, worktrees, subagents, previews,
  the terminal and eikad `/pty`, context-file discovery, and projects. Each
  has its own entry below (Sandboxes, Git, Tools, Subagents). The reason is
  the scope: an assistant is talked to, and its agent is the only thing that
  uses a computer, so every way for a person to work in one by hand is
  surface to defend in an install that holds private data, for a use it does
  not have. Rejected: keeping the coding layer as a second mode beside the
  assistant.
- **What stayed is the sandbox work** (computers, eikad, the egress proxy,
  sealed secrets, the MCP client, search) and the loop and session tree.
  The API serves neither a computer's file routes nor a session's fork route.
  The coding UI that called them is gone, and an agent reaches a computer's
  files through the executor, which stays. A session's branches stay (the
  head, the entries, the outline); only copying a branch into a new session
  is gone, and with it the `fork` session kind and
  `sessions.parent_session_id`, dropped by migration `0002`.
- **The computer routes no UI calls are gone too.** Only `GET /api/computers`
  stays, which the sidebar reads, and `POST /api/sessions` still takes a
  `computer_id`. Create, rename, pin, archive, start, stop, delete, set the
  sandbox and usage had no caller once `home` and the Computer settings
  existed. `computers.pinned` and `computers.archived` went with them
  (migration `0003`), and so did the usage sampler, `Host.Usage`, which no
  plan item uses. A computer is made by the harness, stopped by its idle
  stopper, and given its limits by the settings. A project's own computer
  (phase 2) brings back whatever route it needs.
- What went is recoverable: `PLAN_LOG.md` names the commit
  (`478ce9c`) that last had the subagent spawner and the preview ticket flow,
  and says which parts phases 2 and 12 reuse.

## Stack and dependencies

- Go 1.26 with stdlib `net/http` routing and `log/slog`; Vite, React 19,
  strict TypeScript, Tailwind v4, shadcn/ui. Frontend dependencies are pinned
  exactly.
- `openai-go/v3`: official SDK. Only Chat Completions, because every
  "OpenAI-compatible" endpoint implements it and few implement Responses.
- `pgx/v5`, no ORM. Migrations are a 40-line migrator over embedded
  `NNNN_name.sql` files under an advisory lock; golang-migrate would be a
  dependency for less.
- `docker/docker/client`, the official Engine client, over hand-rolling the
  socket protocol.
- `coder/websocket` (context-aware, maintained). The eikad watcher polls instead of adding
  fsnotify: trees are small and polling behaves the same on bind mounts.
- `golang.org/x/net/html`: the standard library has no HTML parser; a
  Markdown library would still need our container and sanitising rules.
- `golang.org/x/image`, the Go project's own supplement to the standard
  library, for the Catmull-Rom resampler (`image/draw` has none) and the
  WebP decoder images are attached in.
- `dop251/goja`, imported by eikad alone, for web_fetch filters: the model's
  regular expressions need a real JavaScript engine. The harness never links it.
- MCP without an SDK: the official one brings oauth2 and a schema library
  and still leaves transport-in-sandbox, sealed storage, and UI to us.
- Frontend: `react-router`, `zustand`, `react-markdown` + `remark-gfm` +
  `rehype-highlight`, `cmdk`. The resizable split is ~80 lines of our own,
  and so is `remarkLineBreaks` (~30), which keeps the newlines in a user's
  markdown message where `remark-breaks` would be a dependency. `lowlight`,
  which `rehype-highlight` already brings, is a direct dependency so that the
  composer colours code with the same grammars as the transcript.
- `staticcheck` and `goimports` pinned by `tool` directives in `go.mod`.
- Deployment config is YAML (`gopkg.in/yaml.v3`) plus `EIKA_*` env, with the
  compose stack's values as defaults. Everything a user chooses is a
  database row edited in the UI; a fresh stack needs no `.env`.

## Sandboxes

- **Computers, not workspaces.** A sandbox is Eika's own machine, with a
  disk that outlives its container, not a checkout of a repository made for
  one task. "Workspace" carried the second meaning (a branch, a base commit,
  a project) everywhere it appeared, and "project" returns in phase 2 for
  something else (a home for related sessions), so the word was freed
  rather than reused. The rename was made throughout in one pass, without
  aliases: package `computer`, `/api/computers`, the event `computer.state`
  and topic `computer:<id>`, containers and volumes `eika-cx-<id>`, the label
  `eika.computer`, the setting `computer_idle_minutes`, and `computer_id` on
  the wire. The old label is not recognised, so an old install's containers
  are not adopted. The default computer is `home` (below).
- **Every agent action on files or processes runs in the computer
  container** through `executor.Executor`. Network actions (model calls,
  search, fetch, the egress proxy, remote MCP) run in the harness; model
  code (fetch filters) and processes (stdio MCP) run in the sandbox.
- The harness mounts the Docker socket and starts sandboxes as siblings.
  Socket access is root-equivalent; accepted, since there is no other way.
  The harness itself runs as non-root `eika`; the entrypoint joins the
  socket's group (whatever its gid) and drops privileges with `setpriv`.
- **eikad is copied into each container** (`CopyToContainer`) before start.
  A bind mount source resolves on the host, not in the harness container, and
  a shared volume would need a helper container. Any image then works.
- Confinement: Docker init as PID 1, all capabilities dropped,
  `no-new-privileges`, uid 1000 by default.
- **`/process` is not an executor call.** It goes through
  `Computers`/`Host`, so a tool can run commands but never hold a
  process.
- Sandboxes are on `eika_sandbox`, which only the harness also joins, so a
  sandbox cannot reach postgres or searxng. An empty `sandbox_network`
  publishes eikad on loopback for `make dev`.
- **The sandbox config is a row** (`computers.sandbox`): desired state,
  applied at create, when the Computer settings are saved (to `home`'s row),
  and on every start, so partial applies converge. Default: 4096 processes, nothing else.
- **Limits change live** with `ContainerUpdate`: recreating would reset
  everything outside the volume at `/home/eika`. Docker cannot lift a CPU or memory limit,
  so "no limit" on an existing container means the whole host. Swap equals
  memory.
- **Egress is enforced by the network; the proxy is the one door.** A
  restricted sandbox moves to the internal network `eika_sandbox_internal`,
  whose only way out is the harness proxy on `:3128`. A proxy alone is
  advisory; DNS filtering leaks by address; iptables needs a capability the
  sandbox never gets. The proxy checks the allowlist per request and dials
  through `netguard`.
- The proxy identifies a sandbox by its computer id and a proxy token of its
  own, generated at create and kept in the container's configuration:
  per-computer, secret, and recoverable after restart, so no grant table. It
  was the hub token until the hub was due to go, and it stays.
- Proxy variables go through eikad's `PUT /environment`, because a
  container's env is immutable and egress can change while it runs. Open
  sandboxes get no proxy, so they keep private addresses.
- **Previews, the terminal and eikad `/pty` are gone.** They let a person
  work in a sandbox by hand, which a coding harness needs and an assistant
  does not: the agent is the only thing that uses a computer, and the user
  talks to it. With them went the sandbox's forwarded `ports` and the
  `creack/pty` dependency. The one-time-ticket-to-host-cookie flow
  (a separate origin so the page cannot read the UI token, every request
  checked against the row) is in history for phase 12's live view of the
  browser.
- **Idle computers stop after `computer_idle_minutes` (default 15)
  without a run.** A run is what a sandbox is for; an open browser tab would
  keep a forgotten sandbox up forever. One harness-wide setting, not a
  per-computer column: nothing yet needs two
  timeouts. Last use lives in memory rather than a row, so a
  restart only ever delays a stop. A later run starts the computer again
  (see `home`, below). Rejected: a timer in eikad, which sees processes, not
  runs.
- One address guard, `internal/netguard`, for web_fetch and the proxy.

### The home computer

- **`home` is ensured on every start, not created by setup.** `POST
  /api/auth/setup` is one path to a usable install, and not the only one: a
  deployment with a fixed `EIKA_AUTH_TOKEN` never calls it, and a restart
  must find the computer it made. `Server.EnsureHome` does the same work
  whichever way the harness got here: find the row, make the container if it
  is missing, start it if it is stopped. Rejected: creating it in the setup
  handler, which would be a second code path with its own half-finished
  states.
- **The database decides which computer is home.** `computers.home` with a
  unique index `WHERE home` allows one row. The row is inserted before the
  container, so two harnesses starting at once, or a start that follows a
  failed one, agree on one id, and with it on one container name and one
  volume. Rejected: finding it by name, which the user can change, and a lock
  in the process, which two processes do not share.
- **It never blocks start-up.** `Server.Run` ensures it beside the API and
  tries again, after 5 seconds and then longer, up to a minute, until it
  works. Docker not yet reachable or the sandbox image not yet built is the
  expected cause on a first start. The user sees the computer listed as
  `creating`, and `GET /api/system`, which the guided setup shows, names what
  is missing.
- **Nothing deletes its files.** The API has no route that deletes a
  computer. A volume that exists when `Host.Create` runs is reattached, and a `Create`
  that fails does not remove a volume it found, so a container lost to
  `docker rm` comes back over its files, and a failed attempt (the image
  missing) cannot take them. Nothing else recreates a container: limits and
  egress change the running one (`ContainerUpdate`, network connect), and a
  harness restart leaves containers alone.
- **The Computer settings are `home`'s limits and egress.** Saving
  `sandbox_limits` or `sandbox_egress` records them on the home row and
  confines its container, running or stopped, without a restart
  (`applyHomeSandbox`). `EnsureHome` makes `home` before anyone can reach
  Settings, so settings that only said what a computer made later would get
  were a tab that did nothing. The table is written first and then the row,
  and a container that refuses the update is a `500` with the values kept:
  the next start confines it from the row. Before the row exists, a save
  only stores the setting, and `EnsureHome` reads it. Rejected: applying
  before saving, which would lose the user's values when Docker fails.
- **The image is not applied to an existing `home`.** `sandbox_image` is read
  when the container is created, and nothing replaces a container, so `home`
  stays on the image it was created from until its container goes missing,
  and a sandbox image upgrade does not reach it. The Settings text says so.
  Deferred: replacing the container over its volume.
- **A run starts a stopped computer.** The idle stopper is the only thing
  that stops one, and the next run, which needs it, starts it
  (`runExecutor`). Nothing in the API stops or starts a computer, so a stopped
  one is never a stop anyone asked for. A run in `home` whose container is
  gone calls `EnsureHome`. A computer that cannot be started refuses the run
  with a `409` naming why. Rejected: starting at the first `bash` call, which
  would put a lazy start inside the executor every tool shares, for a computer
  the run is about to use anyway.
- **The root is `/home/eika`.** It is the user's home directory and the
  volume's mount point (`eikad.DefaultRoot`, the one Go constant; the
  Dockerfile spells it three times and a test holds them equal). A volume
  keeps what the image held at its mount point from its first creation, so
  the image installs nothing there: Go's `GOPATH` is `/opt/go`, writable by
  `eika`. Everything else the image installs is already outside it (Node and
  Go under `/usr/local`).
- **Any image gets a root the user can write.** A volume over a directory the
  image lacks is owned by root, which the user could read and never write.
  `Host.Create` has the daemon extract the root directory, owned by the
  numeric uid and gid of `Spec.User`, into the container before it starts,
  and sets `HOME` to the root. A user given by name is left to its image.
- **Until phase 2 replaces chats, a chat attaches to `home` from the
  sidebar.** `POST /api/sessions` already took a `computer_id`; the sidebar
  gained a button that opens a session on `home`, and lists those with the
  chats. The UI cannot start any other computer's session.

## Git

- **The git layer is gone.** Eika no longer clones, pushes, fetches or
  mirrors, has no hub (`internal/workspace/hub`, `/git/`, `EIKA_HUB_*`), keeps
  no remote credentials, and has no merge, diff, commit or push routes,
  `session_entries.commit_sha`, or fork-with-workspace. Eika is an assistant,
  not a coding harness, and sandboxes become its computers (phase 0 of
  `PLAN.md`).
- Nothing replaced the hub. A computer holds only its own volume.
- **Projects are gone, and a computer stands alone.** The `projects` table
  held a host directory and a git remote, which nothing needs once the git
  layer is gone and sandboxes become computers. With it went the computer's
  `project_id`, `branch`, `base_commit` and `parent_workspace_id` (nothing set
  the last), the `/api/projects` routes, host-directory bind mounts
  (`Spec.HostPath`), and the project count in `GET /api/system`. A project
  carried no ownership: the harness has one user, and the sign-in token is the
  only gate to a computer, so there was nothing to move onto the computer.
  "Project" comes back in phase 2 with a new meaning.
- **A computer's volume is mounted at its root.** The files live in the
  volume rather than the container's writable layer, and `Inspect` reports the
  volume from the container's mounts, so a restarted harness can remove it.
- What is left of git is inside the sandbox, for whatever an agent runs in
  bash. The `git` binary stays in the Core image for phase 5's memory
  repository.
- The data volume is `eika-data` and holds only the sealing key.

## Persistence and sessions

- One id generator, `store.NewID` (20 base32 chars), generated before the
  row so one id names a row, container, volume, and URL.
- **One provider message is one entry.** An assistant message with tool
  calls stays one entry, so the tree never gains a branch point the loop
  cannot resume from.
- Entry kinds `user`, `assistant`, `tool_call`, `tool_result`, `system`,
  `event` are fixed vocabulary shared with the frontend.
- **Only a resumable entry can be a head**: one whose path leaves no tool
  call unanswered. `session.PathResumable` is the one rule.
- `sessions.kind` (today only `user`) is a column, not a join, because it is
  what the session is.
- A chat is a session with `computer_id` NULL: same tree, run manager, and
  view. Created explicitly (`chat: true`), and a computer's sessions cascade
  with it rather than becoming chats.
- **Fresh baseline schema.** Turning Eika into an assistant dropped most of
  the coding schema (projects, worktrees, subagents, commit SHAs) and renamed
  the table at its centre, so its 17 migrations were squashed into
  `0001_baseline.sql` and no database is migrated: a deployment starts fresh,
  and the README gives the reset. Phase 0 edited the baseline in place as each
  removal landed, which is also why a database made part-way through cannot
  be kept (the migrator records only a file's name). With phase 0 closed it is
  frozen, and migrations are append-only again; `0002` is the first, and
  removes session forks. Rejected:
  a migration from the old schema, which would be code that runs once, for a
  deployment with one user.
- **Schema `core`.** Core's tables live in their own schema rather than
  `public`, so a schema of Sentry's can sit beside it in the same database with
  its own grants, if Sentry becomes a process of its own (`DEFERRED.md`).
- Every model call is a `model_calls` row holding the system prompt
  sections, schemas, and parameters; messages are not copied, since they are
  the session path.
- A cut-off response (`length`, `content_filter`) is kept and ends the turn,
  its tool calls dropped. Rolling back would delete the user's own message
  and make the model apologise for a turn it did take.
- Malformed tool arguments keep their exact text, quoted, with
  `arguments_malformed: true`, so the entry is always writable and the tool
  reports a normal argument error.

## Phase 2 upgrade policy

- **Upgrade the existing database in place.** Migrations are forward-only and
  append-only. Migration 0010 is the current last migration. The schedule reserves
  0011 through 0017 for later packages, but each package must check the current
  sequence after its predecessor merges before it chooses a number. No package
  resets the database or rewrites an applied migration.
- **Keep old conversations readable.** Add the new session and entry shapes before
  moving their consumers. Preserve existing session IDs, entry trees, content and
  computer associations, including null `computer_id`. Old chats do not become the
  new home conversation by implication. Retire creation and old fields only after
  the UI and API callers move, with a populated upgrade test that reads the old
  history. Do not delete old chats to satisfy a new constraint.
- **Preserve profile settings through the transition.** Keep profile rows and the
  existing resolution path while callers still use them. Before removing them,
  explicitly migrate or retain their user-set values so they remain recoverable;
  do not silently replace them with agent defaults or discard them. Test an upgrade
  with multiple populated profiles and session overrides. Agent definitions have
  fixed capabilities, so old tool selections do not grant new agents MCP access.
- **Ensure home on start.** Find it by owner and name and create it idempotently,
  without waiting for Docker. Existing conversations remain separate. This also
  covers installations that completed setup before the home-session feature.

## Agent loop and providers

- **Retries live in the agent loop**; the SDK's are off
  (`WithMaxRetries(0)`), so the fake provider exercises the same path.
  Retryable: 408, 409, 429, 5xx, transport errors.
- **The context window is judged by the token counter.** The next request's
  size is the session's counter's prediction ("The token counter", "Model
  calls"): the endpoint's own count where it renders requests, and otherwise
  the newest measurement plus an estimate of what was added since (Pi's rule,
  which is what `estimated` is). A request whose prediction reaches the window
  fails before the call; one that fits but leaves less than the requested max
  output gets max output lowered to what is left, since an endpoint refuses
  input plus max output over its window. The earlier byte bound (a token per
  byte) failed sessions at a quarter of their window, and the 4-byte estimate
  of the rest of the request was 30% off for code and JSON.
- Reasoning is always streamed as `reasoning.delta`; `preserve_thinking`
  only controls replaying it to the endpoint. Tying the two would force
  users who want to watch reasoning to send it to APIs that reject it.
  `preserve_thinking` is off for new models because the OpenAI API rejects it.
- **Reasoning efforts belong to the model row** (`reasoning_efforts`):
  endpoints disagree on the vocabulary; the harness checks only the shape.
- **`none` turns thinking off** in the one field `models.thinking_switch`
  names (`reasoning_effort`, `chat_template_kwargs`, or `thinking`), because
  no single request fits every server and some reject unknown fields.
  Trying fields in turn on a 400 was rejected: it doubles failures and hides
  the misconfiguration.
- **Context and speed are measured, never estimated from text.** `context`
  is the last call's usage (what fills the window), `usage` the turn's sum.
  Speed is two phases (prompt, generation) as token/duration pairs, from the
  endpoint's own fields when it has them, else generation timed from the
  first streamed token. The prompt phase is never harness-timed: prefill is
  indistinguishable from queueing.
- **Context is assembled in one place** (`agent/context.go`), so the
  preview uses the same request assembly. The context breakdown counts parts
  separately and states the template remainder without scaling ("Context
  breakdown").
- No package reads provider keys from the environment; the OpenAI provider
  sets key and base URL explicitly so `OPENAI_*` never applies. A key belongs
  to its base URL: changing the URL clears it, and probes elsewhere never
  carry it.
- Providers are built per use and never cached, so a changed key applies
  to the next run and a key is in memory only while used.
- Model names are unique across providers, because runs name a model
  alone.

### Tool-call ids

- **The loop mints an id for every tool call and keeps the backend's for the
  wire only.** Local servers reuse ids: llama.cpp can answer `call_0` in every
  response, a server may send none, a Responses server counts per response.
  Entries, events, the resume rule (`session.PathResumable`) and, from phase
  4, approvals and the dispatched marker are keyed by the call, so an id that
  two calls share corrupts them: two parallel calls with the same empty id
  were one pending call to the resume rule. `ToolCall.ID` is now Eika's
  (`call_` and 24 hex digits, from `crypto/rand`) and `ToolCall.WireID` is the
  backend's, set by the loop when a response's call arrives
  (`agent.mintCall`). A tool result names its call by Eika's id; a provider
  translates it with `provider.WireToolCallIDs` when it serialises, so the
  result of a call carries the id the backend gave the call.
- **Replay is byte for byte.** A request sends the backend's id exactly as it
  came, from the first request that carries the call to every one after, so
  the serialised prefix does not change when a call is replayed and exact
  counting sees no drift from it. A backend that gave none (`WireID` is the
  empty string) is sent Eika's id instead, the same on every replay: an empty
  `call_id` is invalid under Responses and ambiguous under chat when two
  calls share it. `WireID` is a pointer so that "the backend sent nothing"
  (`""`) and "stored before ids were minted" (absent) differ.
- **Old entries stay as they are.** A stored call with no `wire_id` has one id
  that is both, and is replayed under it, empty included. Nothing rewrites
  history, because a rewritten id is a changed prefix.
- **A session may change protocol.** Both providers read the same
  `ToolCall`, so a conversation stored under `chat` replays under `responses`
  (`call_id` is the wire id) and the reverse; neither protocol's item id
  (`fc_...`) is stored or sent, since the request is stateless.
- Rejected: replacing the backend's id at the provider, so that the loop never
  sees a collision (the provider cannot see the other responses of a session,
  so it cannot make an id unique in one); a counter per session (it restarts
  with a restart, and a mint must not depend on state that does not persist);
  rewriting the ids of old entries (above).

### The context block

- **Every user-side message ends with a `<context>` block that states the time
  to the minute, written once when the message is first sent.**
  `Message.Context` holds it, stored with the message, and a request joins it
  to the message's text with a blank line (`Message.ModelText`). The block is
  `<context>`, one `name: value` line per field in a fixed order, `</context>`,
  so the channel (phase 2) and the notices (phase 2 and later) join as more
  lines and nothing about the shape changes: `time: Sat 2026-10-31 14:05 +00:00
  (UTC)` today. A message that already has its block keeps it: a message read
  back from the store, or given back to its queue by a turn that failed, is
  never stamped again.
- **Why stored, and why apart from the text.** The time is read once and
  written once. A block computed when a request is built would give every
  earlier user message a different time on every call, the serialised prefix
  would change at the first one, and the whole prompt cache would go with it;
  a resumed session would send different bytes from the session it resumed.
  The agent test that moves the clock between turns and across a restart and
  asserts each request is a byte-prefix of the next is the proof. It is kept
  out of `Content` because everything that reads `Content` (the transcript,
  the turn's start event, the queue's display, the title, the transcript form
  of a summary) means what the user wrote, and each would otherwise have to
  strip a block of its own. Rejected: the block inside `Content` with the UI
  and the title stripping it (one missed reader shows it to the user or puts
  a clock in a title); the block built by the provider at send time (the
  prefix problem above).
- **When a message is stamped.** At the first point the loop holds it: `Run`
  for the message it starts with, `Steer` and `FollowUp` for the ones queued.
  A queued message states the minute the user sent it, not the minute a turn
  picked it up, which may be a long tool call later. A tool result is not a
  user-side message and carries no block, and nor does the summary a
  compaction leaves. The images-in-tool-results message of the design (phase
  2) is a user message the harness writes after a tool result; it will be
  stamped like any other.
- **The zone is UTC until the preamble exists.** The design puts the user's
  time zone and locale in the preamble entry (phase 2), and the zone the block
  states its time in is that zone. Nothing records the user's zone before then:
  the browser knows it, but no setting carries it, and a setting for one value
  the preamble item would replace is the speculative option the style guide
  forbids. So `Options.Location` is UTC by default, the block states the
  zone's name and offset beside the time, so the model never reads a UTC time
  as local, and the preamble item passes the stored zone. Messages already
  stored keep the zone they were written in. Rejected: the harness
  container's local zone (a container's zone is UTC unless someone set it, and
  says nothing of the user); a guess from the browser at each message (a
  zone that can change between messages is a changed prefix, and the block
  would disagree with the preamble).
- **Counting.** The block is part of what is sent, so the counter counts it:
  the endpoint's own rendering sees it under `exact`, and the census of the
  anchored and estimated methods, the context breakdown and the part sizes
  read `ModelText`. The Context inspector shows the block under its message.
- **Compaction.** The cached form of a summary request sends the messages as
  they were, blocks included, and its prompts say what a block is: not the
  user's words, never quoted, useful for dating what happened. The transcript
  form is written by the harness from `Content` and holds no block. Rejected:
  stripping the blocks from the cached form (it is the cached prefix; changing
  it costs the cache the form exists for).

### Prefix stability

- **A request is sent as the same bytes every time.** A server's prompt cache is
  keyed by what it reads, so what the harness sends of the conversation must be
  the same bytes on every call. The Responses provider already encoded its
  fields in sorted order. The Chat provider did not: the SDK writes the fields
  it takes from a map (`SetExtraFields`: `top_k`, `min_p`, `chat_template_kwargs`,
  `thinking`, `preserve_thinking`, `id_slot`) in whatever order the map is
  walked, so one request went out in several byte layouts and a test that
  compared two encodings failed about half the time. No server reads key order
  and the rendered prompt did not change, so it cost nothing in the cache, but
  "deterministic" is a claim the wire has to make true. `withExtraFields` now
  serialises the SDK's own fields and appends the extras in the order of their
  names, so `ChatBody` and `Stream` produce identical bytes (the latter with
  `stream` at its end), under both protocols. Rejected: replacing the SDK for
  the chat provider (the encoding of messages, tools and images is the part it
  does right); comparing decoded documents instead (the byte comparison is the
  invariant, a decoded one hides any later reordering); sending the extras one
  `WithJSONSet` at a time (the counter's `ChatBody` could not share that
  path).
- **A message comes back from its entry as the bytes that were sent.** The
  prefix-stability test (`internal/session/prefix_test.go`) found that it did
  not, at the start of every run, because the server loads the session from the
  database each time: the arguments of every earlier tool call, and every
  reasoning item, were a different prefix from the one the server had cached.
  Two causes. `encoding/json` compacts a `RawMessage` and writes `<`, `>` and
  `&` as `<`, `>` and `&`, so `{"command": "a && b"}` was stored as
  `{"command":"a && b"}`; and the entry's payload was `jsonb`, which
  writes a document back with its keys ordered by length and a space after
  every colon, so even `{"path":"/tmp"}` came back as `{"path": "/tmp"}`. Now a
  tool call whose arguments `encoding/json` would not write back unchanged also
  stores them as `arguments_text`, a string, which comes back exactly (a reader
  still sees `arguments` as a value); a reasoning item that would change is
  stored as a string of its text (`provider.ReasoningItems`); and migration
  `0010` makes the payload column `json`, which keeps the text it was given.
  Nothing queries inside a payload. Rejected: `arguments_text` for every call
  (the common call is compact and stable, and arguments can be a whole file);
  turning arguments into a string everywhere (the API and the UI read an
  object); escaping nothing by marshalling with `SetEscapeHTML(false)` (the
  compaction remains, and every writer of a message would need it); keeping
  `jsonb` and sorting keys at send time (the model's order is what it produced
  and what the cache holds).
- **The test.** It runs the real loop and both real providers against a fake
  server that keeps the raw body of every request, through a scripted
  conversation: parallel tool calls that share a backend id, reasoning, an
  image, a steering message and a follow-up joining mid-run, a retry after a
  500, a stall, a response cut off at the limit, a refused call, an interrupted
  run and its resume, three restarts, a manual compaction, and a clock that
  moves throughout. The session is stored and loaded at the start of every run,
  in a store that writes entries as JSON and in PostgreSQL. Each request's
  `messages` (chat) or `input` (responses) must begin with the previous
  request's array byte for byte, and everything else it carries (system prompt,
  tools, template kwargs, sampling) must be the same bytes, except the output
  limit, which `fit` lowers as the window fills: no server reads it as part of
  the prompt, and the fake template ignores it (the live scenario
  `prefix_across_compaction` shows llama.cpp's cache ignores it too). A failed
  attempt and a refused call extend the last accepted request, and a retry
  repeats its failed attempt. Across a compaction the array is replaced by
  design; the summary request shares the previous request's prefix, and the
  chain holds again from the first request after it. A second check renders
  each request with the fake server's template and requires the same of the
  prompt, and requires the prompt the token counter had rendered to be the
  prompt the call reads (under `responses`, the server's conversion of the
  input). Rejected: comparing the values the providers were given (the
  violations above were all in the encoding between them and the wire);
  normalising the minted tool-call ids (a replay must keep them, and they are
  what the test is for); an in-memory store alone (it cannot show a column).

### Loop limits

- **A run is bounded in model calls and in wall time.** `Options.Limits` holds
  `MaxCalls` (default 40) and `MaxWall` (default 24 hours). 40 is the home
  session's limit in `runtime.md` section 5; the design gives a subagent 200, and
  the agent definitions of phase 2 set each agent's own. Until they exist every
  session is a chat on one computer, which is the home session's case, so 40 is
  the one default. A run is one `Agent.Run`, however many turns its follow-ups
  start. A model call is a step of the loop: a retried attempt is the same call,
  and the summary a compaction asks for is not one, since neither is the
  model's own choice to go on.
- **Hitting a limit is a clean end, not a failure.** The check runs at the top
  of the loop, before the next model call and before steering is taken from its
  queue: every tool call of the last response has been answered and stored, the
  conversation ends on a tool result, and the run returns `nil`. It ends its
  last turn with `turn.end` and the stop reason `max_calls` or `max_wall_time`;
  the transcript says which limit and that a message continues the work
  (`docs/api/events.md`). A message sent next starts a run with a fresh count,
  from the stopped state. Messages still queued stay queued for it (the same
  hand-over an abort makes). A run that has made no call is never stopped,
  because the message it began with is not stored until the first call has
  read it. The reason is on the live event, as `length` is; it is not stored
  with the session, and the phase 2 `notice` entry kind is where a stored one
  belongs. Rejected: failing the run with `run.error` (it reads as a fault and
  the UI offers a retry of what is not a retry); cancelling the run's context
  at the wall time (a tool killed halfway is an abort, with an unknown outcome,
  and the limit exists to stop a loop, not a command); one more call to ask the
  model to wrap up (a model that is looping does not stop looping because it is
  asked).
- **An identical call is warned about, never stopped.** From the third
  identical consecutive tool call of a run, the result the model reads ends with
  a warning that says how many times the call has been made in a row and asks
  for a different approach. "Identical" is the same tool and the same
  arguments as JSON: spacing and key order do not make a call different, and
  arguments that are not JSON are compared as written. Consecutive means in the
  order the run made them; any other call in between starts the count again.
  The result keeps the tool's own text first, the event a client shows carries
  the same text, and the call still runs: a poll that repeats on purpose
  (waiting for a build) is not broken by it. Rejected: stopping at some count
  (a legitimate poll would be stopped; see
  [Architecture](ARCHITECTURE.md#context-layout-and-prefix-stability)); a
  separate message beside the result (a notice the model might read as the
  user's).

### The stall timeout

- **An attempt that receives no bytes for as long as it is allowed is aborted
  and retried.** The allowance is `Limits.Stall` (30 s) once the response has
  produced a token, and before the first token it is `30 s + predicted_prefilled
  / prefill_rate x 2`, since 200k tokens of dense prefill take minutes and a
  limit that fires in the middle of one throws the whole prefill away and
  starts it over. The abort cancels the attempt's context with a cause, the
  attempt fails as a retryable `provider.Error` that wraps `agent.ErrStalled`,
  and the loop's ordinary retry path does the rest: the lease is released
  (`stream` gives it back on every way out, so a more urgent call can take the
  slot during the backoff), `message.reset` is emitted, one retry of the budget
  is spent, and a run that stalls every time fails with a retryable
  `run.error`.
- **The clock starts when the lease is granted.** The wait in the queue is the
  queue's, bounded by aging and the user's patience, and a call that waited
  five minutes behind another session has not stalled. The attempt includes
  the provider's request and the wait for the response's headers.
- **Any bytes are progress.** Provider events are not enough: a server reading
  a long prompt may send only SSE comments (`: keep-alive`), and a chunk with
  nothing in it becomes no event. `provider.WithProgress` puts a function in
  the context, and the client every provider shares (`NewHTTPClient`) calls it
  when the response arrives and for each read of its body that returns bytes,
  under either API. Reasoning, tool-call fragments and usage count like text.
  The first token (text, reasoning or a tool-call fragment) ends the prefill
  allowance. Rejected: counting only provider events (a server that is alive
  would be killed); a second reader of the body in the agent (the provider
  already reads it).
- **Nothing is assumed cached.** `predicted_prefilled` is the predicted prompt
  in full. What is cached is a guess until the response says so (the server may
  have evicted the prefix, and the slot book of the queue records what a slot
  held, not what it still does), and a limit that is too long costs a wait for a
  server that really has hung while one that is too short repeats a whole
  prefill. The cost is that a hung server is noticed after minutes at 128k, not
  seconds.
- **One prefill rate.** The default is the queue's: `inference.DefaultPrefillRate`
  (2000 a second; the bench read 66002 tokens in 22.8 s, 2894 a second), which
  slot affinity already prices a prefix with, read through the queue's policy
  so a changed assumption changes both. When the endpoint has recorded calls,
  the measured rate replaces it for the run: `store.PrefillRate` sums
  `prefilled_tokens` and `prefill_ms` over the endpoint's newest 20 calls that
  read at least 500 tokens (a short prompt is mostly the call's fixed cost),
  read once when the run is built (`server.prefillRate`). A server slower than
  the default would otherwise be killed during real prefills until a
  measurement existed. Rejected: a second constant in the agent; a rate
  learned only inside the run (every run would start from the default, which
  is where the risk is).

### The provider probe

- **A probe reads each server's own description, then verifies the counting
  routes with one tiny request.** `provider/probe` identifies llama.cpp
  (`/props`), SGLang (`/get_server_info`), TabbyAPI (`/v1/model`) and vLLM
  (`/version`), in that order, since SGLang answers `/version` too. What no
  one claims is a hosted API (`openai`) when its address is not on the user's
  own network and `other` when it is. That judgement only picks the default
  slot count, 8 or 1. Rejected: asking the user to pick a backend before
  anything works (the probe is the connection test already); identifying by the
  `owned_by` field of `/v1/models` (llama.cpp says `llamacpp`, vLLM `vllm`, but
  a proxy rewrites it and the other servers do not set it).
- **`probe.Run` never sends a prompt to a model.** It reads descriptions and
  posts `{"content": "hi"}` to a tokenizer. Rejected: a one-token completion to
  read `cached_tokens` and the usage shape, which costs a model call on every
  start for a fact the usage item learns from real calls. `cache_metrics` is
  `reported` where verified (llama.cpp) and `unknown` elsewhere. The Responses
  conformance check does ask the model, as its own entry point and only when
  needed ("The Responses API"), and so does the slot affinity check ("Slot
  affinity"), which `capabilities.slot_hints` (the server takes the field) does
  not stand for.
- **llama.cpp's cache is `unknown` with more than one slot.** On b11942 nothing
  states whether `--kv-unified` is on or what `-c` was: `/props`, `/slots` and
  `/v1/models` all give the per-slot context, which a unified server sets to the
  whole cache and a partitioned one to `-c` divided by `-np`, so 2 slots of
  131072 read the same either way. Guessing would admit calls into a cache that
  fails the request when full (llama.cpp does not preempt), or give up the
  parallelism of a unified one. So the probe says `unknown`, the settings say why
  and ask, and the queue treats `unknown` as shared with the window as its
  size, which cannot overfill either kind. One slot is `shared` with size
  `n_ctx`: it owns the whole cache in both modes. Rejected: a `--kv-unified`
  default (it holds only when `-np` was automatic, which `total_slots` does not
  show); counting two concurrent requests near the limit (a live test, not a
  probe: it takes minutes of the GPU); reading the server's command line over
  SSH (the harness has no business on the host).
- **Overrides are recorded by name, and the probe checks them in SQL.** A
  provider's `backend`, `slots` and `kv` columns hold the value in force, and
  `overridden` lists the ones the user set. `RecordProbe` writes each probed
  column unless it is listed, in one `UPDATE`, so an override saved while a
  probe runs is not lost. `pool` is the user's alone: empty means the provider's
  name, so renaming a provider moves its default pool. A field is handed back
  with `reset` and then probed. Rejected: separate `probed_*` and `override_*`
  columns (every reader would pick between them, and the queue would have to);
  clearing overrides when the base URL changes (they describe hardware the user
  knows, and are one click to reset).
- **A failed probe keeps the earlier result.** `probe_error` records why and
  `probed_at` is the last success. The probe at start runs beside the server in
  `Run`, one goroutine per provider, so an unreachable endpoint delays nothing
  and a Core restarted while the GPU box is down comes up with last time's
  settings. The probe stops after the first request that gets no answer at all.
  A form that tries a new address or a new key writes nothing: what it learns is
  about something that is not the stored provider.
- **The API field and Responses conformance are not part of this.** The result
  is a struct whose `Capabilities` the Responses item extends; a stored
  provider's `capabilities` is JSON for that reason. They are in "The
  Responses API".

### Usage counts

- **A token count is a number or "not reported", never 0.** `provider.Usage`
  holds `provider.Count`s: a comparable value, JSON `null` when not reported,
  `*int` in the store and the wire types. A server that does not say how many
  prompt tokens were cached has not said none, and a cache-hit rate or a
  prediction drift computed from a 0 would be a number nobody measured.
  Rejected: `*int` fields in `Usage`, whose pointers compare by address and
  break the "same usage as last chunk" check in the stream relay; a parallel
  `Reported` bitset, which can disagree with the value it describes; keeping 0
  and adding a flag only for the new fields, which leaves the old three
  fields reading "stated nothing" as "cost nothing".
- **A sum is not reported if any term was not.** `Count.Plus`, `Usage.Add` and
  `Tally` all work so, which makes a turn's `cached_tokens` null when one of its
  calls did not state it, rather than a smaller number that looks complete.
  Rejected: summing only the calls that stated it and saying so beside the
  number, which every reader of the total would have to remember to check.
- **Each field means one thing, whichever protocol it came from.** The prompt
  count includes the cached part and the completion count includes
  reasoning. This was checked on llama.cpp b11942 under both
  `/v1/chat/completions` and `/v1/responses`
  ([Architecture](ARCHITECTURE.md#model-call-accounting)) and is the documented
  meaning of the OpenAI fields the other backends copy; their fixtures are written from documentation and marked
  unverified. A field of a backend whose meaning is not known is not read: SGLang's
  top-level `reasoning_tokens` is 0 whether or not reasoning was counted, so it
  is left alone. Nothing sums a prompt count and a cached count, and
  `prefilled_tokens` is read only where the server states it.
- **Two fields stating the cached count: the usage one wins.**
  `prompt_tokens_details.cached_tokens` (`input_tokens_details` under
  Responses) is the field the OpenAI API defines and vLLM, SGLang and the cloud
  APIs carry, so a cached count means the same on every backend. llama.cpp's
  `timings.cache_n` is the fallback for a build without the usage field. On
  b11942 the two are equal in every recording.
- **A usage block of zeros states nothing.** Gateways send
  `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}` in chunks before
  the last; no call has an empty prompt, so a block whose three headline counts
  are all 0 is skipped. A 0 beside a real count is a statement and stays.
- **Stored entries before this change stay readable.** A stored usage without a
  `cached_tokens` key predates unreported counts, and three zeros in it mean the
  endpoint stated nothing. In the model call table, `0005` makes the three counts
  nullable, turns rows of three zeros into NULL, and adds `cached_tokens`.
  Rejected: a data migration of the session entries' JSON, which the reader
  makes unnecessary.
- **Reasoning tokens are not estimated here.** llama.cpp states none, so the
  count is not reported. Tokenising the reasoning text is the model call record's
  to add, as a separate, labelled source ("Model calls").

### Model calls

- **One row per model call, named for what it is.** `model_requests` became
  `model_calls` (`0006`), with the routes `GET /api/sessions/{id}/calls` and
  `.../calls/{call_id}` and the Go and TypeScript types renamed with it; a
  request is what is sent, a call is what happened to it. The two counts took
  the design's names (`prompt_tokens`, `completion_tokens`). The table keeps
  `total_tokens`, which the design omits: the server states it, and a column
  of a stated number is not worth dropping. Rejected: keeping the old route
  names behind a new table name (every reader would carry both).
- **The design's `window` is `context_window`**, since `window` is a reserved
  word in SQL, and the model row and the context response already call it so.
- **Prefilled is the server's count, or the prompt less the cached part.**
  `Usage.Prefilled` returns `timings.prompt_n` where stated and otherwise the
  difference when both counts are, which is the same number because the prompt
  includes the cached part (verified, "Usage counts"). It is NULL when the
  cached count is unknown: the prompt alone does not say what was processed.
- **Reasoning tokens carry their source.** `reasoning_source` is `reported`
  (the server's count) or `tokenised` (a count of the reasoning text by the
  endpoint's tokenizer, for a server such as llama.cpp that states none), and
  is NULL exactly when the count is, by a check constraint. A tokenised count
  approximates what was generated, so it is stored beside the reported
  numbers but never in `provider.Usage` or the events, where a reader would
  take it for the server's. Rejected: one column with the best number available
  (the reader cannot tell them apart); a separate column for each source (every
  aggregate would pick one).
- **Columns with no source yet exist and are NULL.** `queue_ms`, `slot`,
  `class`, `priority` and `agent` were added so the inference queue and the
  agents items would add no migration; the queue now fills the first four
  ("The inference queue") and `agent` waits for the agents. `endpoint` is the
  provider's name at the time of the call, as `model` is the model's.
- **One counter per session, held by the server.** A run builds its agent
  afresh for every message, so the counter that learns lives in
  `server/counters.go`, keyed by session and built from the provider row's
  probed `backend` and `capabilities.counting` and the provider's
  `ChatEncoder`. A session moved to another model, address, key or counting
  kind gets a new one, since what it learned described the old endpoint. At
  most 64 are kept, least recently used out, and a deleted session's is
  dropped: a dropped counter costs its session about five calls of learning.
  A provider that is not a `ChatEncoder`, or an endpoint with no capabilities
  yet, still gets a counter, which estimates (and counts text where the
  endpoint can).
- **A held counter holds no key.** The counter counts through a
  `counter.Switch`; the `Routes`, which carry the provider's key, are built
  for a run, attached to the switch for as long as it lasts, and released on
  every way out (`drive`, and `abandon` for a reservation that never became a
  run). What the registry keeps to tell that an endpoint changed is a digest of
  the key inside `endpointPrint`, not the key, and a changed key gives a new
  counter. Users of one session at once share the first one's routes, and the
  last to release drops them. Detached, the counter predicts by estimate. A test
  walks everything reachable from the registry for the key after a run ends.
  The cost is that each run builds its `Routes`, so the control-token check of
  a llama.cpp prompt (two small `/tokenize` requests per distinct token) is
  repeated once per run, not once per session. Rejected: keeping the key in a
  held `Routes` (up to 64 long-lived copies of an unsealed key); sealing it in
  the registry (the harness already opens it for every run, and a digest is
  all that comparing needs).
- **Rejected for the counter's home:** a counter on `agent.Session` (loaded
  afresh each run); one per agent (it would learn nothing across turns);
  persisting the learned constants (the decision of "The token counter"
  stands: they are relearned in a few calls, and the next bullet covers the
  case that mattered).
- **A resumed session is seeded from its stored measurement.** The assistant
  messages of a conversation carry the usage of the call that produced them.
  `Agent.Run` hands the newest to `Counter.Seed` when the counter has learned
  nothing, so the first prediction after a restart on an endpoint that
  counts by bytes is the stored count plus the bytes added since, as the 4-byte
  rule used it, and not the bytes alone. A seed teaches the ratio and the
  anchor and leaves the running error of estimates alone, since nothing was
  predicted. An endpoint that renders requests needs no seed.
- **Predict and Observe see the same request, and counting cannot fail a
  call.** The prediction is made on the request the loop assembles; `fit` may
  then lower its max output, so the counter's request key leaves max output
  out (no template renders it). `Predict` runs under `Options.CountTimeout`
  (ten seconds); when it cannot answer, `Counter.Estimate` gives the estimated
  method's number without a network request, and the record says `estimated`.
  Everything the counter does after the call is bounded the same way. The
  routes honour their context because the counter holds its lock while it
  waits. Rejected: failing the run when counting fails (a model call is worth
  more than its size); running `Predict` in the background and sending without
  it (the prediction decides whether the call is sent at all).
- **An exact prediction that drifted is a warning with numbers.** The loop
  logs `predicted`, `measured` and `drift` with the session, run and model, and
  never the request. The drift is on the stored call for the Context
  inspector, which is the next item's.
- **The compaction reserve widens only for `estimated`.** The reserve grows
  by the running relative error of the counter's estimates times the predicted
  size, and never past half the window, so a poor estimator compacts early
  rather than every call. An `exact` or `anchored` prediction is trusted to its
  number. `fit`'s lowering of max output, the threshold and the window check
  keep their meaning. Rejected: widening the anchored method too (its drift
  is a few tokens once learned, and the live suite holds it to that); a fixed
  safety margin (it would cost exact endpoints context they do not need to
  give up). A part-by-part estimate (`Section.Tokens`, `MessageSizes`) is still
  four bytes a token and picture cost from `counter.EstimatedImageTokens`; it
  decides where a compaction cuts and what the inspector shows until the
  breakdown item replaces it. The agent's own `ImageTokens` is gone.
- **Reasoning tokens from the reasoning text, labelled.** Where the endpoint
  states no reasoning count, the recorder tokenises the reasoning text with
  the counter's text route and stores it with `reasoning_source = tokenised`.
  On the bench's llama.cpp a response cut off inside its reasoning tokenised
  to exactly the completion tokens the server generated (60 and 60). No text
  or no text route means no count (NULL), not 0: an endpoint may have hidden
  its thinking. The number never enters `provider.Usage` or the events.
- **A migration renames, it does not copy.** `0006` renames the table, the
  two columns, and every index and constraint named after the old table, so no
  object keeps the old name, and rows are untouched. Rejected: a new table
  and a copy (the rows are the deployment's history).

### The inference queue

`internal/inference` is the one queue every model call of Core waits in
([Architecture](ARCHITECTURE.md#inference-admission-and-priority)).

- **A leaf package on the standard library.** It imports nothing of the
  module, so the agent, the probe, the utility tasks and the server all hold
  leases without it knowing any of them; callers translate provider rows into
  `inference.Endpoint` and a call into `inference.Need`. `internal/imports_test.go`
  holds this (`leaves`). Rejected: a queue that knows `provider.Provider` and
  wraps it. The agent needs the lease itself: `queue_ms`, the slot and the
  priority go on the `model_calls` row, the request's predicted prompt is the
  lease's `Tokens`, the lease's context is the call's and is how a preemption
  reaches it, and every attempt of a retry takes its own lease. A wrapper would
  have hidden all four.
- **Every call site holds a lease, and a test keeps it so.** The agent holds
  one per attempt in `stream` (agent turns and the compaction summaries,
  which follow their run's priority). The server holds one around the session
  title (maintenance, class `fast`), the model test (interactive: the user is
  waiting) and, through `probe.ResponsesCheck.Queue`, each of the Responses
  check's two requests. `internal/modelcalls_test.go` scans the source for
  `Stream` and `provider.Complete` calls and fails on a file it does not list,
  with the reason that file is covered. Counting requests (`/tokenize`,
  `/apply-template`) and probes that send no prompt take no lease. A lease
  covers a model call only: never a tool call, a backoff or any other wait
  between calls, so a retry takes a new lease and a more urgent call can get in
  between.
- **Pools come from the provider rows**, `pool` or else the provider's name,
  and `classes[].pool` is the same value. The server describes the queue from
  the store at start, on first use, and after every change of a provider, a
  probe or one of the queue's settings (`syncInference`), so a changed `slots`,
  `kv` or `kv_tokens` takes effect on the next admission without a restart.
  Rows of one pool that disagree take the most restrictive of each: the
  fewest slots, the smallest cache size, and the most binding kv mode. A pool
  is one machine, so a row that claims more than another is not believed over
  it. Rejected: the largest (it can overfill a cache), the first row (the
  answer would depend on creation order). A lease already running is never
  cancelled by a change of settings; a smaller pool admits nothing until
  its running leases fall under the new size, and a larger one admits what
  waits at once.
- **Admission on a shared cache.** A call is admitted while the running calls
  fit the cache with it, each counted at its predicted prompt plus its maximum
  output (`Tokens`; a request with no output limit counts the rest of the
  window). `kv: unknown` is admitted as shared with the probed per-request
  window as its size, as the probe's log entry says; `shared` with no known
  size, `partitioned`, and `unknown` with no probed window are limited by slots
  alone. Order is strict: the next call by priority is the only one considered,
  and when it does not fit nothing behind it is admitted, so a large call is
  not starved by a stream of small ones. A call larger than the whole cache is
  admitted when the pool is empty, and runs alone, with a warning. The count
  is a worst case, so failing a turn for it would be wrong, and llama.cpp or the
  engine reports the real overflow if it comes. Rejected: failing it clearly
  (it fails a turn that might fit) and admitting it beside others (it does the
  harm admission exists to prevent).
- **A prefix shared between sessions is not counted once.** The design asks
  for it "where the engine caches prefixes", and it cannot be done today:
  the loop has no prefix identity to hand the queue (the counter keeps prefix
  hashes of its own, per session, and exposes none), the only engine on the
  bench is llama.cpp, whose unified cache is not shown to share cells between
  slots, and the engines that do share (vLLM, SGLang) are unverified. Calls are
  counted whole. That over-counts, which costs parallelism and never overfills
  the cache.
- **Priority is per call.** Interactive, foreground, background, maintenance.
  A call waiting gains one level per `aging_seconds`, never passing interactive;
  ties go to the call that began waiting first, so a stream of interactive
  calls cannot starve an older background one (a test shows it admitted within
  two aging periods). The wait is counted from `Need.Since`, which a retry
  passes, so a call that was preempted keeps the age it earned. A run's
  priority that can change is an `inference.Rank` the loop reads at every
  `Acquire`: raising it (a user message joining a background home turn) changes
  the run's next lease and nothing already queued. The home session of phase 2
  calls `Raise`; no caller does yet but tests.
- **Maintenance runs after an idle pool, and is not blocked forever.** A pool
  is idle when none of its running calls is above maintenance and the last one
  ended `maintenance_idle_seconds` ago (a pool that never ran one has been idle
  for ever, and 0 turns the wait off). Maintenance calls do not keep the pool
  from being idle for each other, so three titles run together. A maintenance
  call that has waited one aging period is background, and no longer waits for
  idleness: a pool that never rests delays a title by `aging_seconds` and not for
  ever. Titles are maintenance, so on a pool in use a title appears a couple
  of minutes after the last model call, not within seconds; a title that waits is
  owned by the server (`titles`), not by the run, so it is made after the run is
  over. Its time limit starts at admission, since waiting for the pool is not the
  model being slow.
- **`preempt_running`** (default off) cancels running background or maintenance
  calls, least urgent first and, among equals, the one started last (the least
  prefill lost), when an interactive call is waiting and cannot be admitted for
  want of a slot or of cache room, and only the calls that cancelling makes
  room with. A call that waited its way up to interactive is not a victim, so a
  background call cannot be preempted for ever. The victim's lease context ends
  with `ErrPreempted`; the slot is free once the victim lets go. The agent
  turns it into a retryable `provider.Error`, so the retry path takes it:
  `message.reset`, a new lease at the same request, no `run.error`. It is not
  charged to `MaxRetries` (the endpoint did not fail, and a user chatting a
  while must not fail a background run), and has no backoff. Calls outside the
  loop (titles, the check) are made again by `Queue.Do`, up to sixteen times.
- **The Responses check is maintenance, unless the user asked.** A start of
  Core runs it only for a stale outcome and nobody waits, so it waits for an
  idle pool like a title; "Probe again" is interactive. If its requests never get
  a lease before its four minutes are up, that says nothing of the endpoint, so
  the result is `skipped` with a reason and `Busy` set: the earlier outcome
  stands, and it is not current, so the next probe tries again. Rejected: a
  `failed` verdict, which would put a busy pool's provider on `chat`.
- **What is recorded.** `queue_ms` is the time a call waited for leases, summed
  over its attempts; `slot` is the server's own slot number that the request
  named (`id_slot`), and null for a call that named none (see "Slot
  affinity"); `priority` is what the call asked for;
  `class` is the model class a run was resolved from, which no run names yet,
  so it stays absent; `prefill_ms` and `decode_ms` (migration `0009`) are the
  endpoint's `timings`, the decode time the harness's own from the first token
  when the endpoint states none. Waits over 250 ms are logged when the call is
  admitted, preemptions and oversize calls as warnings.
- **Visibility is a read-only route.** `GET /api/inference` reports each pool,
  what holds its slots and what waits in the order it will be admitted; the
  Activity page of phase 3 is its UI. The queue's three settings
  (`maintenance_idle_seconds`, `aging_seconds`, `preempt_running`) are
  settings keys like `computer_idle_minutes`, applied when saved, and have no
  UI of their own yet.
- Compaction summaries, though queued, are still neither recorded nor
  observed; they remain unowned by the Recorder until the agents item.

### Slot affinity

A llama.cpp server with several slots keeps each slot's prompt in its own KV
cache, so a session's next call is cheap only if it runs on the slot that holds
the session's prefix. The queue places the call there and the request names the
slot (`id_slot`, which both `/v1/chat/completions` and `/v1/responses` read;
verified against `/slots` while a call ran, on b11942). Current behavior is in
[Architecture](ARCHITECTURE.md#inference-admission-and-priority).

- **A book per endpoint, in the queue.** Each endpoint of a pool that takes slot
  hints has one entry per slot of its server: the session whose call last ran
  there, its prompt size and when. It is the "LRU map from session to slot",
  held as slot to session because a slot holds one prefix, so a map with more
  sessions than slots would remember prefixes the server no longer has. A call
  with a slot of its own gets it when it is free. A session without one takes
  the free slot whose owner went longest without a call (an empty slot
  first), and the map follows: the slot it left is given up, since a stale
  shorter copy is not worth sending it back to. The lease says which slot
  (`Lease.SlotHint`), the loop sets it on the request after the request was
  sized, and the provider sends it. There is no backend check in the loop: the
  pool states which endpoints take hints, and a call to any other gets no
  number.
- **Busy slot: wait as long as reading the prefix again would take, then move.**
  When a session's slot is busy (its own earlier call, such as an idle
  compaction still generating when the user writes) and another is free, waiting
  costs a queue delay of unknown length and taking the other costs a prefill of
  the whole prompt. The call waits for the slot for `prompt / prefill rate`
  (`Policy.PrefillRate`, 2000 tokens a second, from the 22.8 s the bench took
  for 66002) and then takes the free slot. That is the rule for buying or renting
  with an unknown horizon: it never costs more than twice the better choice
  whatever the occupant does. While it waits it holds the line like any call that
  does not fit, so a stream of small calls cannot take the slot it will need.
  Rejected: always waiting (a compaction of a minute behind a user message);
  always moving (a 60k prefix prefilled for a wait of a second); backfilling
  past it (it could find the pool full when it gives up).
- **Compaction summaries share the session's slot, titles share nothing.** A
  summary's request is the session's conversation with one instruction added,
  so it reads the session's prefix in the session's slot, and after it the old
  prefix is dead whichever slot held it. A title (and the model test and the
  checks) shares no prefix with any session and takes `Affinity` false: it names
  no slot, never claims a slot in the book and never moves a session's entry, so
  the server places it by its own rules (the slot whose cache shares most with
  the prompt, else the one used longest ago) and the sessions keep what the
  server leaves them. Such a call still takes a lease and the pool's capacity.
- **Slot numbers belong to an endpoint.** A pool's endpoints are one machine's
  capacity but not one server: slot 1 of one means nothing on another. Each
  hinted endpoint has its own book and the lease carries the number of its own
  server's; an endpoint without hints in the same pool is named no slot. A hinted
  endpoint may have more slots than the pool runs calls at once (rows of a pool
  take the fewest), and then keeps more sessions' prefixes warm than run
  together. Rejected: one numbering per pool (a session pinned to slot 1 would
  be sent slot 1 of a server that never held its prefix) and no hints in a pool of
  several endpoints (it loses the common case of one server and a decision
  model beside it). Two provider rows that reach one server are two endpoints to
  the queue and may name the same slot at once; the server then makes the second
  wait for it. Give one server one row.
- **`model_calls.slot` is the number the request named, null otherwise.** A
  column that held the queue's count for a vLLM call and the server's for a
  llama.cpp one would answer neither question.
- **The hint steers where, never what.** `id_slot` is not in the body the
  counter renders (`ChatEncoder.ChatBody`), so `/apply-template` and the exact
  prediction are the request's without it; a test shows the body is the same with
  and without, and the live suite shows zero drift on requests that carry it.
  A server that refuses the field with a 400 that names it is sent the request
  again without it, once, so a hint is never the reason a call fails (llama.cpp
  does not refuse: it takes any number modulo its slot count).
- **Hints are sent only where measured to help.** Whether a named slot keeps its
  prefix depends on how the server was started, which it does not state, and on
  the bench it does not: with a unified KV cache and `--cache-idle-slots` (both
  the default) llama.cpp saves an idle slot's prompt to a host cache and clears it
  whenever another slot starts a task, and then restores a prompt from that cache
  only for a request that names no slot (`get_available_slot`, which takes an
  empty named slot as it is). Alternating sessions pinned to their slots found
  nothing cached on any call (25920 prompt tokens prefilled against 10825
  with no hint). So a llama.cpp server with several slots gets `id_slot` only
  after the slot affinity check passed (`probe.CheckSlotAffinity`, in
  `capabilities.slot_affinity`): three small requests to the user's model, the
  first and last naming slot 0 and the second slot 1, and the last must find at
  least 80% of the first's prompt cached. It runs as the Responses check does, on
  the explicit probe and at start only when its recorded outcome is for another
  build or model, at maintenance priority at start, and until it has passed a
  server is sent no `id_slot`, so a new provider behaves as it did before this
  item. The server's own selection (the longest common prefix, else the slot used
  longest ago) is what runs then. Rejected: sending hints to every llama.cpp
  server (it makes the bench strictly worse), switching them off after a few
  cold calls seen at run time (a compaction or an edited conversation looks
  the same as a server that lost the prefix, and the state would be in no setting
  or probe a person can see), and a setting (nothing tells a person which way to
  set it). Starting the server with `--no-cache-idle-slots` should make it pass:
  that is read from the source of the build, not run, because the bench's server
  is not ours to restart.

- **A run is judged against the smaller of two numbers.** The model row's
  `context_window` is what the user says the model has; the provider's probed
  `capabilities.window` is the per-request context the endpoint said it serves
  (llama.cpp's per-slot `n_ctx`, vLLM's `max_model_len`). `server/window.go`
  takes the smaller (`effectiveWindow`; no probe means the configured one) and
  `runConfig.window()` hands it to `agent.Options.ContextWindow`, which `fit`,
  `shouldCompact`, the summary's room, the counter's reserve, `silentOverflow`,
  `model_calls.context_window`, the events' `context_window` and the meter all
  already read. There is no second place that picks a window. Rejected: the
  agent taking both numbers and choosing (every reader of the window would need
  the rule, and a call's recorded window would not say which was meant).
- **The configured number is never rewritten.** A probe that finds less does
  not edit the model row: the user's number may be right for a later restart
  of the server, and a probe that failed to read the field would otherwise lose
  it. Settings warns on the model instead (`effective_window` below
  `context_window` on the wire, `probed_window` beside it), in words that say
  what to do: set the window to what the endpoint holds, or restart the server
  with a larger context. Rejected: silently lowering the row; refusing to save
  a larger window (the probe can be stale, and a window set before the server
  was started is legitimate).
- **A recorded call keeps its own window.** `GET .../calls/{call_id}` answers
  with the `context_window` the call was judged against, and the model's
  configured one only for a record that kept none, so a probe that changed
  since does not rewrite what an old call was measured against.

### Context breakdown

- **Each part is counted on its own; the template is the remainder, stated
  and never spread.** `agent.MeasureParts` sizes the system prompt's
  sections, each tool, each message and its replayed reasoning with the
  session's counter (`Counter.Size`, `ToolSizes`, `ImageSize`). The server
  states `remainder = prompt - sum(parts)` and returns both. A negative
  remainder stays negative and the UI shows it as an accounting bug. The
  older `calibration` on the context response scaled the parts to a measured
  call; it is retired, because a scale hides exactly the errors the breakdown
  exists to expose. Rejected: scaling parts to the measurement (a part could
  be wrong by any amount and the sum would still look right); clamping the
  remainder at zero (it would hide a double-counted part).
- **What the remainder is stated against.** A recorded call: its measured
  `prompt_tokens`. The next request has no measurement, so the remainder is
  against its prediction only when that prediction is `exact`, since an exact
  prediction is the endpoint's own count of that very request; against an
  `anchored` or `estimated` one it would only measure the predictor's guess
  and is `null`, shown as n/a. A NULL is never shown as 0. Rejected: stating
  it against the prediction always (the line would look measured when it is a
  guess).
- **Tools are sized by rendering prefixes.** Where the endpoint renders the
  request, tool _i_ costs `render(tools[:i+1]) - render(tools[:i])`, so the
  sizes add up to the whole list's cost; the first carries the template's
  wrapper for the list. Past 64 tools, or where the endpoint only tokenises
  text, a tool is its JSON as text and marked approximate. Rejected: tokenising
  the JSON always (the template renders a tool differently, and the error is
  invisible without a mark); rendering without tools once and subtracting (one
  size for the whole list, no per-tool figure).
- **Counted on demand, remembered by hash.** The loop never measures parts;
  the context and call routes do, and the counter's content-hash memos mean a
  growing conversation tokenises only what is new. Rejected: measuring in the
  loop on every call (a second round trip per part on the critical path);
  storing the breakdown on `model_calls` (a recorded call is rebuilt from its
  messages, and a stored figure could not be recomputed when counting
  improves).
- **A picture is approximate until measured.** The routes do not count
  pictures (see "The token counter"), so a message with a picture is marked
  approximate until a measurement of a picture of its size is learned.
- **The meter asks for the prediction alone.** The status bar is always on
  screen, and `GET /api/sessions/{id}/context` counts every part, so the meter
  reads `GET /api/sessions/{id}/prediction`, which sizes the request once.
  `Counter.Peek` sizes without leaving a pending prediction, so a preview never
  stands in for the request a run sends and `Observe` pairs with the run's own.
  Rejected: the meter reading the whole context (a heavy payload for a number
  that changes once a turn).
- **An exact prediction with any drift is shown as a bug.** The inspector
  raises an error notice for a recorded call whose `prediction` is `exact` and
  `drift` is not 0; the loop already logs it.
- **The preamble is not a part yet.** The system prompt's sections are the base
  prompt and the instructions; the preamble item (Phase 1) adds its own.
  Nothing is invented for it.

### The token counter

- **A package of its own over an optional capability of the provider.**
  `provider/counter` holds the `Counter` (one per session), the `Source` (an
  endpoint's tokenizer routes) and `Routes` (the HTTP `Source`). The provider
  contributes one optional interface, `provider.ChatEncoder`, in the way
  `Lister` is optional: `ChatBody(req)` returns the Chat Completions body, from
  the function `Stream` posts. Rejected: a `Count` method on `Provider` (the
  routes depend on what the probe found, and prediction needs per-session
  state a provider does not have); a second serialiser in the counter (it would
  drift from the first the day a field is added); the counter in `agent` (the
  live suite and the next items use it without a loop).
- **The server renders, from the body the provider sends.** llama.cpp's
  `/apply-template` takes the whole Chat Completions body (checked on b11942:
  `reasoning_effort`, `chat_template_kwargs`, `preserve_thinking`, tools, tool
  calls and `reasoning_content` all change the prompt it returns), so the
  counter posts `ChatBody` as it is. That the template adds a preamble when
  `reasoning_effort` is absent is why the body must be complete. vLLM gets the
  same body at `/tokenize` (unverified). Tabby and SGLang count text only.
- **Prefix cache: cut the rendered prompt at control tokens.** A tokenizer
  never merges across a control token, so a prompt cut at the start of one
  counts the same in pieces as whole. `Routes.Boundaries` finds candidates
  (`<|...|>`) and asks the server once each whether it is one token with
  special-token parsing and not with it off (a plain vocabulary entry fails
  that). The counter remembers the token count up to each prompt's last cut by
  the hash of everything before it, so the next call tokenises only the
  messages added since; the generation prompt after the last cut is a memo of
  its own. A count with nothing reused is also taken whole, once per such
  count, and a cut that changes the answer switches cutting off for the
  session. vLLM shows no prompt, so a repeated whole request is remembered
  and nothing finer. Rejected: cutting at newlines or at message ends found
  by rendering prefixes (not guaranteed separable, or one render per message);
  asking for token ids with pieces (a megabyte of JSON at 20k tokens).
- **Pictures are not counted by the routes, so they are taken out.**
  Checked on the bench with one 56, 112 and 224 pixel PNG: `/apply-template`
  leaves a media marker, `/tokenize` counts the marker as 26 ordinary tokens
  and nothing for the picture, and the real prompt was 11, 18 and 51 tokens
  larger than the text without the marker. The counter counts the request
  without its pictures and adds a cost per picture size, learned from the
  first measurement of that size (`anchored`) and a pixel estimate until then
  (`estimated`). A request with a picture is never `exact`. Several unknown
  sizes in one request teach nothing. Rejected: stripping the marker from the
  prompt (it needs the server's per-start marker string, and the text on either
  side of it is tokenised apart there, which stripping would join).
- **Anchored: least squares over the template's share.** The text of a
  request is counted exactly; what the template adds is a weighted sum of seven
  terms (a base, one per role, per tool call, and replayed reasoning after and
  before the last user message), fitted on every measured request, each
  weight pulled toward a prior (a ChatML-sized 5 or 6 tokens a role or call; none
  on the base, or a large constant such as a tool list's boilerplate would be
  spread over the roles, which cost 95 tokens of drift on the second call on the
  bench before the prior). Replayed reasoning has two terms because a template may drop what
  precedes the last question, and the weights say so after a few calls. When a
  request extends the last measured one, the prediction is that measurement plus
  the change in the model; after a compaction it is the model alone. The first
  prediction, before any measurement, is `estimated`. Rejected: a per-role
  average of residuals (a call that adds an assistant message and a tool result
  at once cannot be split).
- **The learned constants live in the session's `Counter`, in memory.** The
  weights, the ratio and the picture costs are rebuilt from the first calls after
  a restart (about five, in the fake) and cost no storage. Each `model_calls` row
  carries its prediction and measurement, so persisting them stays possible; a
  restarted session on an endpoint that counts by bytes is seeded from its stored
  measurement instead ("Model calls").
  Rejected: persisting them now (a new table before anything reads it); sharing
  them among sessions of an endpoint (the tool list sits in the base, and sessions
  differ in theirs).
- **Estimated: one ratio.** Bytes divided by the bytes per token learned over
  the session (4 until measured), from the last measurement when the request
  extends it, with the running relative error of estimated predictions exposed
  as `EstimateError` for the compaction reserve. A ratio per content class
  (prose, JSON, code) is not built: nothing measures one yet.
- **Reasoning tokens from the reasoning text are the loop's.** The counter
  offers `CountText`; the labelled column is `model_calls.reasoning_source`
  ("Model calls").
- **A summary of drift is the sum of absolute drift.** Errors in opposite
  directions must not cancel to a clean report. A scenario fails on drift in any
  prediction called exact.
- **Cost, measured on the bench at about 20k tokens (a prompt of 20,389
  tokens).** `/apply-template` takes 8 ms and `/tokenize` of the whole prompt 23 ms
  over the SSH forward, so a count with no cache is about 31 ms. With the cache a
  count is the render plus the new messages: 10 ms one exchange later. The first
  count costs 58 ms, the two checks of a new session included (the candidates'
  control-token test, and the whole-prompt count against the pieces).

### Provider transport

- **One HTTP client for every request to an endpoint, repeating a request that
  found its idle connection closed.** `provider.NewHTTPClient` shares
  `http.DefaultTransport`'s pool, and when a request fails with EOF or a reset
  before any response byte on a connection taken from the idle pool, it sends
  the request again on another connection (at most four times, never after a
  failure on a new connection). The openai provider, the probe and the token
  counter use it. Cause, found on the bench: llama.cpp closes the connection
  when a streamed response ends (a request after a stream never got a reused
  connection in a trace; one after a plain response did). The client can take
  that connection from its pool before it has seen the close, which an SSH
  forward delays; the POST then fails with `EOF`, about one call in twenty on
  the bench. Go retries such a failure only for requests safe to repeat, and a
  POST is not. The code classed it correctly already (`Retryable`, so the agent
  loop recovers after its backoff); the fault was that nothing else recovered
  and a token count before every call would meet it as often as the call.
  A request whose reused connection the server closes while it is being
  written fails with net/http's `http: server closed idle connection`, which
  is the same fault; `closedByServer` recognises it by that whole message,
  because net/http keeps the value unexported and wraps nothing around it, so
  there is no type or sentinel to test for. The regression test provokes the
  real error, so a Go release that rewords it fails the test.
  Rejected: `Connection: close` on streamed requests (a TLS handshake per
  call to a hosted API for a fault of one server); disabling keep-alives
  (same cost); leaving it to the loop (the counter and the probe have no
  loop, and the probe takes one failed request as "nobody answers"); retrying
  at the SDK (`WithMaxRetries` also repeats after real server errors, which the
  loop owns).

### The Responses API

- **A second implementation of `provider.Provider` beside the chat one, in
  package `openai`, wrapping it.** `openai.Responses` holds the chat `Provider`
  of the same endpoint and uses its SDK client, authentication, HTTP client,
  error classification (`apiError`), model list and `ChatBody`. The registry
  builds one or the other from `Endpoint.API`. Rejected: a package of its own
  (it would copy the client, the key rules, the retry classification and the
  model list, or import them back); one `Provider` with a protocol flag (every
  method would branch, and the two bodies share nothing but the endpoint).
- **The stream is read as raw events, not through the SDK's typed Responses
  stream.** The SDK decodes an item into a union that drops fields it does not
  model; a reasoning item must go back as the endpoint returned it
  (`encrypted_content` is opaque to everything but the server that made it).
  The SDK's server-sent-event decoder and its request path are used, and the
  items stay `json.RawMessage`. Rejected: the typed stream and a re-encoded
  item, which is not "as returned" for a field added next year.
- **Counting stays one serialiser.** `Responses.ChatBody` is the chat
  provider's, so exact counting renders the chat request for either protocol.
  That is only right if the server converts Responses input to chat the way the
  chat body says, which phase 1 checks by drift (live scenarios `responses_*`
  repeat every counting scenario under Responses) and the conformance check
  checks again on every endpoint it runs against. Rejected: a Responses-to-chat
  conversion of our own to render (a second serialiser to keep equal to the
  server's, and wrong the day the server changes its mind).
- **Reasoning items are kept on the assistant message, beside the text.**
  `Message.ReasoningItems` holds each item exactly as returned, in a
  `reasoning_items` array of the stored entry; `Reasoning` stays the text, which
  the UI, the counter, the compaction summary and Chat Completions read. The
  entry's payload keeps the text of the item (a `json` column; "Prefix
  stability" says why not `jsonb`, which reordered the item's keys, and why an
  item `encoding/json` would change is stored as a string of its text), and a
  test stores an 80 kB encrypted blob and reads it back; an old entry has no
  key and reads as before. Rejected: items
  replacing the text (every reader of it changes, and a protocol switch loses
  the reasoning); items in a table of their own (an entry is one message, and a
  fork or a resume would have to join them); the whole output list instead of
  the reasoning items (the answer and the calls already have fields, and two
  copies of a tool call can disagree).
- **A session that changes `api` still sends a valid request.** Chat
  Completions sends `Reasoning` and ignores the items. A Responses provider
  sends the items, and for a message that has only text (made under chat) it
  builds an item holding the text for a server on the user's hardware, so the
  conversation renders as it did and counts as the chat body counts it, and
  sends none to a hosted API, which accepts only items it returned. A hosted API
  also refuses a reasoning item with nothing after it, so one in a message cut
  off in its reasoning is not sent there; a local server takes it, as it does
  under chat. Rejected: dropping stored reasoning on a switch (a cache miss and
  a different prompt than the one counted); a synthesised item for a hosted API
  (refused).
- **A hosted API is asked for `reasoning.encrypted_content` when the model
  preserves thinking.** `store: false` returns no reasoning to pass back
  otherwise, and nothing reads it unless it is sent back, which only a model
  with `preserve_thinking` does. Asking for it of a model that does not reason is
  believed to be refused (unverified: no hosted API is on the bench), so it is
  not asked of every request. Rejected: always asking a hosted API, which risks
  a refused request for the model it is not meant for.
- **What a hosted API takes is what the specification defines.** A request to
  one carries temperature, top_p, max output, `reasoning.effort` and `include`;
  a server on the user's hardware also takes `top_k`, `min_p`, the penalties,
  `seed`, `stop`, `preserve_thinking`, and the template and thinking switches as
  top-level fields, as it takes them beside Chat Completions' (llama.cpp
  b11942 accepted `top_k`, `min_p`, `seed`, the penalties, `preserve_thinking`,
  `chat_template_kwargs` and `reasoning.effort`, and `stop` took effect). A hosted API gets `strict: false` on its
  tools, since the Responses API defaults to strict and refuses a schema that is
  not closed; a local server is not sent the field (not tried on the bench).
  `Endpoint.Hosted` is the probe's backend `openai`. Rejected: sending every
  field to every endpoint (a hosted API refuses an unknown one); a field per
  backend (the same two classes cover the five).
- **A cut-off is reported as `length` even where the server calls it complete.**
  The Responses API says `response.incomplete` with `max_output_tokens`;
  llama.cpp b11942 says `response.completed` with no reason for a response cut
  at its limit (recorded). The provider reports `length` when the output count
  has reached the limit the request set, so the loop keeps the text and drops a
  tool call cut mid-arguments, as under chat. A response that ended on its own
  at exactly the limit is taken as cut off too, which costs at most one tool call
  that the model asks for again.
- **`api` is a provider column, set by a conformance check and overridable.**
  `chat` or `responses`, with the same override rules as `backend`: `overridden`
  names it, a probe leaves it alone, `reset` hands it back. Migration `0007`
  adds it with `chat` for every existing row. The check's outcome is in
  `capabilities.responses` (verdict, reason, the model and build it ran on,
  whether the reasoning round trip, the cache metrics and exact counting were
  exercised); the probe chooses `responses` only for `passed`. Rejected: the
  protocol on the model row (it is a property of the endpoint, and models share
  one); a global setting (a cloud endpoint and a local one differ).
- **The conformance check asks the user's model, and only when needed.**
  `probe.CheckResponses` sends two requests with an output limit of 512 and 256
  tokens and 90 seconds each: a streamed call to a tool with the model's
  reasoning, then the call, its result and the reasoning item sent back as
  stored. It passes when both complete, both state their usage, the second
  states a cached count (cache metrics are the design's condition), and, where
  the endpoint counts exactly, the second request was counted to the token
  before it was sent. A model that does not reason has no item to send back; the
  outcome records `reasoning: false` and may still pass. It runs on the
  explicit probe, and at start only for a stored provider whose recorded outcome
  was made on another server build or another model than the one it would ask
  now (the provider's oldest configured model), so a start of Core sends the
  model nothing in the usual case. A provider with no model is `skipped`, and a
  failed or skipped check means `chat`. Rejected: running at every start (a
  real prompt per provider on every restart, on a GPU that may be serving a
  turn); a canned request inside `probe.Run` (which never asks a model, so a
  probe stays free to run anywhere); a fixed model list (the outcome depends on
  the model's template); checking on a model's creation (the second request
  needs the model's own thinking settings, which a form is still editing); not
  requiring cache metrics (the design makes them a condition, so Activity can
  read the cache for either protocol; vLLM without
  `--enable-prompt-tokens-details` therefore stays on `chat` unless the user
  sets `responses`).
- **llama.cpp is `responses` by default because the check passes and the drift
  is zero.** Every counting scenario run under the Responses API on the bench
  (b11942) predicted the prompt to the token and reported the same counts as
  under chat for the same conversation (Log, 2026-10-31). The check includes the
  drift, so a build whose conversion differs fails it and stays on `chat`. A
  new Core on an existing database moves such a provider to `responses` at its
  first start that finds a model.
- **A reasoning-only assistant message is sent with empty content under
  Chat Completions.** llama.cpp refuses an assistant message with neither
  content nor tool calls (400), which is what a response cut off in its
  reasoning leaves; every later request of the session failed. `content: ""` is
  accepted and renders as before.

## Image input

- **Images are prepared once, in the harness** (`internal/imaging`), when a
  message is posted, not in the browser: every client gets the same result,
  the API refuses what it cannot read before a run starts, and a browser's
  canvas resampling differs by engine. What is stored is what every later
  request sends.
- **Fitted within 1080p** (1920 by 1080, either way up), never enlarged. It is
  about what hosted vision models downscale to anyway (Anthropic's 1568 long
  edge, OpenAI's 2048 box), so more pixels would be paid for and thrown away.
  A full-page screenshot comes out narrow; tiling one was rejected as the
  model's job, not the upload's.
- **Catmull-Rom** (`x/image/draw`), whose kernel widens when shrinking so
  fine detail averages instead of aliasing, and which keeps small text sharp.
  Resampling is in sRGB like Pillow and libvips: linear light was rejected as
  a cost with no visible gain for screenshots.
- **PNG unless it is a photo.** A JPEG, or a lossy WebP, becomes a JPEG at
  quality 90; anything else a PNG, falling back to JPEG on white only when the
  PNG is over 3.5 MB (under the 5 MB base64 limit of the strictest hosted
  API). A JPEG that needs no fitting and no turning keeps its compressed data
  with its metadata stripped, so it loses nothing to a second compression.
  Output is PNG or JPEG only, the formats every endpoint reads (llama.cpp does
  not read WebP).
- **EXIF is applied, then dropped.** Orientation is applied because endpoints
  disagree on honouring it; the rest (location above all) is not sent to a
  third party. Data a phone appends after the image (motion photo video,
  depth maps) is cut.
- **Images live inline in the entry**, as base64 in the stored message, like
  MCP images in `tool.result` details. A table of attachments with ids was
  rejected: a second place to store and clean up, for no gain at 1080p sizes.
- **Image input is a model fact, off by default** (`models.image_input`), not
  a profile setting. An endpoint may refuse a request with images for a model
  that cannot read them; the harness refuses to start or steer a run with
  images on such a model, and a conversation that already holds images is
  sent to it with a note in their place, so switching models never breaks a
  session. OpenRouter's listing says which models read images and the picker
  starts from that.
- **An image is estimated at one token per 750 pixels**, not by its base64
  size, which would count a screenshot as hundreds of thousands of tokens and
  compact every conversation that holds one.
- **Deferred:** images a tool returns (an MCP screenshot) reaching the model,
  and a tool that shows the model an image file in the computer. Chat
  Completions takes images only in user messages, so both need a user message
  after the tool result, which is its own decision.

## Compaction

- **Pi's compaction, ported whole**: when the next request would leave less
  than `reserve_tokens` (16384) of the window, the oldest part of the context
  is summarized and replaced; roughly the newest `keep_recent_tokens` (20000)
  stay verbatim. The cut is at a user or assistant message, never a tool
  result; a turn too large to keep is split, its start summarized apart.
  A later compaction updates the previous summary rather than summarizing a
  summary. The summary format, prompts, 2000-character tool results in
  transcripts, split-turn merge, and one compact-and-retry after an
  overflow error are Pi's. Both token settings shrink to a quarter of a
  small window, or a model whose window is near the settings would compact
  on every call.
- **Checked before every model call**, not only after a run as in Pi: a
  single turn can outgrow the window, and compacting before the
  call is cheaper than an overflow error and a retry.
- **The summary request keeps the prefix cache.** It is the run's own
  request (system prompt, tools, parameters, and messages up to the cut,
  exactly as they were last sent) with the summary prompt appended as a user
  message, so the endpoint reuses the cached prefix and only prefills the
  prompt. Pi sends the conversation as one serialized transcript instead,
  which re-reads the whole context uncached. The transcript form remains the
  fallback for a prefix that no longer fits the window and for a model that
  answers with tool calls instead of a summary. The kept tail is not in the
  request, so the reserve need not hold the summary's output as well.
- **Thinking stays as the run set it**, because some chat templates (gpt-oss)
  render the effort into the system prompt and a different effort would
  miss the cache. A summary whose thinking used up its budget (cut off, or no
  text) is asked for again with thinking off (`none`, or the lowest effort
  the model offers).
- **A compaction is an entry** (kind `compaction`) holding the summary and
  how many context messages before it are kept, not the first kept entry's
  id: a count survives copying the entries.
  The context is a fold over the path: messages append, a compaction
  replaces what came before with the summary and the kept messages. Earlier
  entries stay in the tree, so moving the head above a compaction undoes it.
- **Where Eika departs from Pi's cut and merge.** When only tool output
  would fit in `keep_recent_tokens`, the cut goes to the newest user or
  assistant message instead of keeping everything, which is what Pi does
  and which leaves an over-threshold context uncompacted. When a split
  turn leaves no complete turns to summarize, the previous summary is kept
  verbatim; Pi drops it. A summary still cut off on its last attempt is
  used as far as it goes rather than failing the run.
- **An overflow is recognized by its wording**, with Pi's patterns, since
  endpoints report it only in prose. They match anywhere in the error
  (Eika's errors name the failed operation first), and a throttling error
  that mentions tokens ("please wait", "rate limit") is never one.
- Pi's list of read and modified files is not ported: it comes from `read`,
  `write`, and `edit` tool calls, and Eika's files change through bash.
- **Settings are one global key** (`compaction`: auto, both token budgets,
  and the three prompts), not profile fields: they describe the harness's
  memory management rather than what a run sends. A manual compaction
  (`POST .../compact`) is a run of its own, so the one-run-per-session rule,
  abort, and events apply unchanged.
- The summary call uses the run's model. A utility model was rejected for
  now: it has no cache of the conversation, so it would re-read all of it.

## Model kinds and classes

- **The harness's own tasks name a class, and a setting maps each class to a
  model row.** `model_classes` (`deep`, `main`, `fast`, `decision` to a model
  name) replaced `utility_models`, which mapped one task to a model. A class
  reuses the provider, key and thinking switch already configured on a model
  row, and agents and later tasks share the classes instead of each adding a
  key. The names settle the plan's open question: `high/medium/low`,
  `System 2/System 1` and `thinker/worker/scout` were rejected, because
  `deep`, `main` and `fast` say what a class is for and read clearly in
  config. Migration `0008` carries `utility_models.session_title` to
  `model_classes.fast`, and a user who had assigned none keeps no class set.
- **Fallbacks are `deep -> main` and `fast -> main`, and a chat class that
  reaches the end of that chain gets the default model; `decision` has
  none.** A missing `fast` model must not turn titles off, which the old rule
  (a task with no model does not run) did. A user who has set no class has no
  `main` either, so `main` falls back to the default model (the
  `default_model` setting, or the first chat model when it names none or
  names a deleted or decision model), and `fast -> main -> default model`
  works before anything is configured. The default is not a class and is not
  written to `model_classes`; the classes UI says which models it supplies.
  Leaving `main` unset until agents move to classes was the first choice and
  was reversed: it left titles off for a user who set no class, against the
  rule above. A class whose assigned model is gone, as after a delete, counts
  as unset and falls back.
- **Titles are skipped when `fast` would fall back onto a hosted deep
  model.** Read literally: `fast` has no model of its own, its fallback
  (`main`, or the default model when `main` has none) is the model the user
  assigned to `deep`, and that model's provider is a hosted API (backend
  `openai`). A title is not worth a call to a
  hosted reasoning model. A `fast` model the user assigned is always used,
  and a hosted `main` that is not also `deep` is used. The broader reading,
  skipping whenever `fast` lands on a hosted model, was rejected: it would
  turn titles off for everyone whose only model is hosted, and the sentence
  names deep models.
- **`kind` is `chat` or `decision`, chosen when a model is added and never
  changed.** Changing it would need the default model, profiles, sessions
  and classes that name the model re-checked in one place; delete and add
  is the way. A decision model answers typed questions at `/v1/systemone`
  and cannot hold a conversation, so it is refused wherever one is held
  (`default_model`, profile and session `model_id`, a message's `model`, the
  `deep`, `main` and `fast` classes) and never listed in a model picker, and
  a chat model is refused for `decision`. A decision model has no reasoning
  effort, thinking switch, preserved thinking or image input.
- **The model test refuses a decision model and sends nothing.** The test is
  a chat request, which a decision model cannot answer, and the one request
  it can answer, a `/v1/systemone` question, is built with the reviewer's client
  (`internal/systemone`, deferred) and was not verified against a server
  here. The test moves there. A one-off request in the model test was
  rejected: it would be a second client for the protocol.
- **The Responses check asks the provider's oldest chat model**, never a
  decision model, whatever is older; a provider with only a decision model is
  skipped.
- **`GET /api/settings` reports the classes resolved**, with each model's
  pool and the model that makes titles, so the UI shows which classes share
  slots without a second copy of the fallback rule. The mock harness repeats
  the rule, as it repeats every handler.
- A utility call is one request outside any run: no tools, no history,
  thinking off through the model's `thinking_switch` (effort `none`). It is
  not a `model_calls` row, which records what a session's runs sent.
- **Session titles**: a session created without a title is `untitled` (a
  column) and shows a placeholder; the first run on it titles it in the
  background from the first user message. The flag is a column because
  creating a session and sending its first message are separate requests,
  and a title a client chose must never be replaced. The title is written
  only while the flag is set, and a failed or unconfigured attempt leaves it
  set, so a later run tries again.
- Titles arrive as `session.title` on `global`, since every sidebar shows
  every session, not only the one open.
- **Pinning and archiving are filing, not state.** They are two boolean
  columns on `sessions` and `computers`, edited with `PATCH` on the
  resource, and change nothing else: an archived session still runs, and an
  archived computer's container is neither stopped nor started. Stopping on
  archive was rejected because it would abort runs as a side effect of
  tidying the sidebar; Stop is one menu item away. The listings return
  archived rows with the rest and the client splits them off, so one request
  still returns a computer's whole session tree. A user's rename or filing
  goes out as `session.updated` rather than as `session.title`, which means
  "the harness named an untitled session".

## Tools

- **bash is the only file tool.** Models chain steps in one shell call, and
  one tool is less to describe than seven.
- One shared, stateless tool registry; the executor arrives per call.
- The built-in registry is `tool/builtin/registry.go` and provider kinds are
  `NewRegistry` parameters: both avoid import cycles.
- `tool.Standalone` marks a tool that needs no computer; unmarked tools
  stay out of chats until their author opts in.
- Tool choices (profile or session) are lists where `mcp__<server>__*` means
  all of a server's tools, so tools a server adds later are included.
- **Context files (AGENTS.md discovery) and the workspace prompt are gone.**
  They told a coding agent about a repository it was working in. Eika is an
  assistant, not a coding harness, so there is no repository to describe and
  one base prompt (`chat_prompt`, built in as `agent.ChatPrompt`) serves every
  session. It makes no claim about files or commands: the tools a session is
  given say what it can do. The profile fields `workspace_prompt` and
  `context_files` went with them, edited out of the baseline migration in
  place (phase 0 keeps one migration), as did `context_files_unread` in the
  context preview, which only said a stopped workspace's files were skipped.
- `ask_user` ids are `q-<hex>`, not store ids: a question is never a row,
  and `tool` must not import `store`.

## Subagents

- Subagents, worktrees, `spawn_agent`, `wait_agents`, `list_agents`, and the
  `subagent.*` events were removed in phase 0 of `PLAN.md`, together with
  `workspaces.worktree_of`, the `subagents` table, and sessions of kind
  `agent`. They return in phase 2 with a new design (`design/`). The log in
  `PLAN_LOG.md` says where the pieces worth reusing live in history.

## Server and API

- Composition lives in `server.Run`; `cmd/eika` stays thin. `Computers`,
  `Models` are interfaces declared in `server` so handler tests run
  against temp directories; `*store.Store` stays concrete.
- Event payload structs live in `internal/event`, so decoders need not
  import the loop and one file lists the protocol.
- **Event fan-out drops the slowest** (buffered channel per subscriber). A
  run never blocks on a browser; the client gets `bus.dropped` and replays.
  Replays write straight to the socket so long histories are not dropped.
- Only the two WebSocket routes accept `?token=`, since browsers cannot set
  handshake headers. The handshake is same-origin or `allowed_origins`.
- **One run per session.** A second is 409; the claim is made under the
  lock before any slow work. Steering and follow-up queues add to a run.
- Computer edits (a file save) republish `computer.state` with the
  same state rather than a new event type.
- The API returns resolved values (effective tools, configuration layers,
  sizes) so the UI never re-derives a rule. Editors ask the server what an
  unsaved choice would inherit.
- **The API contract is generated** (`docs/api/contract.json`) from Go wire
  types by reflection, not recorded responses: a type knows which fields may
  be absent or null, and needs no database. Handler tests and the mock
  harness are both held to it.

## Configuration and security

- Credentials are sealed with AES-256-GCM (`internal/secret`); the key is a
  file in the data volume, apart from the database, so a database copy alone
  opens nothing. A lost key reads as "enter the key again".
- Sign-in: PBKDF2-SHA256, 600k iterations. Setup is claimable once, while no
  password exists; the stack binds 127.0.0.1 so only this machine can claim
  it. Tokens are stored as SHA-256, valid 30 days; changing the password ends
  every session. Attempts are serialised, not locked out.
- **Profiles**: named, all-nullable run configurations; null falls through.
  One function resolves a run: request model → session overrides →
  profile → model row → provider default. `default_profile` is a setting; the
  last profile cannot be deleted. A profile's prompt, when set (even empty),
  replaces the built-in one.
- Sampling fields are pointers; nil means the endpoint's default, so
  non-standard fields (top_k, min_p) are sent only when set. An effort the
  model does not offer falls through and is reported as dropped.

## MCP

- Targets revision `2026-07-28` and falls back to `initialize`, then to
  HTTP+SSE, since most servers are older. The era is found once per
  connection.
- Remote servers connect from the harness (a network call; tools reach
  chats). Stdio servers are processes, so they run in the session's
  computer via eikad `/process`, one per computer.
- Tools are `mcp__<server>__<tool>`, cut to 64 characters. Renaming a server
  rewrites tool choices in the same transaction; names hold no `__`.
- OAuth 2.1 per the MCP spec. The redirect lands on a frontend route that
  posts to the authenticated API, so no unauthenticated route receives the
  code. Registration order: user client id, Client ID Metadata Document (needs
  an https `public_url`), dynamic registration.
- Elicitation is supported; roots, sampling, and logging are deprecated in
  `2026-07-28` and not declared.
- **No agent can use a plugin from phase 2 until phase 11 (accepted).** Today
  a chat gets MCP tools through its tool choice, which a profile or the
  session sets. Phase 2 replaces profiles with agent definitions that have
  fixed tool lists, and a plugin call must be decided by Sentry (phase 4). Phase 11
  now comes right after memory, so the gap is short. The pool, the MCP screens, OAuth
  and the server logs stay in Core and keep working: a server can be added and
  connected, and no agent calls it. Rejected: a stopgap for choosing plugin tools in
  agent definitions, which phase 11 would replace.

## Search

- A port of the localsearch Pi extension. **One provider per web search**,
  the first usable in `search_order` (SearXNG, Exa, Tavily, Brave,
  Marginalia): fanning out would spend quota for little.
- Failures cool down 15 min, doubling to 6 h (or the server's reset, capped
  at a day); quotas count per UTC day and month and persist in
  `search_usage`.
- Constants (pool size, budgets, cache lifetimes, timeouts) are not
  settings: they are budgets, not preferences.
- Caches are in memory. localsearch's on-disk cache is not ported: a harness
  path is useless to a model in a sandbox.
- Tools are `web_search` and `web_fetch` so they do not read as grep/find.
- `allowPrivateHosts` is not ported: the harness shares networks with
  postgres and every sandbox.

## Frontend

- Three panes; one session at a time, addressed by `/sessions/<id>`.
- Panel and tool-renderer registries are single arrays/maps. Panel `id`s
  never change once shipped. Unknown tools render as JSON.
- **The transcript is a pure reducer** (`transcript.ts`) keyed by `run_id`
  for live turns and `entry_id` for stored entries, so content arriving twice
  draws once and a scripted event list is the whole test.
- **No polling**: TanStack Query invalidated by events;
  `refetchOnWindowFocus` off.
- One WebSocket per page with reference-counted topics.
- Token in localStorage via `api/connection.ts`, a plain module (`api/` may
  not depend on a feature). Any 401 forgets it.
- Moving the head rebuilds the transcript, since replay only adds; the
  composer text lives in the session store so a rewind can refill it.
- Rewind is in the transcript. Nothing is deleted.
- Destructive actions confirm in `ConfirmDialog`, not `window.confirm`.
- A sidebar row's quick buttons always show; hiding them until hover made
  them a hunt, and touch screens have no hover.
- Transcript display preferences are per browser, not harness settings.
- **A message is written as markdown source and shown rendered once sent.**
  The composer marks the source up as it is typed with a styled copy drawn
  under a transparent textarea, not with a rich editor: `contenteditable`
  and CodeMirror each bring their own caret, undo, and input-method handling,
  and the textarea keeps the browser's. The cost is that the markup may only
  change colour, background, decoration, stroke, or a synthesised slant,
  never a glyph's width: a code block is coloured, not set in mono, and the
  composer spec fails on any run that moves. Italic stays only while no
  italic face is loaded.
- **The composer's keys are a browser preference, `chat` by default.** Enter
  sends, as every chat app does; the `markdown` mode trades that for an
  editor's Enter (list and quotation continuation) and sends on Ctrl+Enter.
  Its Tab nests list items and indents code, and elsewhere moves focus as
  usual; Shift+Tab with nothing to outdent does too, so the keyboard can
  always leave the box.
  Its edits are made with the deprecated `execCommand`, the only way to edit
  a textarea that its undo history records. Guessing an unlabelled code
  block's language costs tens of milliseconds a keystroke, so the composer
  colours only a block that names one.

## Testing

- `make check` is the only gate (there is no CI). It runs the `docker`-tagged
  tests, which skip with a reason without a daemon.
- **Visual tests run against a mock harness**, not the Go server, so every
  state is scriptable without Docker. They load a production build (a dev
  server per context was too slow) in parallel browsers. Screenshots only
  where pixels are the point, ARIA snapshots for what a screen says:
  baselines on every screen failed on each copy edit and made accepting them
  a habit.
- **The test stacks never use `eika-sandbox:latest`.** That tag is what a
  stack started with `make local` starts its computers from, so a test that
  read it failed whenever the user's image was older than the tree, and one
  that built it moved the user's image under a running harness. `make check`
  builds `sandbox/Dockerfile` as `eika-sandbox:test` (`sandbox-test`, a few
  seconds when cached, skipped without Docker) and passes it to the Docker
  tests in `EIKA_TEST_SANDBOX_IMAGE`; a bare `go test` falls back to `latest`.
  `make smoke` builds `eika-sandbox:smoke` and points its harness at it with
  `EIKA_SANDBOX_IMAGE`, as it tags the harness `eika:smoke`. Rejected: a
  required `make sandbox` before `make check`, which is the tag problem again.
- **The compose smoke test** (`make smoke`) is the one test of the real
  stack: its own compose project, port, and networks beside any running
  stack, with a scripted model (`web/e2e/smoke/model.ts`) standing in for a
  provider. It is not part of `make check` because it builds images.
- **Live tests** (`make live`) run scripted scenarios against a real model
  and are never part of `make check`: they need a GPU server, take a model's
  time, and a model's answers are not deterministic. They are Go tests in
  `evals/live` behind the `live` build tag, not a script or a separate
  program, so they share the test runner, the provider (`internal/provider/openai`,
  the code the harness runs) and the style of the Docker tests. Without
  `EIKA_LIVE_ENDPOINT` they skip, saying why. With it set but silent they
  fail with a pointer to `evals/models/README.md`, because a skipped live run
  reads as a pass. The package's plain code (env loading, report, scenario
  helpers) has no tag and ordinary tests on a fake server, and `make lint`
  compiles the tagged files, so the skeleton cannot rot between runs. A
  report is JSON in the ignored `evals/results/`, with the server's build,
  model revision and what the harness's own provider probe read (`/props` and
  `/v1/models` through it, not a parser of the live package's own), so a
  regression can be told apart from a server or model change. The `provider_probe`
  scenario checks the probe against `/slots`, a route it does not read. What no counter measures yet (drift) is
  `null` in the report, never 0. The token counts are the ones usage parsing
  reads (see "Usage counts"). Rejected: a shell
  or Python runner (a second way to talk to the provider, which would not
  exercise it); testing against a fake that mimics llama.cpp (the live suite
  exists to catch what fakes cannot); one key in the environment only (the
  bench's four variables live in a file, so `make live` works from any
  shell).

- **The bench runner changes the server only by what it can undo.** Server
  operations for the live suites live in `evals/bench`, behind a `Transport`
  (one SSH ControlMaster per run) so tests drive a fake host. A run records
  the GPU's holders and the running containers first and refuses with a
  `BusyError` unless only `llama-qwen` holds the GPU; a container is stopped
  only when named in `Options.Take`, and nothing else ever is. A changed
  model is a container of its own (`eika-bench-<run>`, `--restart no`, the
  chat container's image, mounts and network, the key through `--env-file` so
  it is never read), so `command.txt`, `komodo.toml` and the Deployment are
  never edited, and the chat container is only stopped and `docker start`ed,
  which keeps its id and flags, the facts Komodo reads. A container stopped
  with `docker stop` is not restarted by `unless-stopped` after a reboot, so a
  reboot mid-run brings back nothing the run took. The run writes a journal
  before each change (under the user cache directory), a lock on the server
  and a heartbeat that a detached watchdog watches; `Close`, an interrupt and
  the watchdog (after 3 minutes without a heartbeat) run the same idempotent
  restore, and a bench that still differs from its snapshot makes the next
  `Open` refuse. Rejected: editing the Deployment (Komodo's view would stop
  being true), restoring with `run.sh` (it removes the container), stopping
  whatever holds the GPU (the swap to `llama-server` was nobody's decision to
  repeat), and no watchdog (a killed process or a lost connection would leave
  the GPU swapped). Built and tested on a fake host only; not yet run against
  the server.
