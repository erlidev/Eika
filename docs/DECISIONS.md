# Decisions

Why Eika is built the way it is. One entry per decision: the choice, then the
reason. Read the relevant section before changing an area; add an entry
before implementing an architectural change (new package, dependency,
persistence shape, or interface). `ARCHITECTURE.md` says how things work;
this file says why, so neither repeats the other.

## Scope

- **Origin.** A Go port of the [Pi coding agent](https://github.com/badlogic/pi-mono)
  core (loop, tools, session trees) plus sandboxed computers,
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
  applied at create, on `PUT .../sandbox`, and on every start, so partial
  applies converge. Children share the parent's container, so they cannot
  escape its confinement. Default: 4096 processes, nothing else.
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
  was the hub token until the hub was due to go; phase 4 replaces it with
  Sentry's principals.
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
  restart only ever delays a stop. Rejected: Docker's own restart policies
  and a timer in eikad, which see processes, not runs.
- Usage is sampled every 5 s while the Sandbox panel is open: a measurement
  no event announces, so the no-polling rule does not apply.
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
- **Nothing deletes its files.** The API refuses to delete it (`409`). A
  volume that exists when `Host.Create` runs is reattached, and a `Create`
  that fails does not remove a volume it found, so a container lost to
  `docker rm` comes back over its files, and a failed attempt (the image
  missing) cannot take them. Nothing else recreates a container: limits and
  egress change the running one (`ContainerUpdate`, network connect), and a
  harness restart leaves containers alone.
- **A run starts a stopped `home`.** The idle stopper stops `home` like any
  computer, and the next run, which needs it, starts it (`runExecutor`): a
  stopped computer is a `409` the user clears by starting it, and nothing in
  the UI starts a computer any more. A run in `home` whose container is gone
  calls `EnsureHome`. Any other stopped computer still refuses the run, as a
  user who stopped it meant. Rejected: starting at the first `bash` call,
  which would put a lazy start inside the executor every tool shares, for a
  computer the run is about to use anyway; and starting every computer, which
  would override a stop the user asked for.
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
  `POST /api/computers` takes a name (and optionally an image and sandbox),
  which keeps a sandboxed `bash` reachable until the `home` computer replaces
  it. "Project" comes back in phase 2 with a new meaning.
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
- **A fork copies the path** into a new session and shares no rows, so
  deleting or editing one never reaches the other.
- **Only a resumable entry can be a head or fork point**: one whose path
  leaves no tool call unanswered. `session.PathResumable` is the one rule.
- `sessions.kind` (`user`/`fork`) is a column, not a join, because it is
  what the session is.
- A chat is a session with `computer_id` NULL: same tree, run manager, and
  view. Created explicitly (`chat: true`), and a computer's sessions cascade
  with it rather than becoming chats.
- **Fresh baseline schema.** Turning Eika into an assistant drops most of the
  coding schema, so its 17 migrations were squashed into `0001_baseline.sql`
  and existing databases are not migrated: a deployment starts fresh. Until
  phase 0 closes the baseline is edited in place; after that, migrations are
  append-only again. Phase 4 starts fresh once more for Sentry.
- **Schema `core`.** Core's tables live in their own schema rather than
  `public`, so Sentry's schema can sit beside it in the same database with
  its own grants.
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
- Model names are unique across providers, because runs name a model
  alone.

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
  rejected: forks copy entries, and shared rows would need reference counting
  for no gain at 1080p sizes.
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
  `PLAN.md` says where the pieces worth reusing live in history.

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
- Forks nest under their parent session in the sidebar.
- Destructive actions confirm in `ConfirmDialog`, not `window.confirm`.
- A sidebar row's quick buttons (a computer's start or stop and new
  session) always show; hiding them until hover made starting a computer a
  hunt, and touch screens have no hover.
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
