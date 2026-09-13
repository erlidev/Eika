# Architecture

This file describes Eika as it is today, at the end of phase 6. It is updated
in the same change that moves structure. Planned work lives in `docs/PLAN.md`.

## What exists now

Two Go binaries, the agent core, the sandbox machinery agents run in, the
database that outlives them, the HTTP API and event stream that drive all of
it, child agents that work in sandboxes of their own, one frontend, and a
three-service compose stack.

| Piece | Path | Responsibility today |
|---|---|---|
| `eika` | `cmd/eika` | Loads configuration, serves HTTP, optionally serves the built frontend |
| `eikad` | `cmd/eikad` | Sandbox daemon: exec, files, terminal, change watcher |
| `config` | `internal/config` | YAML file plus `EIKA_*` environment overrides, validation, redacted `String()` |
| `event` | `internal/event` | The event envelope, the type name constants, the payload structs, the `Emitter` interface, and the fan-out `Bus` |
| `server` | `internal/server` | Composition of the process, the JSON API, the WebSocket event stream, bearer auth, and the run manager |
| `agent` | `internal/agent` | The agent loop: turns, tool dispatch, steering and follow-up queues, retries, events |
| `provider` | `internal/provider` | The model interface, message and event types, the kind registry, the OpenAI implementation, the scripted fake |
| `tool` | `internal/tool` | The tool interface, call context, result type, and registry; `tool/builtin` holds the eleven built-in tools |
| `executor` | `internal/executor` | The interface every agent action goes through, path validation, and `executor/local` for tests |
| `contextfile` | `internal/contextfile` | AGENTS.md discovery and the system prompt section it becomes |
| `eikad` | `internal/eikad` | The daemon's handlers, path confinement, and wire types |
| `sandbox` | `internal/executor/sandbox` | The production executor: an HTTP client for one eikad |
| `workspace` | `internal/workspace` | Container and volume lifecycle on the Docker daemon |
| `hub` | `internal/workspace/hub` | Bare git repositories and the Smart HTTP endpoint workspaces clone from |
| `store` | `internal/store` | The PostgreSQL pool, the embedded migrations, and the queries behind every table |
| `session` | `internal/session` | The session tree: append, head, branch, fork, outline, and the agent store that records a run |
| `subagent` | `internal/subagent` | Spawning child agents: the hand-over commit, the child workspace and session, the limits, and the result the parent reads |
| frontend | `web/` | Vite, React 19, Tailwind v4, shadcn/ui; an app shell that fetches harness health once, with a Recheck button |

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
  A model can set `reasoning_effort`. An OpenAI-compatible endpoint can also
  set `preserve_thinking`; Eika then sends that extension, captures streamed
  `reasoning_content`, and replays the reasoning with later assistant
  messages. Preservation is enabled when the setting is absent. An endpoint
  that rejects the extension must set it to false. It is not an OpenAI API
  field.
- **tool** holds the `Tool` interface and a registry. A call receives a
  `CallContext` carrying the executor, the event emitter, and the session and
  run identifiers. `tool/builtin` implements `read`, `write`, `edit`, `bash`,
  `grep`, `find`, and `ls` with Pi's semantics, and keeps every output bound in
  `limits.go`. `ask_user` is the one tool that blocks on a human rather than on
  the workspace: it registers a question with the `Questions` broker, emits
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
| `projects` | id, name (unique), kind (remote/local), remote_url, remote_username_env, remote_password_env, host_path, default_branch, created_at | A git repository Eika knows and the names of its optional credential variables |
| `workspaces` | id, project_id, name, branch, base_commit, image, state, container_id, parent_workspace_id, created_at, updated_at | The record of one sandbox container; `state` mirrors `workspace.State` |
| `sessions` | id, workspace_id, title, head_entry_id, parent_session_id, created_at, updated_at | One session tree and the head a run continues from |
| `session_entries` | id, session_id, parent_id, seq, kind, payload (jsonb), commit_sha, created_at | One node of a session tree |
| `runs` | id, session_id, state, started_at, finished_at, error | One execution of the agent loop |
| `subagents` | id, parent_session_id, child_session_id, child_workspace_id, state, result, created_at, finished_at | One child agent and what it reported |
| `settings` | key (primary), value (jsonb) | What the user changes at runtime |

Everything cascades from `projects`: deleting a project deletes its
workspaces, their sessions, and their entries. `sessions.head_entry_id` has no
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

An assistant message can also hold opaque reasoning data for a provider that
must replay it. Tool arguments keep the model's exact text. Valid arguments
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
                    +-- hub.New --------------------- /var/lib/eika/hub
                    +-- workspace.NewHost ----------- docker socket
                    +-- provider.NewRegistry(cfg.Models)
                    +-- builtin.Registry(questions)
                    +-- event.NewBus
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
  the workspace host, the model set, the tool registry, the question broker,
  and the bus. `Workspaces`, `Hub`, and `Models` are interfaces, defined in
  `server` because that is where they are consumed, so the handler tests run
  the whole API against a host backed by temporary directories. A zero `Deps`
  serves the health checks and the event stream alone.
- **Auth** is one middleware over the whole `/api` subtree, comparing the
  bearer token in constant time. `/healthz` is public, and `/git/` is mounted
  outside it because the hub authenticates workspaces itself with
  per-workspace credentials. The event stream is the one route that also
  accepts the token as a query parameter, because a browser cannot set a
  header on a WebSocket handshake.
- **Errors** have one shape, `{"error":{"code","message"}}`. `statusOf` maps
  the sentinel errors of the packages the handlers call onto statuses:
  `store.ErrNotFound`, `workspace.ErrNoWorkspace`, `hub.ErrNoProject`, and
  `builtin.ErrNoQuestion` are 404; `store.ErrConflict` is 409;
  `hub.ErrBadProject`, `workspace.ErrBadBranch`, and `builtin.ErrBadAnswer`
  are 400. Anything unmapped is the harness's own failure: it is logged in
  full and reported as `internal error`, so a database message never reaches a
  client.
- **The run manager** owns one goroutine per active run and at most one run
  per session. A run is not bound to the request that started it: the client
  gets the run row as soon as the loop begins and follows the rest on the
  event stream. The agent is built per run from the workspace's executor, a
  `session.Store` on the session tree, the bus as its emitter, and the model
  the request or the `default_model` setting names. Assistant entries record
  the workspace HEAD through a commit function that runs `git rev-parse HEAD`
  through the executor and reports no commit when the workspace holds no
  repository. `agent.Options` in `runs.begin` is the hook the later phases
  register their tools in: search in phase 7. The subagent tools need no entry
  there; the spawner reaches the run manager itself (see Subagents). A run ends
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

Configuration is loaded once in `main` and passed down as a `config.Config`
value. No other package reads the environment. Precedence, lowest first:

```
config.Default()  ->  the YAML file named by -config  ->  EIKA_* environment variables
```

An empty or comment-only file is valid and means "use the defaults".
`Validate` rejects an empty `listen`, `database_url`, `docker_socket`,
`searxng_url`, `sandbox_image`, `eikad_binary`, `hub_root`, `hub_url`, or
`auth_token`, so a deployment cannot come up with an unauthenticated API by
accident. `sandbox_network` is the one field that may be empty: that is the
development mode where sandboxes publish their daemon port on `127.0.0.1`
instead of being reached by container name.

`allowed_origins` lists the browser origins that may open the event stream,
on top of the one derived from `listen`, which `Load` always prepends. An
entry is a host pattern and a whole URL is reduced to its host, so a
deployment can write either; `EIKA_ALLOWED_ORIGINS` takes a comma-separated
list, which is what `make dev` uses to let the Vite dev server through. A
deployment that serves the frontend from the harness needs none of them,
because a same-origin handshake is always accepted.

The deployed file is `deploy/eika.yaml`; secrets come from the environment
(`EIKA_AUTH_TOKEN`, the per-model `api_key_env` variables, and optional
`EIKA_*` git credential variables). Model API keys and remote git credentials
are never stored in configuration or project rows. A model or remote project
declares the *name* of the environment variable that holds its credential.
`Config.String()` redacts the auth token and the database password so a config
can be logged.

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
 |       (executor/sandbox)   |  bearer      |    /pty   /watch  /healthz       |
 |                            |  EIKAD_TOKEN |                                  |
 |  workspace.Host -----------+--- docker ---+-> container + volume eika-ws-<id>|
 |                            |   socket     |    mounted at /workspace         |
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
project stores only the names of matched `EIKA_*` username and password
variables. The API rejects credentials in `remote_url`. The hub resolves the
variables for each git command and passes their values through the process
environment with a one-line credential helper. The values are never stored,
returned by the API, written to disk, or put in a process argument.

The API also rejects URL query strings and fragments because they can hold
tokens. Migration `0002_remote_credentials` removes userinfo, query strings,
and fragments from old remote URLs. An affected remote project receives the
generic `EIKA_GIT_USERNAME` and `EIKA_GIT_PASSWORD` references. An operator
must set those two variables before Eika fetches or pushes that project again.
A public legacy URL that has none of these credential indicators stays
unchanged and receives no credential references.

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
- **The child's branch** is `<parent branch>-<name>`, not a path below the
  parent's branch: git stores either `refs/heads/main` or
  `refs/heads/main/fix`, never both, and the parent's branch is in the hub by
  then. The name is checked against a narrow pattern before it reaches git,
  and `git check-ref-format` checks the branch itself.
- **The tools** are `spawn_agent`, `wait_agents`, and `list_agents` in
  `internal/tool/builtin`. They reach the spawner through the `Subagents`
  interface declared there, the same shape `ask_user` uses for its question
  broker, so nothing under `tool` learns what a workspace is. `spawn_agent`
  blocks until the child finishes; `wait: false` returns the child's id at
  once, and `wait_agents` collects them later. A tool call only ever names its
  own session's children.
- **The runner.** The spawner drives a child through `subagent.Runner`, which
  the server implements with `runs.runChild`: an ordinary run, bound to the
  context of the parent run that asked for it. So the child's tools, events,
  entries, and retry behavior are the parent's, and no second agent loop
  exists.
- **The limits** are `subagents.max_depth` and `subagents.max_children` in the
  configuration, 2 and 4 by default. Depth is measured by walking `subagents`
  rows up from the spawning session; width counts that session's running
  children, rows and live goroutines alike.
- **Cancellation** flows down. Aborting a run aborts its children, and
  aborting a child aborts its own children in turn, because a child works on a
  branch of a run that has nobody left to report to. An aborted or failed
  child still commits, pushes, and reports: its workspace is stopped, never
  destroyed, so the user can start it again and look at what it did.
- **The result** the parent's model sees is the child's final assistant
  message, its branch, its head commit, and `git diff --stat` from the
  parent's base commit to that head. The same object is stored as JSON on the
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

## Compose topology

```
          127.0.0.1:8080                   127.0.0.1:8888 (debug only)
                 |                                  |
         +-------v--------+   internal net   +------v--------+
         |     eika       +------------------>    searxng    |
         |  (harness)     |                  |  JSON format  |
         +---+--------+---+                  +---------------+
             |        |
   /var/run/ |        | internal net
 docker.sock +        v
 (sibling             +----------------+
  containers)         |    postgres    |  volume: eika-postgres
                      |      16        |  127.0.0.1:5432
                      +----------------+

   volume eika-hub -> /var/lib/eika in the harness; bare repositories live
                      in /var/lib/eika/hub

   sandbox containers eika-ws-<id> join eika_sandbox, which only the harness
   also joins: the harness reaches them by name and they reach the hub at
   http://eika:8080, but never postgres or searxng
```

Every published port binds to 127.0.0.1. Eika is single-user and holds
credentials, so nothing listens on a public interface; put a reverse proxy in
front of it for remote access.

The harness runs as the non-root user `eika` (uid 1000). It still needs the
Docker socket, and the socket's group id differs between hosts, so the image
takes a `DOCKER_GID` build argument (default 999) and adds `eika` to that
group. Accepted risk: access to the Docker socket is equivalent to root on the
host. The harness cannot avoid it, because sandboxes are sibling containers
that it starts itself. Nothing inside a sandbox ever sees the socket.

The harness image is built by the multi-stage `Dockerfile` at the repository
root: stage one builds the frontend with Node 22, stage two builds both Go
binaries statically, and the runtime stage carries `eika`, the frontend bundle,
and `eikad`, staged at `/usr/local/share/eika/eikad`, which is where
`workspace.Host` reads it to copy into sandboxes. The harness mounts the Docker
socket because sandboxes are sibling containers, not children.

`sandbox/Dockerfile` builds the default workspace image. Its build context is
the repository root because it builds `eikad` from source in a builder stage
and bakes it in; the harness still copies its own build over it at container
creation, which is what makes an arbitrary image usable as a sandbox.

`deploy/.env.example` documents every variable. `deploy/searxng/settings.yml`
enables the JSON result format, which the harness needs to parse results.

## Frontend

`web/` is a Vite application. The layout is fixed by the style guide:

```
web/src/
  app/          routes, layout shell, panel registry
  features/     one folder per domain feature (empty until phase 5)
  components/   shared components; components/ui is shadcn-managed
  api/          wire types mirroring docs/api/, HTTP client, event stream
  lib/          pure utilities with tests
```

Server state goes through TanStack Query; the client lives in `main.tsx` and
`api/` owns the wire types and the fetch functions. Streaming and UI state will
use per-feature Zustand stores from phase 5 on.

In development Vite serves the UI on :5173 and proxies `/api` to the harness on
:8080. In production the harness serves the built bundle itself, so the same
relative URLs work in both. Any path that is not a file under the web directory
renders `index.html`, so client-side routes survive a full page load.

## Package dependency direction

Dependencies point inward. An arrow means "may import". Packages in
parentheses do not exist yet; the direction is fixed now so later phases do not
have to renegotiate it.

```
        cmd/eika                                cmd/eikad
            |                                        |
            v                                        v
       +----------+                             +---------+
       |  server  |                             |  eikad  |
       +----+-----+                             +---------+
            |
    +-------+---------+---------+---------+----------+
    v                 v         v         v          v
  agent  <--------  session   store   workspace   subagent   (search)
    |    \              |                  |            |
    |     \             v                  v            |
    |      +-> contextfile           workspace/hub  <----+
    |               |
    v               v
  tool   -------> executor
    |                 ^
    v                 |
 provider    executor/sandbox  ----->  eikad (wire types only)

        +-----------------------------------+
        |  config, event: imported by all   |
        +-----------------------------------+

workspace is imported by server and subagent only. Never by tool.
subagent also imports session, store, event, and tool/builtin, for the
Subagents interface the agent tools call it through.
workspace also imports executor, which is what Host.Executor hands back.
```

Rules that reviews enforce:

- Nothing imports `server`, except `cmd/`.
- `tool` never imports `workspace`. Tools reach a workspace only through an
  `executor.Executor`.
- `config` and `event` are leaves: they import nothing from `internal/`.
- Agent actions on files or processes go through `executor`. The harness
  process never touches the host filesystem on an agent's behalf.

## Testing

`make check` runs `gofmt` and `goimports` verification, `go vet`,
`staticcheck`, `golangci-lint` when it is installed, the Go tests, ESLint,
`tsc`, and Vitest. `goimports` and `staticcheck` are pinned by `tool`
directives in `go.mod`, so CI needs no global installs.

The Go targets name `./cmd/... ./internal/...` rather than `./...`:
`web/node_modules` ships Go files of its own (`flatted`), which `./...` would
otherwise walk into.

After `make web-install`, `make check` runs without Docker and without network
access.
