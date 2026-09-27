# Decisions

Why Eika is built the way it is. One entry per decision: the choice, then the
reason. Read the relevant section before changing an area; add an entry
before implementing an architectural change (new package, dependency,
persistence shape, or interface). `ARCHITECTURE.md` says how things work;
this file says why, so neither repeats the other.

## Scope

- **Origin.** A Go port of the [Pi coding agent](https://github.com/badlogic/pi-mono)
  core (loop, tools, session trees, context files) plus sandboxed workspaces,
  subagents, web search, MCP, and a web UI, built so agents can modify Eika.
- **Deferred:** skills and templates, a Responses API provider, a forge API for pull
  requests (the UI builds a compare link from `remote_url`).
- **Extensibility is source-level.** Implement an interface, register it in
  one registry file, rebuild. No runtime plugin loader, no `init()`
  registration.
- **Single user.** One password; an optional `EIKA_AUTH_TOKEN` for scripts.

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
- `coder/websocket` (context-aware, maintained) and `creack/pty` (the
  standard library has no pty). The eikad watcher polls instead of adding
  fsnotify: trees are small and polling behaves the same on bind mounts.
- `golang.org/x/net/html`: the standard library has no HTML parser; a
  Markdown library would still need our container and sanitising rules.
- `dop251/goja`, imported by eikad alone, for web_fetch filters: the model's
  regular expressions need a real JavaScript engine. The harness never links it.
- MCP without an SDK: the official one brings oauth2 and a schema library
  and still leaves transport-in-sandbox, sealed storage, and UI to us.
- Frontend: `react-router`, `zustand`, `react-markdown` + `remark-gfm` +
  `rehype-highlight`, `cmdk`. The resizable split is ~80 lines of our own.
- `staticcheck` and `goimports` pinned by `tool` directives in `go.mod`.
- Deployment config is YAML (`gopkg.in/yaml.v3`) plus `EIKA_*` env, with the
  compose stack's values as defaults. Everything a user chooses is a
  database row edited in the UI; a fresh stack needs no `.env`.

## Sandboxes

- **Every agent action on files or processes runs in the workspace
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
- **The terminal and `/process` are not executor calls.** They go through
  `Workspaces`/`Host`, so a tool can run commands but never hold a PTY or a
  process.
- Sandboxes are on `eika_sandbox`, which only the harness also joins, so a
  sandbox cannot reach postgres or searxng. An empty `sandbox_network`
  publishes eikad on loopback for `make dev`.
- **The sandbox config is a row** (`workspaces.sandbox`): desired state,
  applied at create, on `PUT .../sandbox`, and on every start, so partial
  applies converge. Forks inherit limits and egress but not ports, so a fork
  cannot escape its parent's confinement; children share the parent's
  container, so they cannot either. Default: 4096 processes, nothing else.
- **Limits change live** with `ContainerUpdate`: recreating would reset
  everything outside `/workspace`. Docker cannot lift a CPU or memory limit,
  so "no limit" on an existing container means the whole host. Swap equals
  memory.
- **Egress is enforced by the network; the proxy is the one door.** A
  restricted sandbox moves to the internal network `eika_sandbox_internal`,
  whose only way out is the harness proxy on `:3128`. A proxy alone is
  advisory; DNS filtering leaks by address; iptables needs a capability the
  sandbox never gets. The proxy checks the allowlist per request and dials
  through `netguard`.
- The proxy identifies a sandbox by its workspace id and hub token: already
  per-workspace, secret, and recoverable after restart, so no grant table.
- Proxy variables go through eikad's `PUT /environment`, because a
  container's env is immutable and egress can change while it runs. Open
  sandboxes get no proxy, so they keep private addresses.
- **Previews are forwarded by the harness at `<port>-<id>.<host>`**: Docker
  port publishing needs a new container per change and does not work on an
  internal network. A separate origin keeps the page from reading the UI
  token; a one-time ticket becomes a host-only cookie, and every request is
  checked against the row.
- Usage is sampled every 5 s while the Sandbox panel is open: a measurement
  no event announces, so the no-polling rule does not apply.
- One address guard, `internal/netguard`, for web_fetch and the proxy.

## Git and the hub

- The hub is the single exchange point between containers: bare repos served
  over Smart HTTP. It mirrors local projects too, so forks and merges work
  the same for both kinds. A child agent shares its parent's repository and
  needs the hub only to publish its branch.
- A hub grant covers one project per workspace; a stopped workspace holds none.
- The hub lives in `internal/workspace/hub`: `workspace` is its only importer.
- Remote credentials are sealed, never in URLs, passed to git by a
  credential helper through the environment, and tried with a fetch before
  they are stored. No remote credential enters a sandbox: "push upstream"
  goes workspace → hub → remote.
- The hub remote in a workspace is `eika-hub`, not `origin`, which a local
  checkout already has.
- **The hub is never force-pushed.** A branch belongs to its creator.
  Unique child branches make this possible.
- A merge conflict is a normal `200` with `merged: false` and the paths; the
  tree stays conflicted for the user or agent to resolve.

## Persistence and sessions

- One id generator, `store.NewID` (20 base32 chars), generated before the
  row so one id names a row, container, volume, and URL.
- **One provider message is one entry.** An assistant message with tool
  calls stays one entry, so the tree never gains a branch point the loop
  cannot resume from.
- Entry kinds `user`, `assistant`, `tool_call`, `tool_result`, `system`,
  `event` are fixed vocabulary shared with the frontend.
- **A fork copies the path** into a new session and shares no rows, so
  deleting or editing one never reaches the other.
- **Only a resumable entry can be a head or fork point**: one whose path
  leaves no tool call unanswered. `session.PathResumable` is the one rule.
- `sessions.kind` (`user`/`fork`/`agent`) is a column so a missing
  `subagents` row cannot change what a session is.
- A chat is a session with `workspace_id` NULL: same tree, run manager, and
  view. Created explicitly (`chat: true`), and a workspace's sessions cascade
  with it rather than becoming chats.
- Every model call is a `model_requests` row holding the system prompt
  sections, schemas, and parameters; messages are not copied, since they are
  the session path.
- A cut-off response (`length`, `content_filter`) is kept and ends the turn,
  its tool calls dropped. Rolling back would delete the user's own message
  and make the model apologise for a turn it did take.
- Malformed tool arguments keep their exact text, quoted, with
  `arguments_malformed: true`, so the entry is always writable and the tool
  reports a normal argument error.

## Agent loop and providers

- **Retries live in the agent loop**; the SDK's are off
  (`WithMaxRetries(0)`), so the fake provider exercises the same path.
  Retryable: 408, 409, 429, 5xx, transport errors.
- **The context window is judged by measurement.** The next request's size
  is the last call's measured context plus a 4-bytes-per-token estimate of
  what was added since (Pi's rule). A request whose estimate reaches the
  window fails before the call; one that fits but leaves less than the
  requested max output gets max output lowered to what is left, since an
  endpoint refuses input plus max output over its window. The earlier byte
  bound (a token per byte) failed sessions at a quarter of their window.
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
  preview is the exact next request, not a second derivation. Section sizes
  are 4-bytes-per-token estimates, scaled to the last measured call.
- No package reads provider keys from the environment; the OpenAI provider
  sets key and base URL explicitly so `OPENAI_*` never applies. A key belongs
  to its base URL: changing the URL clears it, and probes elsewhere never
  carry it.
- Providers are built per use and never cached, so a changed key applies
  to the next run and a key is in memory only while used.
- Model names are unique across providers, because runs and `spawn_agent`
  name a model alone.

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
  subagent's single turn can outgrow the window, and compacting before the
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
  id: a fork copies entries under new ids, and a count survives the copy.
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

## Utility models

- **A utility model is a model row the user assigns to a harness task**
  (`utility_models`, task → model name), not a new kind of model, so it
  reuses the provider, key, and thinking switch already configured. The tasks
  are fixed in `internal/utility`. A task with no model does not run, so the
  harness never makes a call the user did not choose; falling back to the
  default model was rejected because that is often a large reasoning model,
  and a hidden call to it per session costs more than a title is worth.
- A utility call is one request outside any run: no tools, no history,
  thinking off through the model's `thinking_switch` (effort `none`). It is
  not a `model_requests` row, which records what a session's runs sent.
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
  columns on `sessions` and `workspaces`, edited with `PATCH` on the
  resource, and change nothing else: an archived session still runs, and an
  archived workspace's container is neither stopped nor started. Stopping on
  archive was rejected because it would abort runs as a side effect of
  tidying the sidebar; Stop is one menu item away. The listings return
  archived rows with the rest and the client splits them off, so one request
  still returns a workspace's whole session tree. A user's rename or filing
  goes out as `session.updated` rather than as `session.title`, which means
  "the harness named an untitled session".

## Tools

- **bash is the only file tool.** Models chain steps in one shell call, and
  one tool is less to describe than seven.
- One shared, stateless tool registry; the executor arrives per call.
- The built-in registry is `tool/builtin/registry.go` and provider kinds are
  `NewRegistry` parameters: both avoid import cycles.
- `tool.Standalone` marks a tool that needs no workspace; unmarked tools
  stay out of chats until their author opts in.
- Tool choices (profile or session) are lists where `mcp__<server>__*` means
  all of a server's tools, so tools a server adds later are included.
- Context files are discovered through the executor; the "global" file is a
  workspace path (`.config/eika/AGENTS.md`), since the harness filesystem is
  unreachable from a sandbox.
- `ask_user` ids are `q-<hex>`, not store ids: a question is never a row,
  and `tool` must not import `store`.

## Subagents

- A child is an ordinary run through `subagent.Runner` (implemented by the
  run manager), not a second loop.
- The spawner and server are built in a cycle: `subagent.New` takes no
  runner and `Spawner.Attach` supplies it before serving.
- Tools reach the spawner through `builtin.Subagents`, declared where
  consumed, so nothing under `tool` learns what a workspace is.
- Branch: `<parent>-<name>-<6 id chars>`. Git cannot hold both
  `refs/heads/main` and `refs/heads/main/x`, and the id tag keeps same-named
  children apart. Names match `^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`.
- **A child is a git worktree in its parent's container**, not a container of
  its own: a container per child cost a container, a volume, a full clone,
  and a push and fetch through the hub before it could start. A worktree
  shares the object store, starts in the time `git worktree add` takes, and
  puts the child's branch in the parent's repository the moment it commits.
  The cost is isolation between agents of one tree: they share processes,
  ports, limits, and (through bash) each other's files. They were already one
  user's agents under one confinement, and the parent's own files were
  already the child's starting point.
- A child is a **workspace row with `worktree_of`**, not a session with a
  directory: files, changes, terminal, merge, fork, and MCP keep working per
  workspace, and one function, `workspace.Locate`, maps a row to a container
  and directory. `worktree_of` names the container's workspace, never a
  worktree, so a tree is always one container deep.
- Worktrees live at `/workspace/.eika/worktrees/<id>`: eikad serves nothing
  outside `/workspace`, and a directory in the container's own layer would
  not survive a recreated container. The directory ignores itself rather than
  being added to `.git/info/exclude`, which in a local project is the user's.
  `worktree.useRelativePaths` keeps a local project's worktrees usable from
  the host where git supports it.
- A worktree workspace starts, stops, and is confined only through its
  holder; its own start, stop, and sandbox routes are refused rather than
  forwarded, so no request on a child quietly changes its parent. Deleting
  one while the holder is stopped is refused, since its files could not be
  removed and nothing would account for them.
- A child runs its parent's image by sharing its container; `spawn_agent` has
  no image parameter, since nothing validates a name a model invents.
- Limits are settings (`subagent_max_depth` 2, max 8;
  `subagent_max_children` 4, max 16), at least 1, read per spawn. A spawn
  reserves its slot under the counting lock before any slow work.
- A finished child's worktree is kept: the user looks at what it did. An
  aborted child still commits, pushes, and reports, on a context of its own
  bounded to five minutes.
- `Server.Close` aborts runs, then `Spawner.Shutdown` aborts children that
  outlive their parent (`wait: false`) before the pool closes.

## Server and API

- Composition lives in `server.Run`; `cmd/eika` stays thin. `Workspaces`,
  `Hub`, `Models` are interfaces declared in `server` so handler tests run
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
- Workspace edits (save, commit, push) republish `workspace.state` with the
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
  file in the hub volume, apart from the database, so a database copy alone
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
  workspace via eikad `/process`, one per workspace.
- Tools are `mcp__<server>__<tool>`, cut to 64 characters. Renaming a server
  rewrites tool choices in the same transaction; names hold no `__`.
- OAuth 2.1 per the MCP spec. The redirect lands on a frontend route that
  posts to the authenticated API, so no unauthenticated route receives the
  code. Registration order: user client id, Client ID Metadata Document (needs
  an https `public_url`), dynamic registration.
- Elicitation is supported; roots, sampling, and logging are deprecated in
  `2026-07-28` and not declared.

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
- Rewind is in the transcript, forking in the tree panel. Nothing is deleted.
- Forks and child agents nest under their parent session in the sidebar.
- Destructive actions confirm in `ConfirmDialog`, not `window.confirm`.
- Transcript display preferences are per browser, not harness settings.

## Testing

- `make check` is the only gate (there is no CI). It runs the `docker`-tagged
  tests, which skip with a reason without a daemon.
- **Visual tests run against a mock harness**, not the Go server, so every
  state is scriptable without Docker. They load a production build (a dev
  server per context was too slow) in parallel browsers. Screenshots only
  where pixels are the point, ARIA snapshots for what a screen says:
  baselines on every screen failed on each copy edit and made accepting them
  a habit.
- **The compose smoke test** (`make smoke`) is the one test of the real
  stack: its own compose project, port, and networks beside any running
  stack, with a scripted model (`web/e2e/smoke/model.ts`) standing in for a
  provider. It is not part of `make check` because it builds images.
