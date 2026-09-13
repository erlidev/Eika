# Eika Implementation Plan

Eika is a Docker-native agentic coding harness. It ports the core of the
[Pi coding agent](https://github.com/badlogic/pi-mono) (agent loop, tools,
session trees, context files) to Go and adds sandboxed workspaces, parallel
subagents, built-in web search, and a web UI. It is built so that agents can
modify Eika itself.

This document is the source of truth for scope and architecture. Update it
when decisions change. Phase status is tracked in the checklist at the end.

## 1. Decisions

| Area | Decision |
|---|---|
| Backend | Go 1.26, stdlib `net/http` router, `log/slog`, official `openai-go` SDK |
| Frontend | Vite, React 19, TypeScript (strict), Tailwind v4, shadcn/ui |
| Providers | OpenAI-compatible only at launch, behind a `provider.Provider` interface |
| Docker | Harness mounts `/var/run/docker.sock`; sandboxes are sibling containers |
| Persistence | PostgreSQL (`pgx`), embedded SQL migrations; no ORM |
| Extensibility | Source-level modularity plus rebuild; no runtime plugin loader |
| Sandbox image | One default `eika-sandbox` image; per-workspace override by image or Dockerfile |
| Search | SearXNG container plus first-party connectors (Wikipedia, arXiv, ...) |
| Auth | Single user, one bearer token set at deploy time |
| Compaction | Deferred; the session model must support it later |
| Skills / templates | Deferred; AGENTS.md is in scope |
| Config format | YAML via `gopkg.in/yaml.v3`; secrets only through `EIKA_*` env vars |
| Go tooling | `staticcheck` and `goimports` pinned by `tool` directives in `go.mod`; `golangci-lint` optional |
| `EXTENDING.md` examples | One section per extension point exists from phase 0; the copy-pasteable example lands with the phase that creates the interface |
| `internal/event` in phase 0 | The envelope and type names are the contract the later phases and the frontend agree on, so they are fixed before anything emits events |
| `internal/server` in phase 0 | `main` must stay thin, and both binaries need one listener lifecycle; phase 4 extends `routes.go` rather than creating the package |
| Harness process user | Non-root `eika`, added to the host's docker group via a `DOCKER_GID` build arg. Socket access is root-equivalent and accepted: sandboxes are sibling containers |
| OpenAI SDK version | `github.com/openai/openai-go/v3`, pinned at v3.61.0, the latest stable major |
| Chat Completions, not Responses | Providers are "OpenAI-compatible" endpoints. Every such endpoint implements Chat Completions; few implement the Responses API. The `Provider` interface hides the choice, so a Responses implementation can be added later as another kind |
| Retries live in the agent loop | The SDK's retries are switched off (`WithMaxRetries(0)`). One place decides, so the scripted fake provider exercises the same retry path as the real one. Retryable means 408, 409, 429, 5xx, or a transport failure |
| Provider API keys | Resolved from the environment in the provider constructor, the one exception to "packages never read the environment": a key must never enter a `config.Config` value that gets logged or persisted, so configuration carries only the variable's name |
| Event payload structs | Live in `internal/event`, not in the package that emits them, so that the server and the frontend decode events without importing the agent loop, and one file lists the whole protocol |
| Built-in tool registry | `internal/tool/builtin/registry.go`, not `internal/tool/registry.go`: the tools import `tool` for the interface, so the registry of them cannot live in `tool` without an import cycle. `internal/tool/registry.go` holds the `Registry` type |
| Provider kind registry | `provider.NewRegistry` takes each kind's constructor as a parameter, for the same reason: a provider package imports `provider` |
| Tool registry lifetime | One registry is shared by every session; the executor arrives per call in `tool.CallContext`, so tools stay stateless |
| Context file locations | Discovery is workspace-relative and reads through the executor. The "global" file is a workspace path, `.config/eika/AGENTS.md` by default, because the harness filesystem is not reachable from a sandbox |
| `eikad` injection | The harness copies its static `eikad` into each created container (`CopyToContainer`) before starting it, rather than bind-mounting it or using a shared volume. A bind mount source is resolved by the Docker daemon on the *host*, and the harness's filesystem is inside a container; a shared volume would need a helper container to populate it. The copy needs neither and works for any image |
| `eikad` listener | Port 7000, flag-configurable. `cmd/eikad` reuses `server.Serve`, so both binaries share one listener lifecycle |
| `eikad` dependencies | `github.com/coder/websocket` for `/pty` and `/watch` (context-aware, no global state, the maintained successor to nhooyr.io/websocket) and `github.com/creack/pty` for the terminal (the standard Unix pty wrapper; the standard library has none). The watcher polls with the standard library instead of adding fsnotify |
| Docker client | `github.com/docker/docker/client`, the official Engine API client. The alternative is hand-rolling the socket protocol, which the style guide's "boring technology" rule rejects |
| Sandbox network | Sandboxes join their own network, pinned to `eika_sandbox`, which only the harness also joins, so a sandbox cannot reach postgres or searxng. They are addressed by container name. An empty `sandbox_network` publishes each daemon port on `127.0.0.1`, which is how `make dev` and the Docker tests reach a sandbox from outside compose |
| Sandbox confinement | Docker's init is PID 1, all capabilities are dropped, `no-new-privileges` is set, and the container runs as uid 1000 by default. A custom image must have that uid or set `Spec.User` |
| Hub access scope | A hub grant covers one project: `Grant(workspaceID, project, token)`, checked against the requested project on every request. A workspace's token is useless on any other project, and a stopped workspace holds no grant |
| Hub package | `internal/workspace/hub`, not `internal/hub`: the hub exists to serve workspaces and `workspace` is its only importer |
| Hub credentials | Remote credentials reach `git` through a one-line credential helper reading environment variables, so a token is never written to disk. Inside a workspace the same shape reads `EIKA_HUB_USER` and `EIKA_HUB_TOKEN`, so the hub token never appears in a remote URL or in `.git/config` |
| pgx version | `github.com/jackc/pgx/v5`, pinned at v5.11.0, the latest stable major. The pool (`pgxpool`) and the native protocol come with it, so nothing else is needed; adding it upgrades the module graph's `golang.org/x/{mod,sync,telemetry,tools}` entries, which are indirect tool dependencies |
| Migrations | A 40-line migrator in `internal/store/migrate.go` over an embedded `migrations/NNNN_name.sql` directory, applied in file name order under a PostgreSQL advisory lock, one transaction per file, recorded in `schema_migrations`. golang-migrate would be a dependency for less |
| Id format | One generator, `store.NewID`: 20 lowercase base32 characters, 96 bits of randomness. Ids are text in every table, generated before the row exists, so the same id names a row, a container, a volume, and a URL |
| Fork semantics | A fork copies the path from the root to the fork entry into a new session and shares no rows with its parent. Shared ancestry would make either session's deletion or edit reach into the other, and the copy is small: a path is a few dozen rows |
| Entry representation | One `provider.Message` is one entry; an assistant message that carries tool calls stays one assistant entry whose payload is that message's JSON. It round-trips exactly and adds no branch point the agent loop cannot resume from, because a model that asked for three calls needs all three answered |
| Entry kinds | The kind vocabulary (`user`, `assistant`, `tool_call`, `tool_result`, `system`, `event`) is fixed now, like the event names in phase 0, so later phases and the frontend agree. Message conversion writes `user`, `assistant`, and `tool_result`; questions, subagent lifecycle, and compaction write `event` |
| Hub mirrors local projects | Yes. A local project keeps its bind mount for the user's own workspace, and its workspaces still push to a hub repository, so fork-with-workspace and subagents work the same way for both kinds. This resolves the phase 3 open question; nothing in the schema depends on it, and phase 6 implements the push |

## 2. Core principle: every agent action runs in a sandbox

The harness process never reads, writes, or executes anything on behalf of
an agent outside a workspace container. All tools that touch files or run
commands do so through an `Executor` interface whose only production
implementation talks to a sandbox. Search and LLM calls run in the harness
because they are network calls, not filesystem or process actions.

## 3. Domain model

```
Project        A git repository known to Eika. Backed by a bare repo in the
               harness "hub" volume and optionally a remote (GitHub etc.) or a
               host directory.
Workspace      A sandbox container + volume holding a clone of a Project on a
               branch. Sessions run inside a workspace. Has a lifecycle:
               creating -> running -> stopped -> archived.
Session        A tree of entries (user, assistant, tool call, tool result,
               system events) inside one workspace. Has a head pointer.
Agent run      One execution of the agent loop on a session from its head.
Subagent       A child agent run in its own Workspace (cloned from the parent's
               workspace at its current commit, on a child branch), with its
               own Session. Reports back to the parent as a tool result.
```

### Git flow

- The harness serves git over HTTP on the internal Docker network
  (`/git/<project>.git`, Smart HTTP via `git http-backend`). Every workspace
  clones from and pushes to the hub. The hub is the single point of exchange
  between workspaces, subagents, and the user.
- **Remote projects** (e.g. GitHub): the hub mirrors the remote. The UI offers
  "push branch to remote" and "open PR" style actions. Credentials stay in the
  harness; sandboxes never hold remote credentials.
- **Local projects**: a host directory is bind-mounted into the workspace at
  `/workspace`. The agent commits there directly. No hub involvement unless
  the user forks a subagent, in which case the hub is used for the child.
- **Subagents**: parent commits its work-in-progress to a `wip` commit on its
  branch and pushes to the hub; child workspace clones at that commit on
  `<parent-branch>/<subagent-name>`. When the child finishes it pushes; the
  parent gets a summary plus the branch name and may fetch and merge.

### Session trees

Entries form a tree (`parent_id`), like Pi. Eika leans into what a sandbox
enables:

- **Branch in place**: move the head to any entry and continue.
- **Fork with workspace**: fork from an entry into a new workspace cloned at
  the commit recorded for that entry. This gives a true "what if" branch where
  the filesystem also rewinds. Each assistant turn records the workspace's
  HEAD commit so this is possible.

### Working with the user (ergonomics)

- **Steering and follow-up** message queues as in Pi, exposed in the UI.
- **`ask_user` tool**: the agent can pose a structured question (options or
  free text). The UI renders it as a form; the run pauses until answered.
- **Shared sandbox**: the user has a terminal and file editor into the same
  container the agent works in. Edits by either side are visible to both.
- **Review surface**: every workspace shows a live diff against its base
  commit with commit, push, and "hand to a subagent" actions.
- **Notifications**: subagent completion, questions, and errors surface as
  events in the UI event stream.

## 4. Repository layout

```
Eika/
  AGENTS.md                  Entry point for agents working on Eika
  docs/
    PLAN.md                  This file
    STYLE_GUIDE.md           Coding and documentation rules (Go, TS, docs)
    ARCHITECTURE.md          How the pieces fit; written in phase 1, kept current
    EXTENDING.md             How to add a tool, provider, search source, UI panel
    api/                     Event protocol and HTTP API reference
  cmd/
    eika/                    Harness server binary
    eikad/                   Sandbox daemon binary (static, runs inside sandboxes)
  internal/
    agent/                   Agent loop (Pi port): turns, queues, tool dispatch
    provider/                Provider interface; provider/openai implementation
    tool/                    Tool interface, registry, built-in tools
    executor/                Executor interface; executor/sandbox (eikad client),
                             executor/local (tests only)
    session/                 Session tree model and persistence
    workspace/               Workspace lifecycle, Docker client, volumes, git hub
    subagent/                Spawning and reporting
    search/                  Source interface; search/searxng, wikipedia, arxiv, fetch
    contextfile/             AGENTS.md discovery and assembly
    server/                  HTTP API, WebSocket event stream, auth
    store/                   Postgres access and migrations
    config/                  Config loading (YAML + env)
    event/                   Shared event types emitted by the loop and UI
  sandbox/
    Dockerfile               eika-sandbox image
  web/                       Frontend (Vite + React)
  deploy/
    docker-compose.yml       eika, postgres, searxng
    searxng/settings.yml
  Makefile
```

`internal/` packages depend inward: `server -> agent -> tool -> executor`;
nothing under `tool` imports `workspace`. Dependency direction is enforced by
review and by `go vet`-style checks in CI where practical.

## 5. Key interfaces

```go
// provider
type Provider interface {
    Stream(ctx context.Context, req Request) (<-chan Event, error)
}

// tool
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage           // JSON Schema for parameters
    Call(ctx context.Context, c CallContext, args json.RawMessage) (Result, error)
}

// executor: the only way tools touch a workspace
type Executor interface {
    Root() string                                                  // workspace root
    Exec(ctx context.Context, spec ExecSpec) (ExecResult, error)   // streams output
    ReadFile(ctx context.Context, path string, opts ReadOpts) ([]byte, error)
    WriteFile(ctx context.Context, path string, data []byte) error
    Stat(ctx context.Context, path string) (FileInfo, error)
    List(ctx context.Context, path string) ([]FileInfo, error)
}

// search
type Source interface {
    Name() string
    Search(ctx context.Context, q Query) ([]Result, error)
}
```

Adding a tool, provider, or search source means: create a package, implement
the interface, register it in one registry file, add tests, document it in
`docs/EXTENDING.md`. That is the whole malleability story.

## 6. Sandbox daemon (`eikad`)

A small static Go binary baked into `eika-sandbox` and copied into every
container the harness creates, so any image works. It listens on the Docker
network on port 7000 and provides: exec with streaming stdout/stderr and exit
codes, PTY sessions for the web terminal, file read/write/stat/list, and a
file-change watcher for the editor. The harness authenticates with a
per-workspace token passed as `EIKAD_TOKEN` at container start. The API is
documented in `docs/api/eikad.md`.

## 7. Event protocol

One WebSocket per UI client, subscribed to topics (`workspace:<id>`,
`session:<id>`, `global`). Events are the same types the agent loop emits
internally (`internal/event`): `turn.start`, `message.delta`, `tool.call`,
`tool.output`, `tool.result`, `turn.end`, `run.error`, `question.asked`,
`subagent.started`, `subagent.finished`, `workspace.state`. Documented in
`docs/api/events.md` and mirrored as TypeScript types in `web/src/api/`.

## 8. Phases

Each phase ends with: tests green, docs updated, `make check` clean. Phases
are sized so one Opus subagent can own one phase (or one half of a large
phase) in a single focused effort. Where phases are independent they run in
parallel.

### Phase 0: Foundations
- Go module, `web/` scaffold, `Makefile` (`build`, `test`, `lint`, `check`, `dev`).
- CI-style checks: `go vet`, `staticcheck`, `gofmt`, `tsc`, `eslint`, `prettier`.
- `docker-compose.yml` skeleton with eika, postgres, searxng.
- `docs/ARCHITECTURE.md` initial version.

### Phase 1: Agent core (Pi port)
- `provider`: interface + OpenAI implementation (Chat Completions, streaming,
  tool calls, usage). Model registry from config.
- `tool`: interface, registry, built-ins `read`, `write`, `edit`, `bash`,
  `grep`, `find`, `ls` with Pi's semantics (offset/limit reads, old/new
  string edits, output truncation).
- `executor/local` for tests only.
- `agent`: loop with streaming, tool dispatch, steering and follow-up queues,
  abort, error recovery. In-memory session for now; a `Store` interface that
  phase 3 implements against PostgreSQL.
- `contextfile`: AGENTS.md discovery (project, parents, global).
- Table-driven tests with a fake provider.

### Phase 2: Sandboxes and workspaces
- `cmd/eikad` and `sandbox/Dockerfile`.
- `executor/sandbox` client.
- `workspace`: Docker client wrapper, create/start/stop/destroy, volumes,
  custom image and Dockerfile builds, injection of `eikad` for custom images.
- Git hub: bare repos, Smart HTTP endpoint, remote mirroring, local bind mode.
- Integration tests gated behind a `docker` build tag.

### Phase 3: Persistence and sessions
- Postgres schema and migrations: projects, workspaces, sessions, entries,
  runs, subagents, settings.
- `session`: tree model, head management, fork, fork-with-workspace.
- `store`: query layer, transactional writes for entries.

### Phase 4: Server and event stream
- Wiring left from phase 2, and phase 4 owns it: nothing constructs
  `hub.Hub` or `workspace.Host` yet, and nothing serves the hub. Phase 4
  builds both in `cmd/eika` from configuration, mounts `hub.Handler` at
  `/git`, and reconciles workspaces with `Host.List` on startup. Until then
  `hub_url` in `deploy/eika.yaml` is inert: it is the address a sandbox will
  use once the harness serves the hub.
- HTTP API for projects, workspaces, sessions, runs, questions, settings.
- WebSocket event stream with topics and replay from an entry id.
- Bearer token auth.
- API docs in `docs/api/`.

### Phase 5: Web UI (core)
- App shell, workspace list and creation, session view with streaming
  messages, tool call rendering (collapsible, per-tool renderers), queues,
  `ask_user` forms, session tree navigation, settings.

### Phase 6: Subagents
- `subagent`: `spawn_agent` tool, child workspace creation via hub, branch
  naming, concurrency limits, cancellation propagation, result reporting.
- UI: agent tree panel, jump into a child session.

### Phase 7: Search
- `search`: interface, aggregator, SearXNG source, Wikipedia, arXiv, `fetch`
  (HTML to markdown with readability). Tools `web_search` and `web_fetch`.
- Configurable source list, per-source rate limits, result caching.

### Phase 8: Terminal, editor, diff
- `eikad` PTY endpoint, xterm.js terminal panel.
- File tree and Monaco editor with save through `eikad`.
- Diff view against base commit; commit, push, open-PR-style actions.

### Phase 9: Hardening and docs
- End-to-end smoke test in compose.
- Docs pass: `ARCHITECTURE.md`, `EXTENDING.md`, API docs current.
- Resource limits for sandboxes (CPU, memory, pids), network policy option.

## 9. Testing strategy

- Unit tests everywhere with the standard library; `executor/local` and a
  fake provider make the agent loop fully testable without Docker or a model.
- Docker-dependent tests use build tag `docker` and run in CI only when a
  socket is available.
- Frontend: Vitest for logic, Playwright smoke test against compose in phase 9.

## 10. Resolved and open questions

- Sandbox network policy defaults to open egress, configurable per workspace.
- Resolved in phase 3: the hub mirrors local projects as well, so
  fork-with-workspace and subagents behave the same for both project kinds.
  See the decision table.

## 11. Phase checklist

- [x] Phase 0: Foundations
- [x] Phase 1: Agent core
- [x] Phase 2: Sandboxes and workspaces
- [x] Phase 3: Persistence and sessions
- [ ] Phase 4: Server and event stream
- [ ] Phase 5: Web UI core
- [ ] Phase 6: Subagents
- [ ] Phase 7: Search
- [ ] Phase 8: Terminal, editor, diff
- [ ] Phase 9: Hardening and docs
