# Architecture

This file describes Eika as it is today, after phase 8, terminal, editor, and diff, and the sandbox confinement of phase 9. It is
updated in the same change that moves structure. Planned work lives in `docs/PLAN.md`.

## What exists now

Two Go binaries, the agent core, the sandbox machinery agents run in, the
database that outlives them, the HTTP API and event stream that drive all of
it, child agents that work in sandboxes of their own, web search and page
reading, MCP servers whose tools runs offer, one frontend, and a
three-service compose stack.

| Piece | Path | Responsibility today |
|---|---|---|
| `eika` | `cmd/eika` | Loads configuration, serves HTTP, optionally serves the built frontend |
| `eikad` | `cmd/eikad` | Sandbox daemon: exec, files, terminal, change watcher, long-lived processes; `eikad filter` runs a web_fetch filter |
| `config` | `internal/config` | Deployment configuration: defaults for the compose stack, an optional YAML file, `EIKA_*` overrides, validation, redacted `String()` |
| `secret` | `internal/secret` | Seals the credentials the UI stores, API keys and git passwords, with a key kept apart from the database |
| `event` | `internal/event` | The event envelope, the type name constants, the payload structs, the `Emitter` interface, and the fan-out `Bus` |
| `server` | `internal/server` | Composition of the process, the JSON API, the WebSocket event stream, sign-in and bearer auth, provider and model management, and the run manager; `server/servertest` reads the wire types' shapes into `docs/api/contract.json` and checks bodies against it, for tests on both sides of the API |
| `agent` | `internal/agent` | The agent loop: turns, tool dispatch, steering and follow-up queues, retries, events |
| `provider` | `internal/provider` | The model interface, message and event types, the kind registry, the OpenAI implementation, the scripted fake |
| `tool` | `internal/tool` | The tool interface, call context, result type, and registry; `tool/builtin` holds the thirteen built-in tools |
| `executor` | `internal/executor` | The interface every agent action goes through, path validation, and `executor/local` for tests |
| `contextfile` | `internal/contextfile` | AGENTS.md discovery and the system prompt section it becomes |
| `eikad` | `internal/eikad` | The daemon's handlers, path confinement, and wire types |
| `sandbox` | `internal/executor/sandbox` | The production executor: an HTTP client for one eikad |
| `workspace` | `internal/workspace` | Container and volume lifecycle on the Docker daemon, and each container's confinement: its limits, its sandbox network, and its usage |
| `egress` | `internal/egress` | The egress modes and allowlist patterns, and the HTTP proxy a restricted sandbox reaches the internet through |
| `netguard` | `internal/netguard` | The dial check that keeps the harness's outbound connections on public addresses, shared by `search/fetch` and the egress proxy |
| `hub` | `internal/workspace/hub` | Bare git repositories and the Smart HTTP endpoint workspaces clone from |
| `store` | `internal/store` | The PostgreSQL pool, the embedded migrations, and the queries behind every table |
| `session` | `internal/session` | The session tree: append, head, branch, fork, outline, and the agent store that records a run |
| `subagent` | `internal/subagent` | Spawning child agents: the hand-over commit, the child workspace and session, the limits, and the result the parent reads |
| `search` | `internal/search` | The search engine: the web failover chain, quotas and cooldowns, the result cache, and the backends in `search/web`, `wikipedia`, `arxiv`, and `github` |
| `mcp` | `internal/mcp` | The Model Context Protocol client: both protocol eras, the Streamable HTTP, HTTP+SSE, and stdio transports, the pool of connections, the tools runs offer, and elicitation; `mcp/mcptest` holds the fake servers |
| `oauth` | `internal/mcp/oauth` | MCP authorization: the Bearer challenge, protected resource and authorization server discovery, client registration, and the authorization code flow with PKCE |
| `fetch` | `internal/search/fetch` | Reading one URL as Markdown: URL planning, GitHub reads, HTML extraction, section selection, and the content budget from `search/page`; `search/filter` is the JavaScript filter eikad runs |
| frontend | `web/` | Vite, React 19, Tailwind v4, shadcn/ui; the guided setup, the settings, and the three-pane workbench: project tree, streaming session, context panels |

`eika` serves `GET /healthz` and `GET /api/healthz`, both returning
`{"status":"ok"}`. `/healthz` is the container health check; `/api/healthz` is
what the frontend calls, so the same URL works behind the Vite dev proxy and in
production. Everything else under `/api` is the JSON API documented in
`docs/api/http.md`, and `/git/` is the hub. With `-web <dir>` the harness also
serves the built frontend and falls back to `index.html` for unknown paths.

## The agent core

One `agent.Agent` drives one session. A run is a sequence of turns:

```
Run(ctx, session, "fix the build")
  |
  v
turn: append user message -> turn.start
  |
  +--> provider.Stream(request)  ---- message.delta ---->
  |        |
  |        +-- assistant text + tool calls
  |
  +--> no tool calls? -> turn.end, and the turn is over
  |
  +--> for each tool call, in order:
  |        tool.call -> executor does the work -> tool.output* -> tool.result
  |        append the result to the conversation
  |
  +--> deliver queued steering messages, then call the model again
  |
  v
after the turn: deliver queued follow-up messages, which start the next turn
```

The pieces:

- **provider** turns a `Request` (system prompt, messages, tool schemas) into a
  channel of `Event` values: text deltas, assembled tool calls, usage, and a
  terminal done or error. `provider/openai` speaks Chat Completions with the
  official SDK; `provider/providertest` replays scripted responses in tests.
  A model can set `reasoning_effort`, and the words it may take are the
  model's own row rather than a list in the code, because compatible
  endpoints disagree on the vocabulary. The effort `none` turns thinking
  off, sent in the one field the model's `thinking_switch` names:
  `reasoning_effort`, `chat_template_kwargs`, or `thinking`. Streamed
  `reasoning_content` always becomes `KindReasoningDelta`, so a client can
  show the model thinking; an OpenAI-compatible endpoint can also set
  `preserve_thinking`, which is what makes Eika send that extension and
  replay the reasoning with later assistant messages. An endpoint that
  rejects the extension must set it to false. It is not an OpenAI API field.
  Usage is relayed as soon as a chunk carries it, so an endpoint with
  continuous usage statistics keeps the context meter current while the
  response is still arriving. An endpoint that
  measures its own speed (llama.cpp, vLLM, NIM, LM Studio, TabbyAPI, Groq,
  Ollama) reports it in fields outside the OpenAI shape; `openai/timings.go`
  reads them into `provider.Timings` on the usage event.
- **tool** holds the `Tool` interface and a registry. A call receives a
  `CallContext` carrying the executor, the event emitter, and the session and
  run identifiers. A tool that also implements `tool.Standalone` runs without
  a workspace, with a nil executor; every other tool needs one, and
  `Registry.Filter` narrows the shared registry to what one run may offer.
  `tool/builtin` implements `bash` and keeps every output bound in
  `limits.go`. There are no file or search tools: the model explores, reads,
  and edits files through `bash`. `ask_user` is the one tool that blocks on a
  human rather than on the workspace: it registers a question with the `Questions` broker, emits
  `question.asked`, and waits for the answer the API delivers.
- **executor** is the only way a tool reaches files or processes.
  `executor.Resolve` rejects any path that leaves the workspace root; the local
  implementation re-checks after resolving symlinks. `executor/local` carries
  the `!prod` build tag, so a production binary cannot contain it.
- **contextfile** discovers AGENTS.md (CLAUDE.md as a fallback,
  AGENTS.override.md as a replacement) from the workspace root down to the
  working directory, through the executor, and renders the system prompt
  section.
- **event** owns the envelope, the run payload structs, and the `Emitter`
  interface both the loop and the tools emit through.
- **context assembly** (`agent/context.go`) builds every model request in
  one place, as an `agent.Context`: the system prompt by named section (the
  base prompt, the context files, the extra instructions), the tool schemas
  marked built-in or MCP, the messages exactly as sent, and the parameters,
  with a size estimated at four bytes to a token for each. A run's model
  call and `Agent.Preview` share it, and a `Recorder` is told about every
  call that returned a response. The base prompt is `WorkspacePrompt` or
  `ChatPrompt` unless `Options.BasePrompt` replaces it, and the sampling
  parameters are one `provider.Sampling`, whose nil fields are the
  endpoint's.

Queues follow Pi. A steering message joins the conversation as soon as the
running tool finishes, before the next model call. A follow-up message waits
until the turn ends; `QueueOneAtATime` then starts a turn with one of them and
`QueueAll` with all of them. Cancelling the context aborts the run: a message
the turn had taken but never got a model response for leaves the conversation
again and goes back on its queue, so the next run does not replay it. A turn
that stops part way through a batch of tool calls still answers every call, so
the session stays valid for the next request. Queue acceptance closes atomically
with the loop's final queue check. Messages accepted before an abort or failure
stay with the session's run manager and transfer to its next run.

A retryable provider failure (429, 5xx, or a transport error) is retried with
exponential backoff, at most `MaxRetries` times, honouring `Retry-After`. The
SDK's own retries are switched off so that one place decides. Anything else
fails the run with a `run.error` event. A retry emits `message.reset`, so a
client discards text streamed by the failed attempt. A response is complete
only when it has a terminal stop reason other than `length` or
`content_filter`.

Each configured model's context window is an active preflight bound. The agent
uses the serialized system prompt, messages, and tool definitions as a
conservative token upper bound, adds the requested output tokens, and rejects
an oversized call before it reaches the provider. Automatic compaction remains
deferred.

Messages go to a `Store` as they are appended: `agent.MemoryStore` in tests
and single-process runs, `session.Store` against PostgreSQL everywhere else.

## Persistence and session trees

`store` owns the database and nothing else: a pgx connection pool, the
embedded migrations, and hand-written SQL in one file per entity. `Open`
connects, pings, and applies every migration the database is missing, so a
harness start and a fresh database take the same path. A missing row is
`store.ErrNotFound`, a duplicate key is `store.ErrConflict`, and every other
pgx error arrives wrapped in the operation that failed.

Ids are text everywhere: 20 lowercase base32 characters from `store.NewID`,
generated by the harness before the row exists so that one id can name a row,
a container, and a URL.

| Table | Columns | Holds |
|---|---|---|
| `schema_migrations` | version, applied_at | Which embedded migrations this database carries |
| `projects` | id, name (unique), kind (remote/local), remote_url, remote_username, remote_password (sealed), host_path, default_branch, created_at | A git repository Eika knows and a private remote's credentials |
| `workspaces` | id, project_id, name, branch, base_commit, image, state, container_id, parent_workspace_id, created_at, updated_at | The record of one sandbox container; `state` mirrors `workspace.State` |
| `sessions` | id, workspace_id (NULL for a chat), title, kind (user/fork/agent), head_entry_id, parent_session_id, tools (NULL for the profile's choice), profile_id (NULL for the default profile), overrides (jsonb, NULL for none), created_at, updated_at | One session tree, who opened it, the head a run continues from, the tools its runs may offer, and the profile it runs with and what it sets over it |
| `session_entries` | id, session_id, parent_id, seq, kind, payload (jsonb), commit_sha, created_at | One node of a session tree |
| `runs` | id, session_id, state, started_at, finished_at, error | One execution of the agent loop |
| `subagents` | id, parent_session_id, child_session_id, child_workspace_id, state, result, created_at, finished_at | One child agent and what it reported |
| `settings` | key (primary), value (jsonb) | What the user changes at runtime |
| `providers` | id, name (unique), kind, base_url, api_key (sealed), created_at, updated_at | A model provider: an endpoint of one provider kind and its key |
| `models` | id, provider_id, name (unique), model, context_window, max_output, reasoning_effort, reasoning_efforts, thinking_switch, preserve_thinking, created_at, updated_at | A model a run may use; `name` is Eika's, `model` the endpoint's, `reasoning_efforts` the words its effort cycles through, `thinking_switch` the request field that carries the effort `none` |
| `profiles` | id, name (unique), description, model_id, workspace_prompt, chat_prompt, instructions, context_files, preserve_thinking, tools, sampling (jsonb), created_at, updated_at | A named configuration of what a run sends; NULL, and a sampling key left out, is not set |
| `model_requests` | id, session_id, run_id, entry_id, model_id, model, sections (jsonb), tools (jsonb), parameters (jsonb), message_tokens, input_tokens, output_tokens, total_tokens, created_at | One model call a run made: what it sent besides the messages, which are the session's path down to entry_id, and what the endpoint measured |
| `auth_password` | id (always 1), hash, updated_at | The sign-in password as a PBKDF2 hash |
| `auth_sessions` | token_hash, created_at, expires_at | A signed-in browser, by the SHA-256 of its token |
| `search_keys` | name (primary), key (sealed), updated_at | The API key of a search provider, or the GitHub token |
| `search_usage` | name (primary), day, day_used, month, month_used, cooldown_until, fail_streak, updated_at | One search quota bucket's counters and cooldown, so quotas survive a restart |
| `mcp_servers` | id, name (unique), kind (http/stdio), url, headers (sealed), command, args, env (sealed), enabled, disabled_tools, oauth_client_id, oauth_client_secret (sealed), created_at, updated_at | An MCP server the user configured: a remote endpoint and its headers, or a command started in workspaces and its environment |
| `mcp_credentials` | server_id (primary, cascades), issuer, resource, resource_metadata_url, metadata (jsonb), client_id, client_secret (sealed), client_auth_method, client_registration, redirect_uri, access_token (sealed), refresh_token (sealed), scope, expires_at, updated_at | What authorizing an MCP server left: its authorization server, the client registered there, and the tokens |

Everything cascades from `projects`: deleting a project deletes its
workspaces, their sessions, their entries, and their model requests. An MCP server's credentials
cascade from its `mcp_servers` row. A chat has no workspace, so it
hangs off nothing and is deleted on its own. `sessions.head_entry_id` has no
foreign key, because `session_entries` already references `sessions` and a key
in the other direction would be a cycle; the guarded `UPDATE` in `SetSessionHead`
sets a head only when the entry is one of the session's own. Every foreign key
is indexed, and `session_entries` also has `(session_id, parent_id)` for
walking down and `(session_id, seq)`, which is the uniqueness constraint on
the sequence number as well.

Three writes are transactional. `AppendEntry` locks the session row, reads the
head, inserts the entry with that head as its parent and the session's next
sequence number, and moves the head to it; the row lock is what makes
`max(seq) + 1` unique, so concurrent appends to one session form one chain
rather than colliding. `ForkSession` creates a session and copies entries into
it. `migrate` applies every pending migration under a transaction-scoped
advisory lock, which the database releases however the transaction ends.

`session` is the domain on top: `Tree` appends, reads `Path` from the root to
the head, moves the head, forks, lists `Children`, and renders an `Outline`
for the user interface. `session.Store` implements `agent.Store`, so a run
writes its messages into the tree as it goes, and `Load` rebuilds an
`agent.Session` from the path, which is how a run resumes and how a branch
becomes the conversation the model sees.

One message is one entry. An assistant message that carries tool calls stays
one assistant entry whose payload is that message's JSON, so an entry
round-trips to exactly the message it came from and the tree gains no branch
point the agent loop could not resume from: a model that asked for three
calls needs all three answered. `user`, `assistant`, and `tool_result`
entries make up the conversation; `system` and `event` entries (questions,
subagent lifecycle, compaction later) are shown in the user interface and are
not sent to the model.

An assistant message can also hold the model's reasoning, which the session
view shows apart from the answer and a provider configured to preserve
thinking replays. Tool arguments keep the model's exact text. Valid arguments
are stored as their JSON value. Malformed arguments are stored as a JSON
string with `arguments_malformed: true` and restored before the tool sees
them. The marker distinguishes malformed text from a valid top-level JSON
string, so bad model output cannot make an assistant entry impossible to
write.

Every assistant entry records the workspace HEAD commit the answer was
produced at, when the caller gives `session.NewStore` a `CommitFunc` that
knows it. That commit is what fork-with-workspace clones at in phases 6 and 8;
phase 3 only stores it.

Moving the head and forking are different operations:

```
branch in place                      fork at e2
(one session, head moved to e2)      (a second session, e1 and e2 copied)

  e1 user "fix the build"              session A            session B
   |                                     e1                   e1'
  e2 assistant "on it"                    |                    |
   |\                                     e2                   e2'   <- head B
   | \                                     |                    |
  e3  e5  <- head                          e3                   e5'
   |   |                                    |
  e4  ...                                  e4  <- head A
```

A branch keeps every entry in one session, so the outline shows both
continuations and the head decides which one the next run extends. A fork
copies the path into a session of its own and shares no rows, so the two
sessions cannot disturb each other, either one can be deleted, and the fork
can be pointed at a workspace cloned at its last entry's commit.

Both are allowed only at a resumable entry: one whose path leaves no tool call
unanswered, since a model that asked for three calls needs all three answered
before it is asked anything else. `session.PathResumable` is the rule,
`Outline` reports it per node, and the server checks `Tree.Resumable` before
it moves a head or clones a fork's workspace. A fork is written with kind
`fork` and a subagent's session with kind `agent`; `Store.Sessions` with
descendants follows `parent_session_id` recursively, so the sidebar can hang
both under the session they came from even when they run in a workspace of
their own.

## Chats

A chat is a session with no workspace: `sessions.workspace_id` is NULL. It is
the same session tree, run manager, event stream, and session view as any
other session. What it lacks is everything that reaches a sandbox, and each
piece that would reach one checks for it:

```
POST /api/sessions {"chat": true}  ->  sessions row, workspace_id NULL
POST /api/sessions/{id}/messages
   |
   v
runs.begin
   +-- no executorFor, no commit function     (nothing to act in or record)
   +-- sessionTools(sess)                     registry every run shares
   |      drop tools that NeedsWorkspace        ask_user, web_search, web_fetch
   |      keep the ones sessions.tools names    left for a chat
   +-- agent.New(provider, narrowed, Options{Executor: nil})
          system prompt: the chat rules, no context files
          a standalone tool runs with a nil executor
          any other call is refused as an unknown tool
```

The model is never told about a tool it may not use, and the refusal in the
agent loop is the second line: a model that calls one anyway gets an error
result, not a sandbox. web_fetch reads pages in a chat but refuses a filter,
because the model's JavaScript runs only in a sandbox. A chat's fork is a
chat and keeps its tools; a fork with a workspace is refused.

`sessions.tools` is the user's choice for any session, NULL until one is
made, when the session's profile's choice applies. The API reports the effective list, what the next run will offer, so
the client shows it without knowing the rule, and `GET /api/tools` says which
tools need a workspace.

## Server, run manager, event bus

`internal/server` is where the process is composed. `server.Run` opens the
store, builds the hub and the workspace host from the configuration, makes the
model set and the tool registry, hands them to `server.New` as `Deps`,
reconciles the recorded workspace states against what the Docker daemon
actually has, and serves. `cmd/eika` parses two flags, loads the
configuration, and calls it.

```
  cmd/eika  ->  server.Run(ctx, cfg, log, opts)
                    |
                    +-- store.Open ------------------ postgres
                    +-- secret.Load ----------------- /var/lib/eika/secret.key
                    +-- hub.New --------------------- /var/lib/eika/hub
                    +-- workspace.NewHost ----------- docker socket
                    +-- provider.NewRegistry(openai.New)
                    +-- builtin.Registry(questions)
                    +-- event.NewBus
                    +-- NewMCP -> mcp.Pool ---------- MCP servers (Start after New)
                    |
                    v
               server.New(Deps) -> routes -> Serve

  HTTP                                          WebSocket
  POST /api/sessions/{id}/messages              GET /api/events
        |                                             |
        v                                             v
   runs.start                                   bus.Subscribe(topics)
        |  store.StartRun                             ^
        |  session.NewStore(tree, commitFunc)         |
        |  agent.New(provider, tools, opts) <-- the hook later phases
        |                                      register their tools in
        v
   goroutine: agent.Run(runCtx, session, text)
        |                       |
        |  entries              |  events
        v                       v
   session tree (postgres)   event.Bus --fan out--> one buffered channel
        |                                            per connection
        |  store.FinishRun(done | error | aborted)        |
        v                                                 v
   runs.finish                                    JSON frames to the client
```

The pieces:

- **Deps** are the harness pieces every request shares: the store, the hub,
  the workspace host, the provider kind registry, the secret box, the tool
  registry, the question broker, the bus, and the MCP pool (see MCP). `Workspaces`, `Hub`, and
  `Providers` are interfaces, defined in `server` because that is where they
  are consumed, so the handler tests run the whole API against a host backed
  by temporary directories and a scripted provider. A zero `Deps` serves the
  health checks and the event stream alone.
- **Auth** is one middleware over the whole `/api` subtree. A request carries
  a bearer token: a sign-in session's, looked up by its SHA-256 in
  `auth_sessions`, or the deployment's optional API token, compared in
  constant time. `GET /api/auth/status`, `POST /api/auth/setup`, and
  `POST /api/auth/login` hand out sessions and are the public routes under
  `/api`; setup succeeds once, while no password exists. `/healthz` is public,
  and `/git/` is mounted outside the middleware because the hub authenticates
  workspaces itself with per-workspace credentials. The two WebSocket
  routes, the event stream and a workspace's terminal, are the only ones that
  also accept the token as a query parameter, because a browser cannot set a
  header on a WebSocket handshake; `bearerToken` matches them by path. Sign-in attempts take
  turns behind one mutex, which with the hash's cost bounds guessing without
  a lockout.
- **Providers and models** are rows. A run, a probe, or a model test opens
  the provider's sealed key, builds a client through `Providers`
  (`*provider.Registry` in production), and drops it when done, so a changed
  key applies to the next use.
- **Profiles and a run's configuration.** `configuration.go` holds the one
  rule for what a run sends. `configure` loads the models, the profiles, and
  the two defaults, and `resolve` takes each value from the first layer that
  sets it:

  ```
  request model -> session overrides + tools -> profile -> model row -> default
  ```

  The model row supplies `max_output` and `reasoning_effort`; the default is
  the `default_model` setting (or the first model), the built-in prompts,
  context files read, every tool, and the endpoint's own sampling defaults.
  An effort from above the model row that the model does not offer falls
  through to the row's and is reported as dropped. The result carries the
  layer of every value, which the API returns so the UI never re-derives the
  rule, and `inherited` is the same resolution with one layer's own values
  taken away, which is what an editor of that layer shows as fall-through.
  A tool choice is resolved against the registry and the MCP pool by one
  function, `toolChosen`, where `mcp__<server>__*` takes every tool the
  server offers. `newAgent` turns a configuration into `agent.Options`; the
  run manager and the context preview both call it, so the preview
  (`GET /api/sessions/{id}/context`, through `agent.Preview`) is the request
  the next run sends. A run's `Recorder` writes each model call to
  `model_requests`, keyed by the run row and the session's head when the call
  was made.
- **Settings** are one key/value table the UI writes. The keys the harness
  reads itself (`default_model`, `sandbox_image`, `subagent_max_depth`,
  `subagent_max_children`, `setup_complete`) are validated on write and read
  with a fallback to their defaults, so a bad row never stops a run.
- **Errors** have one shape, `{"error":{"code","message"}}`. `statusOf` maps
  the sentinel errors of the packages the handlers call onto statuses:
  `store.ErrNotFound`, `workspace.ErrNoWorkspace`, `hub.ErrNoProject`,
  `builtin.ErrNoQuestion`, `mcp.ErrNoElicitation`, and
  `mcp.ErrNoAuthorization` are 404; `store.ErrConflict` and
  `mcp.ErrDisabled` are 409; `hub.ErrBadProject`, `workspace.ErrBadBranch`,
  `builtin.ErrBadAnswer`, `mcp.ErrBadElicitationAnswer`, and
  `mcp.ErrNeedsWorkspace` are 400. An MCP server's or its authorization
  server's own failure, which says what the user can do about it, is
  reported as 400 by `mcpFailure`; the database behind the pool stays
  internal. Anything unmapped is the harness's own failure: it is logged in
  full and reported as `internal error`, so a database message never reaches a
  client.
- **The run manager** owns one goroutine per active run and at most one run
  per session. A run is not bound to the request that started it: the client
  gets the run row as soon as the loop begins and follows the rest on the
  event stream. The agent is built per run from the workspace's executor, a
  `session.Store` on the session tree, the bus as its emitter, a recorder of
  its model calls, and the configuration `configure` resolves: the model's
  endpoint identifier, limits, and switches, the sampling parameters, the
  base prompt, the instructions, whether context files are read, and the
  tool choice. Assistant entries record
  the workspace HEAD through a commit function that runs `git rev-parse HEAD`
  through the executor and reports no commit when the workspace holds no
  repository. Every tool is in the registry all runs share, which holds what
  the tools reach beyond the workspace: the question broker, the spawner (see
  Subagents), the search engine, and the page reader (see Search). A run ends
  `done`, `error`, or `aborted`, recorded with a retrying `store.FinishRun`
  call on a context that outlives the cancelled one. If its 30-second
  foreground window ends, the run manager continues the exact write in the
  background and joins that retry on shutdown. Startup reconciliation aborts
  any run row left `running` by an earlier process. Stopping or deleting
  a workspace, deleting a session, and shutting the harness down all abort the
  runs involved first,
  because they reach the workspace through an executor that is about to go
  away.
- **The bus** is `event.Bus` in `internal/event`, which implements
  `event.Emitter`. Every subscriber has a buffered channel of its own and a
  set of topics; `Emit` never blocks, so a client that stops reading loses
  events instead of stalling the run that produced them. The dropped count is
  reported to that client alone as a `bus.dropped` event as soon as it reads
  again. Topics are `global`, `workspace:<id>`, and `session:<id>`.
- **The event stream** is one WebSocket per client. A reader goroutine handles
  `subscribe` and `session.replay` requests; the handler's own loop writes the
  subscription's events. Whichever stops first cancels the other. A replay
  writes the session's path to the socket directly rather than through the
  subscription, so a long history cannot be dropped as if the client were
  slow.

The whole HTTP surface is `routes.go`, one handler file per resource, and
`docs/api/http.md` documents every route.

## Configuration

Configuration is two things. What the process needs before it can reach its
database, addresses, paths, and the Docker topology, is deployment
configuration: `config.Config`, loaded once in `main` and passed down. What a
user chooses, model providers and models, the default model, the sandbox
image, the sandbox defaults, the subagent limits, git credentials, and the
sign-in password, is rows in the database, edited in the web UI. No package reads the environment
for either.

Deployment configuration's precedence, lowest first:

```
config.Default()  ->  the YAML file named by -config  ->  EIKA_* environment variables
```

The defaults are the compose stack's, so the harness image runs with no file
and the stack sets only `EIKA_DATABASE_URL`. `Validate` rejects an empty
`listen`, `database_url`, `docker_socket`, `searxng_url`, `sandbox_image`,
`eikad_binary`, `hub_root`, `hub_url`, or `secret_key_file`.
`sandbox_network` may be empty: that is the development mode where sandboxes
publish their daemon port on `127.0.0.1` instead of being reached by
container name. `sandbox_internal_network` (`eika_sandbox_internal`) is the
internal network a sandbox with restricted egress joins instead; it must
differ from `sandbox_network`, and with it `egress_listen` (`:3128`) must be
set and `egress_proxy_url` (`http://eika:3128`) must be an http address.
Either network empty means no sandbox's egress can be restricted, which is
the case in `make dev`. `auth_token` may be empty too: it is an optional fixed API
token for scripts, beside password sign-in. A file that still sets `models`
or `subagents` is refused with a pointer to where the setting went.

`allowed_origins` lists the browser origins that may open the event stream,
on top of the one derived from `listen`, which `Load` always prepends. An
entry is a host pattern and a whole URL is reduced to its host, so a
deployment can write either; `EIKA_ALLOWED_ORIGINS` takes a comma-separated
list, which is what `make dev` uses to let the Vite dev server through. A
deployment that serves the frontend from the harness needs none of them,
because a same-origin handshake is always accepted. The same hosts, with the
one a request came to, are where an MCP authorization may send the browser
back.

`public_url` is the address people reach the deployment at, empty by
default. When it is https, the harness serves its OAuth Client ID Metadata
Document below it, and an MCP authorization server that accepts one
identifies Eika by it instead of registering a client; a harness on
127.0.0.1 has no address an authorization server could fetch. `Validate`
accepts a bare http or https origin.

Credentials the UI stores are sealed by `internal/secret` with AES-256-GCM
before they reach a row. The key is 32 random bytes, hex in
`secret_key_file` (`/var/lib/eika/secret.key`, in the hub volume), created on
the first start. A copy of the database alone therefore opens no key or
token; a harness whose key file changed reports that a stored key cannot be
read and asks for it again. The API returns whether a credential is stored
and, for a long API key, its last four characters, never the value.
`Config.String()` redacts the auth token and the database password so a
config can be logged.

## Sandboxes

Every agent action on a file or a process happens inside a workspace
container. The harness never touches its own filesystem on an agent's behalf.
The one exception is hub maintenance, where the harness runs `git` itself
against its bare repositories.

```
  harness container (eika)                    workspace container (eika-ws-<id>)
 +----------------------------+              +----------------------------------+
 |  agent -> tool             |   HTTP       |  eikad :7000                     |
 |    -> executor.Executor ---+------------->|    /exec  /files  /stat  /list   |
 |       (executor/sandbox)   |  bearer      |    /pty   /watch  /process       |
 |  mcp.Pool -> Host.Process -+------------->|    /healthz                      |
 |                            |  EIKAD_TOKEN |                                  |
 |  workspace.Host -----------+--- docker ---+-> container + volume eika-ws-<id>|
 |    limits, networks, usage |   socket     |    mounted at /workspace         |
 |  egress.Proxy :3128 <------+--------------+-- HTTP(S)_PROXY, when restricted |
 |  previews <port>-<id>.* ---+------------->|    a forwarded port             |
 |  hub.Handler  /git/...     |<-------------+--  git clone / push (basic auth, |
 |    bare repos in           |  Smart HTTP  |    per-workspace hub token)      |
 |    /var/lib/eika/hub       |              |                                  |
 +--------------+-------------+              +----------------------------------+
                |
                | git fetch / push (harness credentials, never in a sandbox)
                v
         upstream remote (GitHub, ...)
```

### eikad

`cmd/eikad` is a thin main over `internal/eikad`: it reads `EIKAD_TOKEN` from
the environment, refuses to start without it, and serves the daemon through
`server.Serve`, the same listener lifecycle the harness uses.
`eikad filter` is the one other mode: a one-shot command that reads a
web_fetch filter request on stdin, runs it with `search/filter`, and writes
the outcome to stdout. It needs no token, because it is only ever run through
the daemon's own exec.

The daemon confines every path to its root (`/workspace`). A path may be
relative to the root or absolute inside it; `..` is rejected rather than
clamped, and the longest existing prefix of a path is resolved through
symlinks before it is compared to the root, so neither a traversal nor a
symlink out of the workspace can escape. The canonical path is what the daemon
then opens, so the path that was checked is the path that is used.
`EIKAD_TOKEN` is stripped from the environment of every command the daemon
runs, so an agent cannot read the token that protects its own sandbox.

A command leads its own process group and a timeout kills the group, so a
backgrounded grandchild cannot survive the run or keep its output pipes open.
Output is capped per run; past the cap the daemon reports the run as truncated
rather than streaming without limit.

The API is documented in `docs/api/eikad.md`. The change watcher polls and
compares modification times instead of taking an inotify dependency: a
workspace tree is small, and polling behaves the same on a bind mount as on a
volume.

### The sandbox executor

`executor.Executor` is the interface tools use. Its production implementation,
`executor/sandbox`, is an HTTP client for one daemon: a base URL, the
workspace's token, and the workspace root. `Exec` decodes the daemon's
newline-delimited frames and writes them into the caller's `Stdout` and
`Stderr` as they arrive, so a tool streams output without buffering a whole
command. A `404` from the daemon comes back as `sandbox.ErrNotFound`.

### Terminal, files, and changes

A person works in a workspace beside the agent through three kinds of route,
all of which reach the sandbox the way the agent does, and none of which the
harness serves from its own filesystem.

Files and changes go through the executor. `GET` and `PUT
/api/workspaces/{id}/file` and `GET .../files` call `Stat`, `ReadFile`,
`WriteFile`, and `List`, after `executor.Resolve` has checked the path
lexically; eikad checks it again after following symlinks. A refused path is
`403`, a missing one `404`. Commit and push run `git` in the workspace through
`Exec`, as the diff does; push then goes through `Host.Push` to the hub and,
for an upstream push, `hub.Push` from the hub to the project's remote with the
project's sealed credentials, which never enter the sandbox. A save, a commit,
and a push publish `workspace.state` so that open views refresh.

The terminal is one of the two sandbox connections that are not executor
calls. It is `sandbox.Client.Terminal`, reached through `Host.Terminal` and
the server's `Workspaces` interface, and deliberately absent from
`executor.Executor`: a tool holds an executor, so it can run commands but can
never get a PTY. The other is `sandbox.Client.Process`, eikad's `/process`,
which the MCP pool runs a stdio server over (see MCP) and which is kept off
the executor for the same reason.

```
  browser                      harness (server/terminal.go)             sandbox
  GET /api/workspaces/{id}/terminal?rows&cols&token
     |                            |
     |                            +-- Workspaces.Inspect: running? else 404/409
     |                            +-- Host.Terminal -> sandbox.Client.Terminal
     |                            |      dial ws://<address>/pty  --------> eikad /pty
     |                            |      Authorization: Bearer EIKAD_TOKEN    shell on a PTY
     |<-- 101 (origin checked) ---+
     |                            |
     |  input / resize  --------> relay, bytes unchanged  ---------------> pty write / resize
     |  <-------- output, exit    relay, bytes unchanged  <--------------- pty read, exit
     |                            |
     |  close (status, reason) -> passed on as is ------------------------> shell killed
     |  <- close (status, reason) passed on as is <------------------------ shell exited
```

The sandbox is dialled before the browser's handshake is accepted, so a
stopped or missing workspace is an ordinary HTTP error. Each direction runs in
its own goroutine; whichever side closes first has its close status and reason
passed to the other, and a connection that drops without a close frame closes
the other side with `1011`. A message is at most 1 MiB either way, which eikad
enforces as well, so a large paste arrives whole.

### Workspace lifecycle

`workspace.Host` wraps the Docker Engine client. `Create` builds the image
when the spec carries a build context, creates the volume `eika-ws-<id>` (or
bind-mounts a host directory in local project mode), creates the container
with the label `eika.workspace=<id>`, and copies the harness's static `eikad`
binary into it at `/usr/local/bin/eikad`, which is also the container's
entrypoint. `Start` starts the container and waits for `/healthz`. `Stop`
leaves the volume alone; `Destroy` removes the container, the volume, and the
image if it was built for that workspace alone.

A sandbox runs arbitrary code, so the container is confined: Docker's own init
is PID 1 (it reaps what a shell orphans, and eikad runs under it), all Linux
capabilities are dropped, `no-new-privileges` is set, and the container runs as
uid 1000 unless the spec names another user. A custom image therefore needs
that uid to exist, or must set `Spec.User`.

Sandboxes join their own Docker network, `sandbox_network` (`eika_sandbox` in
compose), which the harness joins as well. Postgres and SearXNG stay on the
default network, so a sandbox can reach the harness, the hub, and the internet,
but not the database.

The binary is copied into the created container rather than bind-mounted,
because a bind mount source is resolved by the Docker daemon on the host, and
the harness's own filesystem lives inside a container the host cannot see.
The copy happens before the container starts, so any image works as a sandbox
without being rebuilt.

In local project mode the spec carries a host path instead of a volume, and it
is bind-mounted at `/workspace`. The path is resolved by the Docker daemon, so
it is a path on the *host*, not in the harness container. Under compose that
means a local project directory has to be mounted into the harness as well, at
the same path, if the harness is to read it directly; the workspace itself
only needs the daemon to see it.

The harness reaches a workspace at `http://eika-ws-<id>:7000` on the compose
network named by `sandbox_network`. With an empty `sandbox_network` the daemon
port is published on `127.0.0.1` instead, which is what a harness running on
the host in `make dev` needs. `List` finds containers by label and `Inspect`
recovers a workspace's tokens from the container's environment, so the harness
reconciles with what is actually running after a restart. Reconciliation marks
a workspace `gone` only when Docker reports that its container does not exist.
An inspection or daemon failure stops reconciliation and keeps every recorded
state, so a temporary Docker failure does not become a destructive state
change.

### Confinement: limits, egress, and previews

A workspace's row records its `sandbox`: CPU, memory, and process limits, an
egress mode with its allowlist, and the ports the harness forwards to it. The
row is the desired state. `Host.Create` builds the container with it,
`PUT /api/workspaces/{id}/sandbox` writes the row and then applies it with
`Host.Confine`, and every start applies the row again before `Host.Start`, so
a change the container took only in part is completed. A fork and a child
agent get their parent's limits and egress and none of its ports
(`server.ChildSandbox`, which the spawner is given).

Limits are Docker's: `NanoCPUs`, `Memory` with `MemorySwap` equal to it, so
there is no swap past the limit, and `PidsLimit`. `Confine` changes them on
the running container with `ContainerUpdate`. Docker reads a zero CPU or
memory limit in an update as "unchanged" and refuses to lift one, so no limit
on an existing container is the whole host: every core and all of its memory,
from `Host.Capacity`. `Host.Usage` samples `docker stats` once;
`GET /api/workspaces/{id}/usage` adds the hosts the egress proxy refused, and
the Sandbox panel asks for it every five seconds while it is open.

```
   open egress                          allowlist or none
  +-------------+                      +-------------+
  | eika-ws-a   |                      | eika-ws-b   |  HTTP(S)_PROXY=
  +------+------+                      +------+------+  http://b:<hub token>@eika:3128
         | eika_sandbox                       | eika_sandbox_internal (internal: no route out)
         v                                    v
  +------+------------------------------------+------+
  |            eika (on both, and eika_default)      |
  |   eikad calls, /git hub   egress.Proxy :3128 ----+--> public internet,
  +------+-------------------------------------------+    allowed hosts only
         |
         v  internet, directly
```

An open sandbox is on `eika_sandbox` and has a route out of its own. A
restricted one is on `eika_sandbox_internal`, an internal Docker network with
no route out, which the harness is on as well, so eikad calls and the hub work
as before. Its only way to the internet is the harness's egress proxy,
`egress.Proxy`: it takes `CONNECT` tunnels and absolute `http://` requests,
knows a sandbox by its workspace id and hub token in `Proxy-Authorization`,
asks `Server.EgressPolicy` for the workspace's mode and allowlist on every
request, so a change applies to the next connection, and dials through
`netguard`, so neither the database nor another container is reachable
through it. A refusal is `403` with a line saying why and is remembered per
workspace (`Proxy.Blocked`), which the Sandbox panel lists with a button that
adds the host to the allowlist. The hub token is read from the container the
first time the proxy is asked about a workspace and cached until it is
destroyed. The proxy serves only when the host has both sandbox networks
(`Host.EgressControl`).

Switching between open and restricted moves the container between the two
networks: `Confine` connects it to the new one before it leaves the old, so
the harness reaches the daemon by name throughout, and the connections the
sandbox had open drop. What the sandbox's processes inherit is given to eikad
with `PUT /environment` rather than set on the container, because a
container's environment cannot change: the proxy URL under both spellings of
`HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` (the hub and loopback), and
`NODE_USE_ENV_PROXY`. `Host.Start` gives it on every start, since eikad
forgets it when the container stops. A tool that ignores the proxy variables
reaches nothing, which is the point: the network is what enforces the mode,
and the proxy is the one door in it.

A preview is how the person reaches a forwarded port from their browser. The
harness forwards it, since the harness is the only thing on the sandbox
networks, on a host of its own, `<port>-<workspace id>.<host>`, so the page
is another origin than the UI and cannot read its token; browsers resolve any
`*.localhost` name to the machine itself. `Server.Handler` sends a request
for such a host to the preview before the routes. The UI asks
`POST /api/workspaces/{id}/ports/{port}/preview` for a link carrying a
one-time ticket, the preview trades the ticket for a cookie on its own host,
and every later request is checked against the workspace's row, so taking a
port off the list closes it at once. Tickets and sessions are in memory.

### The git hub

`internal/workspace/hub` keeps one bare repository per project at
`<hub_root>/<project>.git` and serves them at `/git/<project>.git` through
`git http-backend` over CGI. A workspace authenticates with HTTP basic auth:
its workspace id as the user and a per-workspace token as the password, which
`Grant` hands out at creation and `Revoke` withdraws at destruction. A grant
covers exactly one project, so a workspace reaches its own repository and
nothing else, and `Inspect` only re-grants a workspace whose container is
running.

`Host.Clone` checks a project out into a running workspace through the
executor, like any other agent action. Before cloning it configures a git
credential helper inside the container that answers from `EIKA_HUB_USER` and
`EIKA_HUB_TOKEN`, so the token never lands in the remote URL, in
`.git/config`, or in a log. Project and branch names are validated (the branch
by `git check-ref-format`) and every git invocation passes them as arguments
rather than through a shell. The commit the workspace starts from is returned
as its base commit.

Upstream remotes are the harness's business alone: `Mirror` fetches every
branch of a remote into the hub and `Push` sends a refspec back. A private
project's username and password or token are entered with the project and
its password is sealed; the API rejects credentials in `remote_url`. The
server opens the password for one git command and the hub passes it through
the process environment with a one-line credential helper, so it is never
written to disk or put in a process argument. Changing a project's
credentials fetches with the new ones first, so a wrong token is refused
before it is stored.

The API also rejects URL query strings and fragments because they can hold
tokens. Migration `0002_remote_credentials` removes userinfo, query strings,
and fragments from old remote URLs, and migration `0003_ui_configuration`
drops the environment-variable references earlier versions stored instead of
credentials, so such a project has its credentials entered again in its
settings.

## Subagents

`internal/subagent` gives a running agent child agents. A child is a full run
of its own: its own workspace cloned from the parent's commit, its own
session, its own branch, its own event topic, and the same tool registry,
which includes `spawn_agent` again down to the configured depth.

```
parent run                spawner              hub              child workspace
    |                        |                  |                      |
 spawn_agent --------------> |                  |                      |
    |          commit dirty tree in the parent workspace               |
    |                        |-- git add -A; git commit "wip: before   |
    |                        |   spawning <name>" ------------->       |
    |                        |-- git push eika-hub <parent branch> --> |
    |                        |                  |                      |
    |                        |-- Host.Create + Start ----------------> |
    |                        |-- CloneAt(project, <parent>-<name>, base commit) -->
    |                        |                  |                      |
    |                        |-- rows: workspaces(parent_workspace_id), |
    |                        |   sessions(parent_session_id), subagents |
    |                        |-- event subagent.started on session:<parent>
    |                        |                                         |
    |                        |-- runs.runChild(child session, task) --> agent loop
    |                        |          (events on session:<child>)    |
    |                        |                                         |
    |                        |<-------------- run ends ----------------|
    |                        |-- git commit "wip: subagent <name>      |
    |                        |   finished"; git push --force --------> |
    |                        |-- git diff --stat <base>..<head>        |
    |                        |-- Host.Stop (not Destroy)               |
    |                        |-- FinishSubagent(state, result JSON)    |
    |                        |-- event subagent.finished on session:<parent>
    |<-- tool result: summary, branch, commit, diffstat ---|
```

The pieces:

- **The hand-over commit.** A child clones from the hub, so the parent's
  uncommitted work has to reach the hub first. The spawner commits the whole
  tree as `wip: before spawning <name>` and pushes the parent's branch. The
  hub mirrors local projects too, so a bind-mounted local workspace hands over
  the same way a volume-backed one does; `Host.Push` creates the project's
  repository if it has none and adds the hub as the `eika-hub` remote rather
  than as `origin`, which a local checkout already has.
- **The child's branch** is `<parent branch>-<name>-<6 characters of the
  child's id>`, not a path below the parent's branch: git stores either
  `refs/heads/main` or `refs/heads/main/fix`, never both, and the parent's
  branch is in the hub by then. The tag makes the branch the child's own, so
  two children a parent gave the same name never write over each other and no
  push has to force. The name is checked against a narrow pattern before it
  reaches git, and `git check-ref-format` checks the branch itself.
- **The tools** are `spawn_agent`, `wait_agents`, and `list_agents` in
  `internal/tool/builtin`. They reach the spawner through the `Subagents`
  interface declared there, the same shape `ask_user` uses for its question
  broker, so nothing under `tool` learns what a workspace is. `spawn_agent`
  blocks until the child finishes; `wait: false` returns the child's id at
  once, and `wait_agents` collects them later. A tool call only ever names its
  own session's children, and it cannot name the image its child runs: a child
  runs its parent's image, because nothing validates an image name a model
  made up.
- **The runner.** The spawner drives a child through `subagent.Runner`, which
  the server implements with `runs.runChild`: an ordinary run, bound to the
  context of the parent run that asked for it. So the child's tools, events,
  entries, and retry behavior are the parent's, and no second agent loop
  exists.
- **The limits** are the `subagent_max_depth` and `subagent_max_children`
  settings, 2 and 4 by default, which `subagent.Options.Limits` reads at
  every spawn, so a change applies to the next child. Depth is measured by walking `subagents`
  rows up from the spawning session; width counts that session's running
  children, rows and live reservations alike. A spawn claims its slot under
  the lock that counts them before it does any of the slow work, so two
  `spawn_agent` calls that arrive together cannot both pass a count taken
  before either had a row.
- **Cancellation** flows down. Aborting a run aborts its children, and
  aborting a child aborts its own children in turn, because a child works on a
  branch of a run that has nobody left to report to. Cancelling ends a child's
  loop but does not end it at once, so the spawner waits for the loop to stop
  before it commits the child's tree. An aborted or failed child still
  commits, pushes, and reports: its workspace is stopped, never destroyed, so
  the user can start it again and look at what it did. A child started with
  `wait: false` outlives its parent's run, so `Server.Close` stops the runs
  and then calls `Spawner.Shutdown`, which aborts every child left and waits
  for it to record how it ended before the database pool closes.
- **The result** the parent's model sees is the child's final assistant
  message, its branch, its head commit, and `git diff --stat` from the
  parent's base commit to that head. A child that ended without a closing
  message gets one written for it from its state and its diffstat, so the
  parent never reads a blank report. The same object is stored as JSON on the
  `subagents` row, served by `GET /api/sessions/{id}/agents`, and carried by
  the `subagent.finished` event.
- **Taking the work back** is `POST /api/workspaces/{id}/merge`: the source
  workspace pushes, the target fetches from the hub, and git merges or rebases
  inside the target workspace through its executor. A conflict is a normal
  response with the conflicted paths; the tree is left as git made it, for the
  user or the agent in that workspace to resolve.

Fork-with-workspace uses the same machinery: `POST /api/sessions/{id}/fork`
with `with_workspace` pushes the current workspace, clones a new one from the
hub at the commit the fork entry recorded, on `<branch>-fork-<short id>`, and
points the forked session at it, so the files rewind with the conversation.

## Search

`internal/search` is a port of the localsearch Pi extension. The model has
two tools. `web_search` takes a query, a source, and a count; `web_fetch`
takes a URL and optionally a section, a filter, and a format.

```
 web_search --> search.Engine --+-- source "web": the failover chain
                                |     searxng -> exa -> tavily -> brave -> marginalia
                                |     (search_order; one provider answers)
                                +-- wikipedia | arxiv | github_code/repos/issues
                                |
                  Tracker: quotas and cooldowns per bucket --> search_usage
                  Keys: sealed search_keys, opened per request

 web_fetch --> fetch.Reader --> plan the URL --+-- GitHub API / raw host
                                               +-- text file, fenced
                                               +-- HTML: container, readability,
                                                   body --> Markdown
               cache --> section --> filter (eikad filter, in the sandbox)
                                 --> or budget: whole, outline, or truncated
```

- **The failover chain.** A web search asks exactly one provider: the first
  in `search_order` that has its key, quota left in its bucket, and no
  cooldown. It stops at the first that answers with results; a transport
  failure, an HTTP error, a rate limit, or no results records an attempt and
  moves on. A failure cools the bucket down for 15 minutes, doubling to six
  hours, or until the server's `Retry-After` or `X-RateLimit-Reset` (capped
  at a day). An empty `search_order` turns web search off. When
  SearXNG failed and a fallback answered, the result opens with a `Notice:`
  line saying so and how to fix it, because the details that record the
  attempts never reach the model. A search asks for a pool of 30 results and
  caches it for a day, so the same query with a larger count is free.
- **Sources** go to their one backend. arXiv is paced one request at a time,
  three seconds apart, and its pool is cached. The GitHub sources and
  web_fetch's GitHub reads count against one `github` bucket, which a 403 or
  429 cools down until GitHub's reset time. GitHub repository search refuses
  qualifiers that only code or issue search understands.
- **Keys and settings.** Exa, Tavily, Brave, and GitHub keys are sealed rows
  in `search_keys`, read per request, so a new key applies to the next
  search. web_fetch sends the GitHub token only to the GitHub API and, over
  https, to `githubusercontent.com` and its subdomains. `search_order` and `search_limits` are settings validated against
  the registered backends. The SearXNG URL is deployment configuration.
- **Reading a page.** `fetch.Reader` plans the URL before any request (see the
  PLAN decision on web_fetch reads), dispatches on the content type the server
  sent, and caches the Markdown for six hours. A section is selected by
  heading, exact then prefix then substring, with its subsections; a URL
  fragment selects the same way but falls back to the page. Past 10,000
  tokens a plain read returns the page outline and a narrowed read is cut on
  a section boundary with a note of what was left out.
- **Filters run in the sandbox.** A filter is JavaScript the model wrote. The
  tool runs `eikad filter` through the run's executor with the page on stdin;
  eikad runs it in goja with a two-second limit, renders the result, cuts it
  to the budget, and writes the outcome to stdout. The harness never
  evaluates model code, and does not link goja.
- **The address guard.** `fetch.NewClient` refuses to dial a non-public
  address after DNS resolution, re-checks every redirect's connection, and
  uses no proxy, so a model cannot point web_fetch at postgres, a sandbox, or
  a cloud metadata endpoint. Obvious private names are refused before any
  request with a clear message.

The Search tab of the settings dialog shows every backend's state and usage,
reorders and disables web providers, stores keys, edits quotas, and tries a
search through `POST /api/search`.

## MCP

`internal/mcp` is a Model Context Protocol client written against the
protocol itself, without an SDK, and `internal/mcp/oauth` its authorization.
The servers the user configures under Settings are rows in `mcp_servers`;
their tools join a run's registry beside the built-in ones.

```
            runs.begin                     GET /api/mcp/servers/{id}, routes in mcp.go
                |                                   |
                v                                   v
   mcp.Pool.Tools(ctx, workspace)            mcp.Pool.Details / Reconnect / ...
                |
   +------------+--------------------------+
   | one conn per remote server            | one conn per stdio server per workspace
   v                                       v
 ConnectHTTP                          Launcher.Launch (server/mcp.go: mcpLauncher)
   modern request (server/discover)        -> Workspaces.Process -> Host.Process
   else initialize (2025-11-25 ...)        -> sandbox.Client.Process
   else HTTP+SSE (2024-11-05)              -> eikad GET /process (WebSocket)
   headers: configured, then Bearer        -> the command, in the workspace
   token from mcp_credentials            ConnectStdio: server/discover, else initialize
                |                                   |
                +---------------+-------------------+
                                v
              catalog: tools, resources, templates, prompts
              events: mcp.server on global, mcp.elicitation on session:<id>
```

The pieces:

- **Two eras.** The client targets the 2026-07-28 revision, whose requests
  are stateless and carry their version, client, and capabilities in
  `_meta`, and falls back to an `initialize` handshake for a server that
  does not answer `server/discover` as a modern one; over HTTP it falls back
  once more, to the deprecated HTTP+SSE transport. A modern server may answer
  a request with `input_required`, and the client answers what it asked and
  sends the request again, up to eight rounds. `Client.Info` records the era,
  transport, and version found, which the UI shows.
- **Transports.** `http.go` is Streamable HTTP: a POST per message whose
  answer is JSON or an event stream, the `Mcp-Method`, `Mcp-Name`, and
  `Mcp-Param-*` headers of the modern revision (a tool whose `x-mcp-header`
  annotations break the rules is listed as excluded, never offered), and an
  older server's `Mcp-Session-Id`, GET stream, and DELETE. `legacysse.go` is
  HTTP+SSE, whose endpoint must be on the server's own origin. `stdio.go` is
  newline-delimited JSON over any `io.ReadWriteCloser`.
- **The pool.** `Pool` keeps one connection to each remote server, connected
  in the background at start, and one to each stdio server in each workspace
  that uses it. Concurrent callers share one attempt; a failure stands for 30
  seconds before a run asks again, so a server that is down costs each run
  nothing. A modern connection holds `subscriptions/listen` open, and a
  changed list is read again and announced. `Store` is the database behind
  it, which `server` implements over `store` and `secret` (`mcpStore`), so
  the pool never sees a sealed value and the store never an open one.
- **Stdio servers run in the workspace.** A stdio server is a process an
  agent's session asked for, so it runs where every such process runs: eikad
  starts it through `/process`, the harness holding the other end of its
  standard streams over a WebSocket. The route is reached through the
  server's `Workspaces` and `Host.Process`, never through
  `executor.Executor`, so a tool still cannot hold a process of its own.
  Stopping or deleting a workspace ends its stdio servers after its runs,
  and a container that stops takes them with it. Their environment is
  visible to the agent in that workspace.
- **Tools.** `Pool.Tools` connects what a run may use, waiting at most 15
  seconds, and returns `mcp__<server>__<tool>` tools, cut to 64 characters
  with a hash, and `mcp_list_resources` and `mcp_read_resource` when a
  server has resources. A remote server's tools are standalone and reach
  chats; a stdio server's need a workspace. The session's tool choice
  narrows them as it narrows the built-ins (`sessionTools` in
  `server/tools.go`), and a session whose choice names no MCP tool starts no
  server. `Pool.Offered` builds the same tools from each server's last
  listing without connecting, which is what `GET /api/tools` and the
  session's effective tool list show. A call's progress becomes
  `tool.output`; its result's text is what the model reads, images and audio
  replaced by a line saying the user saw them, and its blocks, media within
  4 MiB, are the `tool.result` details. A call whose session expired, or
  whose token a refresh replaced, is sent once more; every other failure is
  the model's to read.
- **Authorization.** `auth.go` and `oauth` run OAuth 2.1 as the MCP
  authorization spec describes it:

```
 browser               harness (server/mcp.go, mcp.Pool)          authorization server
 POST .../authorize {redirect_uri}
    ------------------> checkRedirect: <UI host>/mcp/callback
                        oauth.Discover: WWW-Authenticate, protected
                          resource metadata, RFC 8414 / OpenID metadata
                        client: oauth_client_id | metadata document
                          (https public_url) | dynamic registration ----->
                        PKCE S256, state, scope, resource; pending 10 min
    <------------------ {authorization_url}
    ---------------------------------------------------------------------> sign in, consent
    <--------------------------------------------------------------------- 302 /mcp/callback?code&state&iss
 /mcp/callback page
 POST /api/mcp/oauth/callback {state, code, iss}
    ------------------> CheckIssuer (RFC 9207), then Exchange -------------> token
                        tokens sealed in mcp_credentials; Reconnect
    <------------------ {server_id}
```

  The redirect comes back to a frontend route that posts to the
  authenticated API, so no unauthenticated route receives the code and the
  sign-in token never leaves the browser. A token is refreshed a minute
  before it expires and after a 401, a 403 with `insufficient_scope` asks
  for the union of the scopes, and signing out revokes both tokens and keeps
  the client. `GET /oauth/client-metadata.json` serves the metadata document
  for a deployment with an https `public_url`.
- **Elicitation.** A server may ask the user for input during a call, as an
  `input_required` result or, on an older server, an `elicitation/create`
  request. `mcp.Elicitations` registers it, emits `mcp.elicitation`, and
  holds the call until `POST /api/elicitations/{id}/answer`, the shape
  `ask_user` has. Roots and sampling are not declared. A server's stderr, an
  older server's log notifications, and what the client did go to a
  200-line log per server that the details show.

Every bound the client applies is in `internal/mcp/limits.go`.
`internal/mcp/mcptest` holds a scripted server in each era and a fake
authorization server, which the package's tests and the server's handler
tests run against.

## Compose topology

`compose.yaml` at the repository root is the whole deployment, and it needs
no configuration: `docker compose up -d` builds and starts it.

```
                     127.0.0.1:8080
                           |
                   +-------v--------+   internal net   +---------------+
  host.docker. <---+     eika       +------------------>    searxng    |
  internal (a      |  (harness)     |                  |  JSON format  |
  local model      +---+--------+---+                  +---------------+
  server)              |        |
             /var/run/ |        | internal net
           docker.sock +        v
           (sibling             +----------------+
            containers)         |    postgres    |  volume: eika-postgres
                                |      16        |  internal only
                                +----------------+

   volume eika-hub -> /var/lib/eika in the harness: bare repositories in
                      /var/lib/eika/hub and the sealing key in secret.key

   sandbox containers eika-ws-<id> join eika_sandbox, which only the harness
   also joins: the harness reaches them by name and they reach the hub at
   http://eika:8080, but never postgres or searxng. A sandbox with restricted
   egress joins eika_sandbox_internal instead, an internal network with no
   route out, and reaches the internet only through the harness's egress
   proxy on eika:3128
```

Only the harness publishes a port, on 127.0.0.1. Eika is single-user and
holds credentials, so nothing listens on a public interface; put a reverse
proxy in front of it for remote access. Postgres and SearXNG are reachable on
the internal network alone, which is why their built-in password and secret
are safe defaults; `compose.dev.yaml` publishes them on loopback for
`make dev`. The harness maps `host.docker.internal` to the host gateway, so a
model server on the Docker host is reachable on Linux as it is on Docker
Desktop.

A `sandbox-image` service builds `eika-sandbox:latest` from
`sandbox/Dockerfile` and exits at once; the harness waits for it to complete,
so the default image exists before the first workspace. Its build context is
the repository root because it builds `eikad` from source and bakes it in;
the harness still copies its own build over it at container creation, which
is what makes an arbitrary image usable as a sandbox.

The harness runs as the non-root user `eika` (uid 1000). It still needs the
Docker socket, and the socket's group id differs between hosts, so the
image's entry point, `deploy/eika-entrypoint.sh`, starts as root only to add
`eika` to whatever group owns the mounted socket, then drops to `eika` with
`setpriv`. Accepted risk: access to the Docker socket is equivalent to root on
the host. The harness cannot avoid it, because sandboxes are sibling
containers that it starts itself. Nothing inside a sandbox ever sees the
socket.

The harness image is built by the multi-stage `Dockerfile` at the repository
root: stage one builds the frontend with Node 22, stage two builds both Go
binaries statically, and the runtime stage carries `eika`, the frontend bundle,
and `eikad`, staged at `/usr/local/share/eika/eikad`, which is where
`workspace.Host` reads it to copy into sandboxes.

`.env.example` documents the few optional variables: the published port, the
API token, and replacements for the internal password and secret.
`deploy/searxng/settings.yml` enables the JSON result format, which the
harness needs to parse results.

## Frontend

`web/` is a Vite application: React 19, TypeScript in strict mode, Tailwind v4,
and shadcn/ui on the neutral palette. The layout is fixed by the style guide:

```
web/src/
  app/          routes, layout shell, panel registry, command palette, theme
  features/     one folder per domain feature
  components/   shared components; components/ui is shadcn-managed
  api/          wire types mirroring docs/api/, HTTP client, event stream
  lib/          pure utilities with tests
```

### Signing in and the guided setup

`app/App.tsx` decides what a browser sees. Without a token it asks
`GET /api/auth/status`, which needs none: a harness with no password gets the
guided setup (`features/setup`), and one with a password gets the sign-in
screen (`features/connect`). With a token it shows the setup until the
harness has a password and the `setup_complete` setting is true, then the
workbench. The setup walks a password, a provider, its models, the sandbox
check, and a first project, resuming at the first thing still missing; every
step after the password can be skipped.

The settings dialog (`features/settings`) is where everything the setup asked
lives afterwards: providers and models (`features/providers`), the profiles
and the default one (`features/profiles`), the default model, the sandbox
image, the subagent limits, the MCP servers (`features/mcp`), and the
password.

A profile's editor (`features/profiles/SettingsEditor`) has Model, Prompt,
Tools, and Sampling tabs under a strip that says what the draft costs every
request before the conversation (the system prompt and the tool
definitions, estimated as the harness estimates them). Every field's header
carries a chip that says where its value comes from: set here, or the layer
it falls through from, from the `inherited` configuration the API returns;
an unset field shows that value, and a set one has a reset. Bounded
sampling parameters have a slider beside the input, the reasoning effort and
the on/off settings are segmented choices that outline the inherited value,
stop sequences are chips, and a base prompt that differs from the built-in
one shows the difference line by line. Tools are chosen with
`features/profiles/ToolPicker`, a searchable switch per tool with its cost,
grouped by MCP server; a chat's Tools panel uses the same picker over the
session's own tool choice. The session's status bar shows its profile
beside the model, marked when the session overrides it, and opens the same
editor over the session's overrides, its profile, and its tool choice.
Which editor is open, and on which tab, is a small store
(`features/profiles/store.ts`), so the Context inspector's Edit links open
the editor of the layer a value comes from; the settings dialog's open state
is another, so the command palette, a session with no model, and the MCP
sign-in's return open it on the right tab.

The MCP tab lists the servers with their state and opens each on a page of
its own: its connection and sign-in, a switch per tool, its resources and
prompts to try, its log, and what the connection learned. Signing in sends
the browser to the authorization server, which returns it to `/mcp/callback`
(`features/mcp/MCPCallbackScreen`); that page posts what came back to
`POST /api/mcp/oauth/callback` and opens the settings on the server again.
`useMCPEvents`, which the workbench calls once, subscribes to `global` and
refreshes the servers and the tool list on every `mcp.server` event.

### The workbench

The shell is three panes.

```
+------------------------------------------------------------------+
| Eika          [palette] [theme] [settings]                        |
+-------------+--------------------------------+-------------------+
| projects    | session                        | Tree | Run        |
|  workspaces |   transcript                   |                   |
|   sessions  |   ------------------------     | the panel the tab |
|             |   run status bar               | strip is built    |
|             |   composer                     | from panels.tsx   |
+-------------+--------------------------------+-------------------+
```

`app/Sidebar.tsx` is the only place that composes the project, workspace, and
session features, which is why it lives in `app/` rather than in one of them.
Below the projects it lists the chats in a section of their own, newest first,
each with its forks nested under it.
`app/Workbench.tsx` holds the two dividers; `components/ResizableSplit` is a
pointer-events handler over a `role="separator"` element, so a pane is resized
by dragging or by an arrow key and the width is remembered in localStorage
through `lib/persisted`. The divider draws a hairline and takes the pointer
across about twelve pixels, which is the difference between a line worth
looking at and one worth aiming at. Below 1024px the side panes become drawers.

`app/panels.tsx` is the panel registry. The right pane's tab strip is that
array filtered by what is open, so phases 6 and 8 add a panel by writing one
component and one entry. Phase 5 registers the session tree and the run. A
workspace session adds the files, the terminal, the changes, and the
sandbox (`features/sandbox`: usage against the limits, refused hosts,
previews, and the editor of the workspace's limits, network, and ports); a
chat has none of those and shows its Tools panel instead, the switches for the tools
its runs may offer. Every session has a Context panel (`features/context`):
the next model request, previewed live by the harness, or any recorded call,
at a glance: how full the model's context window is, a stacked token bar by
part (base prompt, context files, instructions, built-in tools, MCP tools,
messages), and the key parameters. A part, or Inspect, opens the Context
inspector, a large dialog with the parts in a column and the chosen one
whole beside it: the system prompt section by section as the model reads it,
each tool's description and parameter table (or its raw schema), every
message with its size, and the parameters with the layer each came from.
Each part of the next request links to the setting behind it, and the
request copies as JSON. The estimates are scaled to the session's last
measured call.
The session header says which of the two is open: a
workspace session names its workspace, branch, and state, and a chat carries a
"Chat · no workspace" badge.

### Two kinds of state

Server state is TanStack Query over `api/routes.ts`. Nothing polls: an event
invalidates what it makes stale. `features/mcp`'s `useMCPEvents` refreshes
the MCP servers and the tool list on `mcp.server`;
`features/workspaces/useWorkspaceEvents`
subscribes to `workspace:<id>` and refreshes the workspace a `workspace.state`
event names; `features/session/useSessionStream` refreshes the run status and
the outline when a turn ends.

Stream state is a Zustand store per feature. The open session's store
(`features/session/store.ts`) is a shell around a pure reducer,
`features/session/transcript.ts`, which folds the event protocol into the rows
the transcript renders. Two sources feed it: run events keyed by `run_id` are
the turn in flight, and `session.message` events keyed by `entry_id` are the
stored conversation a replay delivers. A turn that ends is sealed and its live
rows are replaced by the entries that follow, so nothing is drawn twice. The
reducer is pure, so a scripted event sequence from `docs/api/events.md` is the
whole test. It also holds what the run waits on the user for: `ask_user`
questions and `mcp.elicitation` requests, each dropped by its call's
`tool.result`. An MCP tool's card (`renderers/MCPRenderer.tsx`, chosen for
any name starting `mcp_`) shows a waiting request as its form, and the
server's content blocks once the call is done.

The same reducer keeps the status bar's meter: `turn.progress` and `turn.end`
carry usage, the model's configured context window, the generation time the
harness measured, and the last model call's `timings`: a token count and a
duration for prompt processing and for generation, from the endpoint's own
clock or, for generation alone, from the harness timing the stream. Nothing in
it is inferred from the text on screen. Each assistant entry stores the final
measured context state and timings, which a replay uses to restore the meter
and the speed line after a reload. How the transcript renders — whether
reasoning blocks start open, whether each answer states its speed — is
`features/session/preferences.ts`, a
localStorage store apart from the harness's settings, because two people
reading one session can want different things from it.

### One client, one socket

`api/client.ts` is the only place that calls `fetch`. It attaches the bearer
token, decodes the documented error body into an `ApiError`, and forgets a
token the harness answers `401` to, which returns the user to the sign-in
screen. `api/connection.ts` holds the token and the harness URL in
localStorage; it is a plain module with listeners rather than a store, because
`api/` may not depend on a feature.

`api/stream.ts` is the one WebSocket. Subscribers reference-count topics, so
several components watching one workspace cost one subscription; a reconnect
backs off and re-sends the union of the live topics. The first connection
carries its topics in the handshake URL, so a client that never gets to send a
frame still receives what it asked for. `bus.dropped` reaches every handler,
whatever the topic, because it reports on the connection.

### Reading a transcript

A transcript is read for minutes at a time, so it is set apart from the
chrome around it: `text-base` prose with `components/Markdown`, which styles
headings, lists, quotations, tables, and fenced code (with its language and a
way to copy it) from the theme tokens alone. The three voices are distinct at
a glance — a message the user wrote is the one filled, named block on screen;
the model's answer is unadorned prose; a tool call and the model's reasoning
are collapsed rows that open on a click. Reasoning arrives as its own
`reasoning.delta` stream, so it never interleaves with the answer.

### Tool call rendering

A tool call is a collapsible card. `features/session/renderers/renderers.tsx`
maps a tool name to a renderer: `bash` shows the command, its streamed output,
and its exit code; `ask_user` renders the question form inline and posts the answer;
`web_search` lists its results as links (web URLs only) from the tool's
details; `web_fetch` shows the page, how it was narrowed, and its content. A
tool with no renderer falls back to formatted JSON, so a new tool is useful
before anyone writes a renderer for it.

### Development and production

In development Vite serves the UI on :5173 and proxies `/api` to the harness on
:8080. In production the harness serves the built bundle itself, so the same
relative URLs work in both. Any path that is not a file under the web directory
renders `index.html`, so client-side routes survive a full page load.

## Package dependency direction

Dependencies point inward. An arrow means "may import".

```
        cmd/eika                                cmd/eikad
            |                                        |
            v                                        v
       +----------+                             +---------+
       |  server  |                             |  eikad  |
       +----+-----+                             +---------+
            |
    +-------+---------+---------+---------+----------+----------+
    v                 v         v         v          v          v
  agent  <--------  session   store   workspace   subagent    search     mcp --> mcp/oauth
    |    \              |                  |            |
    |     \             v                  v            |
    |      +-> contextfile           workspace/hub  <----+
    |               |
    v               v
  tool   -------> executor
    |                 ^
    v                 |
 provider    executor/sandbox  ----->  eikad (wire types only)

        +-------------------------------------------+
        |  config, event, secret: imported by all   |
        +-------------------------------------------+

workspace is imported by server and subagent only. Never by tool.
subagent also imports session, store, event, and tool/builtin, for the
Subagents interface the agent tools call it through.
workspace also imports executor, which is what Host.Executor hands back.
tool/builtin imports search and search/fetch for web_search and web_fetch;
search imports nothing from internal/. search/filter (goja) is imported by
cmd/eikad alone, so the harness binary does not link a JavaScript engine.
mcp imports tool, for the tools it hands a run, event, and mcp/oauth, and
never workspace or executor: the server hands it a Store and a Launcher, and
a stdio server's process reaches the workspace through them.
egress imports netguard alone, and search/fetch imports netguard too;
server builds the proxy and answers its policy lookups. workspace imports
executor/sandbox for eikad's PUT /environment, which is kept off the
Executor interface like the terminal.
```

Rules that reviews enforce:

- Nothing imports `server`, except `cmd/`.
- `tool` never imports `workspace`. Tools reach a workspace only through an
  `executor.Executor`.
- `config`, `event`, and `secret` are leaves: they import nothing from
  `internal/`.
- Agent actions on files or processes go through `executor`. The harness
  process never touches the host filesystem on an agent's behalf.

## Testing

`make check` runs `gofmt` and `goimports` verification, `go vet`,
`staticcheck`, `golangci-lint` when it is installed, the Go tests, ESLint,
`tsc`, and Vitest. `goimports` and `staticcheck` are pinned by `tool`
directives in `go.mod`, so a checkout needs no global installs.

The Go targets name `./cmd/... ./internal/...` rather than `./...`:
`web/node_modules` ships Go files of its own (`flatted`), which `./...` would
otherwise walk into.

After `make web-install`, `make check` runs without Docker and without network
access.
