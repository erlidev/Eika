# Architecture

How Eika works today. Update it in the same change that moves structure.
Reasons for the design are in `DECISIONS.md`; wire details in `api/`.

## Domain model

```
Project    A git repository: a bare repo in the harness hub, plus a remote
           (GitHub etc.) or a host directory.
Workspace  A sandbox container + volume holding a clone of a project on a
           branch. States: creating -> running -> stopped (gone if the
           container disappeared).
Session    A tree of entries with a head pointer, in one workspace.
Chat       A session with no workspace: only tools that need none.
Run        One execution of the agent loop from a session's head.
Subagent   A child run in its own workspace (cloned from the parent's commit
           on a child branch) and session; reports back as a tool result.
Profile    A named, nullable configuration of what a run sends.
```

Every agent action on files or processes happens inside a workspace container
through `executor.Executor`. Model calls, search, fetch, remote MCP, and the
egress proxy are network calls and run in the harness.

## Packages

| Package | Responsibility |
|---|---|
| `cmd/eika` | Loads config, calls `server.Run`; `-web <dir>` serves the built frontend with an `index.html` fallback |
| `cmd/eikad` | Sandbox daemon; `eikad filter` runs a web_fetch filter |
| `config` | Deployment config: compose defaults, optional YAML, `EIKA_*` overrides, validation, redacted `String()` |
| `secret` | Seals stored credentials (AES-256-GCM) |
| `event` | Envelope, type constants, payload structs, `Emitter`, fan-out `Bus` |
| `server` | Composition, JSON API, event stream, auth, run manager; `server/servertest` builds and checks `docs/api/contract.json` |
| `agent` | The loop: turns, tool dispatch, queues, retries, context assembly |
| `provider` | Model interface, kind registry, `Complete`; `openai` implementation; `providertest` scripted fake |
| `utility` | The harness's own one-shot model tasks (session titles), sent to the model a task is assigned |
| `tool` | Tool interface, call context, registry; `tool/builtin` holds the built-ins and `limits.go` |
| `executor` | The interface every agent action uses, path validation; `local` (tests only), `sandbox` (eikad client) |
| `contextfile` | AGENTS.md discovery and its system prompt section |
| `eikad` | Daemon handlers, path confinement, wire types |
| `workspace` | Container and volume lifecycle, confinement, usage; `workspace/hub` serves bare repos |
| `egress` | Egress modes, allowlist patterns, the proxy |
| `netguard` | Public-address-only dialing |
| `store` | PostgreSQL pool, embedded migrations, queries |
| `session` | Session tree: append, head, branch, fork, outline; `session.Store` records runs |
| `subagent` | Spawning children and collecting their results |
| `search` | Failover chain, quotas, cache; backends in `web`, `wikipedia`, `arxiv`, `github`; `fetch` reads a URL; `filter` is the JS filter |
| `mcp` | MCP client, transports, pool, elicitation; `mcp/oauth` authorization; `mcptest` fakes |
| `web/` | The frontend |

Health: `GET /healthz` (container check) and `GET /api/healthz` (frontend)
return `{"status":"ok"}`. `/api` is the JSON API, `/git/` the hub.

## Agent core

```
Run(ctx, session, text)
  turn: append user message -> turn.start
    provider.Stream(request) -> message.delta / reasoning.delta
    no tool calls? -> turn.end
    each call in order: tool.call -> executor -> tool.output* -> tool.result
    deliver queued steering messages, call the model again
  after the turn: queued follow-ups start the next turn
```

- **provider** turns a `Request` into a channel of `Event`s (text and
  reasoning deltas, tool calls, usage with optional `Timings`, done or
  error). `openai/timings.go` reads the speed fields of llama.cpp, vLLM, NIM,
  LM Studio, TabbyAPI, Groq, and Ollama. Usage is relayed as soon as a chunk
  carries it.
- **tool**: a call gets a `CallContext` (executor, emitter, ids). A
  `tool.Standalone` tool runs with a nil executor in chats;
  `Registry.Filter` narrows the shared registry per run. `bash` is the only
  file tool. `ask_user` registers with the `Questions` broker, emits
  `question.asked`, and waits for the API to deliver an answer.
- **executor**: `executor.Resolve` rejects paths leaving the root;
  `executor/local` has the `!prod` tag.
- **contextfile** reads AGENTS.md (CLAUDE.md fallback, AGENTS.override.md
  replacement) from the workspace root down to the working directory.
- **Context assembly** (`agent/context.go`) builds every request as an
  `agent.Context`: system prompt sections (base prompt, context files,
  instructions), tool schemas (built-in or MCP), messages, parameters, and a
  size estimate for each. Runs and `Agent.Preview` share it; a `Recorder`
  sees every call that returned.

Queues follow Pi: steering joins after the running tool, before the next model
call; follow-ups wait for the turn to end (`QueueOneAtATime` or `QueueAll`).
Aborting returns an unanswered message to its queue and answers every pending
tool call so the session stays valid. Messages accepted after an abort move to
the session's next run.

Retryable failures (408, 409, 429, 5xx, transport) back off exponentially up
to `MaxRetries`, honouring `Retry-After`, and emit `message.reset`. Others end
with `run.error`. A request that cannot fit the model's context window (a
conservative estimate plus requested output) fails before the call.

## Persistence

`store` owns the database: a pgx pool, embedded migrations applied by `Open`,
and SQL in one file per entity. Missing rows are `store.ErrNotFound`,
duplicates `store.ErrConflict`. Ids are `store.NewID` text.

| Table | Holds |
|---|---|
| `projects` | name (unique), kind (remote/local), remote_url, remote_username, remote_password (sealed), host_path, default_branch |
| `workspaces` | project, name, branch, base_commit, image, state, container_id, parent_workspace_id, sandbox (jsonb: limits, egress, ports) |
| `sessions` | workspace_id (NULL for a chat), title, untitled, kind (user/fork/agent), head_entry_id, parent_session_id, tools, profile_id, overrides (jsonb) |
| `session_entries` | session, parent_id, seq, kind, payload (jsonb), commit_sha |
| `runs` | session, state, started/finished, error |
| `subagents` | parent/child session, child workspace, state, result |
| `settings` | key → jsonb value |
| `providers` | name (unique), kind, base_url, api_key (sealed) |
| `models` | provider, name (unique), model (endpoint id), context_window, max_output, reasoning_effort, reasoning_efforts, thinking_switch, preserve_thinking |
| `profiles` | name, description, model_id, workspace_prompt, chat_prompt, instructions, context_files, preserve_thinking, tools, sampling (jsonb); NULL = not set |
| `model_requests` | session, run, entry, model, sections, tools, parameters (jsonb), token counts |
| `auth_password`, `auth_sessions` | PBKDF2 hash; session tokens by SHA-256 |
| `search_keys`, `search_usage` | sealed search keys; quota counters and cooldowns |
| `mcp_servers`, `mcp_credentials` | configured servers (sealed headers/env); OAuth client and tokens (sealed) |

Everything cascades from `projects`; chats are deleted on their own; MCP
credentials cascade from their server. `sessions.head_entry_id` has no foreign
key (it would be a cycle); `SetSessionHead` checks ownership in its `UPDATE`.

`AppendEntry` locks the session row, inserts with the head as parent and
`max(seq)+1`, and moves the head, so concurrent appends form one chain.
`ForkSession` copies entries into a new session.

### Session trees

`session.Tree` appends, reads `Path` (root to head), moves the head, forks,
lists `Children`, and renders an `Outline`. `session.Store` implements
`agent.Store`; `Load` rebuilds a conversation from the path.

`user`, `assistant`, and `tool_result` entries are the conversation; `system`
and `event` entries are shown but not sent. An assistant entry holds the
message JSON (with tool calls, reasoning, and measured `metrics`) and the
workspace HEAD commit it was produced at.

```
branch in place (head moved to e2)     fork at e2 (path copied)
  e1                                      A: e1 - e2 - e3 - e4
  e2                                      B: e1' - e2' - e5'
  |\
  e3 e5 <- head
  e4
```

Both require a resumable entry (`session.PathResumable`: no tool call left
unanswered); the outline reports it per node. Forks get kind `fork`, subagent
sessions `agent`; `Store.Sessions` with descendants follows
`parent_session_id` recursively for the sidebar. Fork-with-workspace pushes
the workspace, clones a new one at the fork entry's commit on
`<branch>-fork-<id>`, and points the fork at it.

### Chats

A chat's run gets no executor and no commit function. `sessionTools` drops
every tool that `NeedsWorkspace`, then applies the tool choice, so the model is
never offered a workspace tool; the loop also refuses one. The system prompt
is the chat prompt without context files. web_fetch refuses filters in a chat.
A chat's fork is a chat.

## Server

```
cmd/eika -> server.Run
  store.Open, secret.Load, hub.New, workspace.NewHost,
  provider.NewRegistry(openai.New), builtin.Registry, event.NewBus, NewMCP
  -> server.New(Deps) -> Reconcile -> Serve

POST /api/sessions/{id}/messages          GET /api/events
  runs.start: StartRun, session.Store,      bus.Subscribe(topics)
  configure + newAgent                             ^
  goroutine agent.Run --entries--> postgres        |
                      --events---> event.Bus ------+ one buffered channel
  FinishRun(done | error | aborted)                  per connection
```

- **Deps**: store, hub, workspace host, provider registry, secret box, tool
  registry, question broker, bus, MCP pool. `Workspaces`, `Hub`, and
  `Providers` are interfaces declared here.
- **Auth** is one middleware over `/api`. A bearer token is a sign-in
  session (looked up by SHA-256) or the API token (constant-time compare).
  Public: health, `GET /api/auth/status`, `POST /api/auth/setup` (once),
  `POST /api/auth/login`, `/oauth/client-metadata.json`. `/git/` has its own
  auth. `/api/events` and the terminal also accept `?token=`. Sign-in
  attempts are serialised.
- **Configuration** (`configuration.go`): `configure` loads models,
  profiles, and defaults; `resolve` takes each value from the first layer
  that sets it and records the layer:

  ```
  request model -> session overrides + tools -> profile -> model row -> default
  ```

  The model row supplies `max_output`, `reasoning_effort`, and
  `preserve_thinking`. `inherited` is the same resolution without one
  layer's values, for editors. `toolChosen` resolves tool choices against
  the registry and MCP pool. `newAgent` turns a configuration into
  `agent.Options`, used by runs and the context preview alike.
- **Settings** the harness reads (`default_model`, `default_profile`,
  `utility_models`, `sandbox_image`, `sandbox_limits`, `sandbox_egress`,
  `subagent_max_*`, `search_order`, `search_limits`, `setup_complete`) are
  validated on write and fall back to defaults on read. Renaming a model
  rewrites the settings that name it.
- **Utility models** (`titles.go`): a session created without a title is
  `untitled` and called `New session` or `New chat`. When a run begins on
  one, `titles` starts a goroutine (at most one per session, cancelled and
  joined by `Close`) that reads the model `utility_models` assigns
  `session_title`, builds its provider, and calls `utility.Title` with the
  first user message: one request, no tools, effort `none` in the model's
  thinking switch when it offers `none`. `TitleUntitledSession` writes the
  title only if the session is still untitled, then `session.title` goes out
  on `global`. No model, a failure, or a timeout leaves the session untitled
  for the next run.
- **Errors**: one shape, `{"error":{"code","message"}}`. `statusOf` maps
  package sentinels to 404/409/400; `mcpFailure` reports MCP server failures
  as 400. Anything unmapped is logged and returned as `internal error`.
- **Run manager**: one goroutine per run, one run per session (a
  placeholder is reserved under the lock before slow work). The client gets
  the run row immediately and follows events. Assistant entries record HEAD
  via `git rev-parse HEAD` through the executor. `FinishRun` retries on a
  context that outlives the run and continues in the background past 30 s.
  Startup aborts stale `running` rows. Stopping or deleting a workspace or
  session, and shutdown, abort the runs involved first.
- **Bus**: `Emit` never blocks; a slow subscriber loses events and is told
  with `bus.dropped`. Topics: `global`, `workspace:<id>`, `session:<id>`.
- **Event stream**: a reader goroutine handles `subscribe` and
  `session.replay`; replays write straight to the socket.

Routes are in `routes.go`, one handler file per resource.

## Configuration

Deployment config (`config.Config`) is loaded once in `main`:
`config.Default()` → YAML from `-config` → `EIKA_*`. Defaults match the
compose stack, which sets only `EIKA_DATABASE_URL`. Notable fields:

- `sandbox_network` (`eika_sandbox`): empty means eikad is published on
  loopback (`make dev`). `sandbox_internal_network`
  (`eika_sandbox_internal`) must differ from it and, with `egress_listen`
  (`:3128`) and `egress_proxy_url` (`http://eika:3128`), enables restricted
  egress. Either network empty disables it.
- `allowed_origins`: extra origins for the event stream and MCP redirects,
  after the one derived from `listen`.
- `public_url`: when https, serves the OAuth Client ID Metadata Document.
- `auth_token`: optional fixed API token.
- A file that still sets `models` or `subagents` is refused with a pointer
  to the UI.

Everything a user chooses is a database row. Stored credentials are sealed
with the 32-byte key in `secret_key_file` (`/var/lib/eika/secret.key`,
created on first start). The API reports only whether a credential is set
and, for long keys, the last four characters.

## Sandboxes

```
 harness (eika)                              workspace container eika-ws-<id>
  tool -> executor/sandbox --HTTP, bearer--> eikad :7000
  mcp.Pool -> Host.Process --------------->    /exec /files /stat /list
  terminal relay ------------------------->    /pty /watch /process /environment
  workspace.Host ---------docker socket--->  container + volume at /workspace
  egress.Proxy :3128 <-----------------------  HTTP(S)_PROXY, when restricted
  previews <port>-<id>.<host> ------------>    a forwarded port
  hub.Handler /git/ <--------------------------  clone / push, per-workspace token
        |
        v git fetch / push with sealed credentials
  upstream remote
```

### eikad

Reads `EIKAD_TOKEN`, refuses to start without it, and serves via
`server.Serve`. Every path is confined to `/workspace`: `..` is rejected, the
longest existing prefix is resolved through symlinks before comparison, and
the canonical path is what is opened. `EIKAD_TOKEN` is stripped from command
environments. A command leads its own process group, killed on timeout;
output is capped and reported as truncated. The watcher polls modification
times. API: `api/eikad.md`.

`executor/sandbox` is an HTTP client for one daemon; `Exec` streams the
daemon's newline-delimited frames into the caller's writers. A 404 is
`sandbox.ErrNotFound`.

### Files, terminal, changes

File routes call the executor after `executor.Resolve` (eikad re-checks after
symlinks): refused paths are 403, missing 404. Commit, diff, and push run git
through `Exec`; push goes through `Host.Push` to the hub, and an upstream push
then runs `hub.Push` with the project's credentials. Saves, commits, and
pushes publish `workspace.state`.

The terminal relays a browser WebSocket to eikad `/pty` through
`Host.Terminal` (never the executor). The sandbox is dialled before the
browser handshake is accepted, so a stopped workspace is an ordinary HTTP
error. Frames and close codes pass through unchanged; a drop without a close
frame closes the other side with 1011; messages are capped at 1 MiB.

### Workspace lifecycle

`workspace.Host` wraps the Docker client. `Create` builds the image if the
spec has a build context, creates volume `eika-ws-<id>` (or bind-mounts the
local project's *host* path), creates the container labelled
`eika.workspace=<id>` with its sandbox settings, and copies the harness's
static eikad to `/usr/local/bin/eikad` as the entrypoint. `Start` waits for
`/healthz`. `Stop` keeps the volume; `Destroy` removes container, volume, and
a per-workspace image. The harness reaches eikad at `http://eika-ws-<id>:7000`.

`List` finds containers by label and `Inspect` recovers tokens from the
container's environment. `Reconcile` updates recorded states at startup and
marks a workspace `gone` only when Docker says the container does not exist;
any other error stops reconciliation without changing states.

### Limits, egress, previews

`Host.Create` applies the workspace's `sandbox` row; `PUT .../sandbox` writes
the row then `Host.Confine`s; every start re-applies it. Children and forks
get the parent's limits and egress (`server.ChildSandbox`).

Limits are `NanoCPUs`, `Memory` (= `MemorySwap`), and `PidsLimit`, changed
live with `ContainerUpdate`; "no limit" on an existing container is the host's
capacity (`Host.Capacity`). `Host.Usage` samples `docker stats`;
`GET .../usage` adds hosts the proxy refused.

```
 open: eika-ws-a on eika_sandbox --------------------> internet directly
 allowlist/none: eika-ws-b on eika_sandbox_internal (no route out)
     HTTP(S)_PROXY=http://<ws id>:<hub token>@eika:3128
     -> egress.Proxy -> allowlisted public hosts only
```

`egress.Proxy` takes `CONNECT` and absolute `http://` requests, identifies the
sandbox from `Proxy-Authorization`, asks `Server.EgressPolicy` per request,
dials through `netguard`, and answers refusals with 403, remembered per
workspace (`Proxy.Blocked`). It serves only when both networks exist
(`Host.EgressControl`). Switching modes connects the container to the new
network before leaving the old. Proxy variables (`HTTP_PROXY`, `HTTPS_PROXY`,
`NO_PROXY` in both cases, `NODE_USE_ENV_PROXY`) are given to eikad with
`PUT /environment` on every change and start.

A preview is served at `<port>-<workspace id>.<host>`: `Server.Handler`
routes such hosts before the API. `POST .../ports/{port}/preview` returns a
link with a one-time ticket, traded for a host-only cookie; every request is
checked against the row. Tickets and sessions are in memory.

### The git hub

One bare repo per project at `<hub_root>/<project>.git`, served at
`/git/<project>.git` by `git http-backend`. Workspaces authenticate with basic
auth (workspace id, per-workspace token from `Grant`, withdrawn by `Revoke`);
a grant covers one project. `Host.Clone` configures a credential helper from
`EIKA_HUB_USER`/`EIKA_HUB_TOKEN`, validates names (`git check-ref-format`),
passes them as arguments, and returns the base commit. `Mirror` fetches a
remote into the hub and `Push` sends a refspec back, with credentials passed
through the environment to a credential helper.

## Subagents

```
parent run -> spawn_agent -> spawner
  commit parent tree "wip: before spawning <name>", push to hub
  Host.Create + Start, CloneAt(<parent>-<name>-<id>, base commit)
  rows: workspace, session (kind agent), subagents; subagent.started
  runs.runChild(child session, task)  -- events on session:<child>
  on end: commit "wip: subagent <name> finished", push, diff --stat base..head
  Host.Stop, FinishSubagent, subagent.finished
  -> tool result: summary, branch, commit, diffstat
```

- Tools `spawn_agent` (blocks unless `wait: false`), `wait_agents`, and
  `list_agents` call the spawner through `builtin.Subagents` and only name
  their own session's children.
- `Host.Push` creates the hub repo if needed and adds it as `eika-hub`.
- Depth is measured by walking `subagents` rows up; width counts running
  children plus live reservations.
- Aborting a run aborts its children recursively; the spawner waits for a
  child's loop to stop, then commits, pushes, and reports whatever the state.
  A child without a closing message gets one written from its state and
  diffstat. The result is stored on the row, served by
  `GET /api/sessions/{id}/agents`, and carried by `subagent.finished`.
- `POST /api/workspaces/{id}/merge`: the source pushes, the target fetches
  and merges or rebases in its own workspace.

## Search

```
web_search -> search.Engine
  "web": first usable of search_order (searxng, exa, tavily, brave, marginalia)
  wikipedia | arxiv | github_code / github_repos / github_issues
  Tracker: quotas and cooldowns per bucket -> search_usage; keys from search_keys

web_fetch -> fetch.Reader -> plan URL: GitHub API/raw | text file | HTML
  HTML: doc-generator container, else readability, else body -> Markdown
  cache -> section -> filter (eikad filter in the sandbox) or budget
```

- A search asks for a pool of 30, cached a day; the model sees at most 10.
  A failing provider records an attempt and the next is tried; when SearXNG
  failed and a fallback answered, the result opens with a `Notice:` line.
  An empty `search_order` disables web search.
- arXiv is paced 3 s apart. GitHub sources and web_fetch's GitHub reads share
  the `github` bucket. The GitHub token is sent only to the GitHub API and
  `githubusercontent.com` over https.
- Pages are cached 6 h. Sections match by heading (exact, prefix,
  substring). Past 10,000 tokens a plain read returns the outline; a narrowed
  one is cut on a section boundary.
- Filters run as `eikad filter` through the run's executor, in goja with a
  2 s limit, budgeted inside the sandbox.
- `fetch.NewClient` dials only public addresses (checked after DNS, per
  redirect, at most five) and uses no proxy.

The Search settings tab shows each backend's state and usage, orders
providers, stores keys, edits quotas, and tries `POST /api/search`.

## MCP

```
runs.begin -> mcp.Pool.Tools(ctx, workspace)
  remote: one conn per server, ConnectHTTP
    modern (server/discover) -> initialize -> HTTP+SSE; Bearer from mcp_credentials
  stdio: one conn per server per workspace
    Launcher -> Workspaces.Process -> Host.Process -> eikad /process -> command
  catalog: tools, resources, templates, prompts
  events: mcp.server on global, mcp.elicitation on session:<id>
```

- **Eras**: modern requests carry version and capabilities in `_meta`; an
  `input_required` answer is satisfied and resent, up to eight rounds.
  `Client.Info` records era, transport, and version.
- **Transports**: `http.go` (Streamable HTTP, modern `Mcp-*` headers, older
  session ids), `legacysse.go` (same-origin endpoint), `stdio.go`
  (newline-delimited JSON).
- **Pool**: remote servers connect in the background at start; concurrent
  callers share an attempt; a failure stands for 30 s. Modern connections
  hold `subscriptions/listen` open. `Store` (implemented by `server` as
  `mcpStore`) keeps sealed values away from the pool.
- **Stdio servers** end with their workspace (after its runs). Their
  environment is visible to the agent.
- **Tools**: `Pool.Tools` waits at most 15 s and returns
  `mcp__<server>__<tool>` (hashed to 64 chars), plus `mcp_list_resources`
  and `mcp_read_resource` when a server has resources. Remote tools are
  standalone; stdio tools need a workspace. `Pool.Offered` lists tools from
  the last listing without connecting (for `GET /api/tools` and previews).
  Progress becomes `tool.output`; images and audio are replaced in the model's
  text and kept (≤ 4 MiB) in `tool.result` details. An expired session or
  refreshed token gets one retry.
- **Authorization** (`auth.go`, `oauth`):

  ```
  POST .../authorize {redirect_uri}  -> check <UI host>/mcp/callback
    discover: WWW-Authenticate, resource metadata, RFC 8414 / OpenID
    client: configured id | metadata document | dynamic registration
    PKCE S256, state, scope, resource; pending 10 min -> {authorization_url}
  browser signs in -> /mcp/callback -> POST /api/mcp/oauth/callback
    check iss (RFC 9207), exchange, seal tokens, reconnect
  ```

  Tokens refresh a minute before expiry and after a 401; `insufficient_scope`
  steps up with the union of scopes; signing out revokes both tokens.
- **Elicitation**: `mcp.Elicitations` registers the request, emits
  `mcp.elicitation`, and waits for `POST /api/elicitations/{id}/answer`.
  Server stderr and log notifications go to a 200-line per-server log.

Bounds are in `internal/mcp/limits.go`.

## Compose

```
              127.0.0.1:8080
                    |
 host.docker. <-- eika (harness) ----- searxng     (eika_default)
 internal           |      \---------- postgres 16 (volume eika-postgres)
 (local models)     | docker.sock: sibling sandboxes
                    | volume eika-hub -> /var/lib/eika (hub/, secret.key)
                    +-- eika_sandbox / eika_sandbox_internal: sandboxes only
```

Only the harness publishes a port, on loopback. `compose.dev.yaml` publishes
postgres and searxng on loopback for `make dev`; `compose.smoke.yaml` runs the
stack for `make smoke`. The `sandbox-image` service builds
`eika-sandbox:latest` and exits; the harness waits for it. The root
`Dockerfile` builds the frontend, both static Go binaries, and a runtime image
with eikad at `/usr/local/share/eika/eikad`. `deploy/eika-entrypoint.sh`
joins the socket's group and drops to uid 1000. `deploy/searxng/settings.yml`
enables JSON results.

## Frontend

```
web/src/
  app/         routes, shell, panel registry, sidebar, command palette, theme
  features/    one folder per feature, imported through its index.ts
  components/  shared components; components/ui is shadcn
  api/         wire types, HTTP client, event stream
  lib/         pure utilities
```

- **Entry** (`app/App.tsx`): no token → `GET /api/auth/status` → setup
  (`features/setup`) or sign-in (`features/connect`). With a token the setup
  shows until a password exists and `setup_complete` is true.
- **Settings** (`features/settings`): providers and models, profiles,
  defaults and utility models, sandbox, search, MCP, password. The profile editor
  (`features/profiles/SettingsEditor`) has Model, Prompt, Tools, and Sampling
  tabs, a cost strip, and a chip per field naming where its value comes from.
  `ToolPicker` is shared by profiles, sessions, and chats. Small stores hold
  which editor and settings tab are open, so links elsewhere can open them.
- **Workbench**: sidebar (projects → workspaces → sessions, forks and child
  agents nested; chats below), session (transcript, status bar, composer),
  and a tab strip of panels from `app/panels.tsx`. Workspace sessions get
  Files, Terminal, Changes, and Sandbox; chats get Tools; every session gets
  Tree, Run, and Context (window fill, token bar by part, key parameters; the
  Context inspector dialog shows a request whole with links to the layer
  behind each part). `ResizableSplit` remembers widths; below 1024px side
  panes become drawers. Heavy panels (Monaco, xterm) load lazily.
- **Server state** is TanStack Query over `api/routes.ts`, invalidated by
  events (`useWorkspaceEvents`, `useSessionStream`, `useMCPEvents`,
  `useSessionTitles`).
- **Stream state**: `features/session/store.ts` wraps the pure reducer
  `transcript.ts`, fed by run events (`run_id`) and replayed
  `session.message`s (`entry_id`). It also tracks pending questions and
  elicitations and the status bar's meter (usage, window, timings).
  Display preferences are in `features/session/preferences.ts`
  (localStorage).
- **API**: `api/client.ts` is the only `fetch` caller (token, `ApiError`,
  forgets the token on 401); `api/connection.ts` stores token and URL;
  `api/stream.ts` is the one WebSocket with reference-counted topics.
- **Tool cards**: `features/session/renderers/renderers.tsx` maps tool names
  to a summary and a body (`bash`, `ask_user`, `web_search`, `web_fetch`,
  `mcp_*`); unknown tools render as JSON.

In development Vite serves :5173 and proxies `/api` to :8080; in production
the harness serves the bundle.

## Dependency direction

An arrow means "may import".

```
 cmd/eika -> server                     cmd/eikad -> eikad, search/filter
 server -> agent, session, store, workspace, subagent, search, mcp, egress,
           utility
 agent -> tool, provider, contextfile     session -> agent, store
 utility -> provider
 tool -> executor                         contextfile -> executor
 executor/sandbox -> eikad (wire types)   workspace -> executor, executor/sandbox, hub
 subagent -> workspace, session, store, tool/builtin
 tool/builtin -> search, search/fetch     search/fetch, egress -> netguard
 mcp -> tool, event, mcp/oauth            config, event, secret: leaves
```

- Nothing imports `server` except `cmd/`.
- `tool` never imports `workspace`; tools reach a workspace only through an
  executor. `mcp` never imports `workspace` or `executor`: the server hands
  it a `Store` and a `Launcher`.
- `search` imports nothing from `internal/`; goja (`search/filter`) is linked
  into eikad only.

## Testing

`make check` runs gofmt/goimports checks, `go vet`, staticcheck,
golangci-lint if installed, the Go tests including `docker`-tagged ones,
ESLint, `tsc`, Vitest, then the visual suite. Go targets name
`./cmd/... ./internal/...` because `web/node_modules` contains Go files.

- Go: stdlib tests; `providertest` and `executor/local` make the loop
  testable without Docker or a model. `docker`-tagged tests (handlers, store,
  workspaces) use `storetest` and skip without a daemon.
- Contract: `docs/api/contract.json` is generated from the wire types; handler
  tests check responses against it and the mock harness checks requests,
  responses, and events.
- Frontend: Vitest for logic; Playwright specs in `web/e2e/` against the mock
  harness (`make visual`); `npm run shot` for screenshots.
- `make smoke`: the real stack from setup to a sandboxed bash call.
