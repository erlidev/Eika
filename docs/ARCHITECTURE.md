# Architecture

This file describes Eika as it is today. It is updated in the same change that
moves structure. Planned work lives in `docs/PLAN.md`.

## What exists now

Two Go binaries, the agent core, one frontend, and a three-service compose
stack. The agent core runs in-process: nothing wires it into the server yet,
which is phase 4.

| Piece | Path | Responsibility today |
|---|---|---|
| `eika` | `cmd/eika` | Loads configuration, serves HTTP, optionally serves the built frontend |
| `eikad` | `cmd/eikad` | Sandbox daemon placeholder; serves `/healthz` only |
| `config` | `internal/config` | YAML file plus `EIKA_*` environment overrides, validation, redacted `String()` |
| `event` | `internal/event` | The event envelope, the type name constants, the run payload structs, and the `Emitter` interface |
| `server` | `internal/server` | Routing, the HTTP listener lifecycle, the health handlers |
| `agent` | `internal/agent` | The agent loop: turns, tool dispatch, steering and follow-up queues, retries, events |
| `provider` | `internal/provider` | The model interface, message and event types, the kind registry, the OpenAI implementation, the scripted fake |
| `tool` | `internal/tool` | The tool interface, call context, result type, and registry; `tool/builtin` holds the seven built-in tools |
| `executor` | `internal/executor` | The interface every agent action goes through, path validation, and `executor/local` for tests |
| `contextfile` | `internal/contextfile` | AGENTS.md discovery and the system prompt section it becomes |
| frontend | `web/` | Vite, React 19, Tailwind v4, shadcn/ui; an app shell that fetches harness health once, with a Recheck button |

`eika` serves `GET /healthz` and `GET /api/healthz`, both returning
`{"status":"ok"}`. `/healthz` is the container health check; `/api/healthz` is
what the frontend calls, so the same URL works behind the Vite dev proxy and in
production. With `-web <dir>` the harness also serves the built frontend and
falls back to `index.html` for unknown paths.

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
- **tool** holds the `Tool` interface and a registry. A call receives a
  `CallContext` carrying the executor, the event emitter, and the session and
  run identifiers. `tool/builtin` implements `read`, `write`, `edit`, `bash`,
  `grep`, `find`, and `ls` with Pi's semantics, and keeps every output bound in
  `limits.go`.
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
the session stays valid for the next request.

A retryable provider failure (429, 5xx, or a transport error) is retried with
exponential backoff, at most `MaxRetries` times, honouring `Retry-After`. The
SDK's own retries are switched off so that one place decides. Anything else
fails the run with a `run.error` event.

Messages go to a `Store` as they are appended. Phase 3 implements it against
PostgreSQL; `agent.MemoryStore` is what exists now.

## Configuration

Configuration is loaded once in `main` and passed down as a `config.Config`
value. No other package reads the environment. Precedence, lowest first:

```
config.Default()  ->  the YAML file named by -config  ->  EIKA_* environment variables
```

An empty or comment-only file is valid and means "use the defaults".
`Validate` rejects an empty `listen`, `database_url`, `docker_socket`,
`searxng_url`, `sandbox_image`, or `auth_token`, so a deployment cannot come up
with an unauthenticated API by accident.

The deployed file is `deploy/eika.yaml`; secrets come from the environment
(`EIKA_AUTH_TOKEN`, and the per-model `api_key_env` variables). Model API keys
are never stored in configuration: a model declares the *name* of the
environment variable that holds its key. `Config.String()` redacts the auth
token and the database password so a config can be logged.

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

   volume eika-hub -> /var/lib/eika in the harness (bare git repos, phase 2)
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
and `eikad` (staged at `/usr/local/share/eika/eikad` so phase 2 can mount it
into sandboxes). The harness mounts the Docker socket because sandboxes are
sibling containers, not children.

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
       +----------+                            (sandbox daemon
       |  server  |                             internals, phase 2)
       +----+-----+
            |
    +-------+----------+--------------+
    v                  v              v
  agent              (session)      (search)
    |    \               |
    |     \              v
    |      +-> contextfile   (store)
    |               |
    v               v
  tool   -------> executor
    |
    v
 provider

        +-----------------------------------+
        |  config, event: imported by all   |
        +-----------------------------------+

(workspace) is imported by server and subagent only. Never by tool.
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
