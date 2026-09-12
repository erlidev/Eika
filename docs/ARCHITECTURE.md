# Architecture

This file describes Eika as it is today, at the end of phase 0. It is updated
in the same change that moves structure. Planned work lives in `docs/PLAN.md`.

## What exists now

Two Go binaries, one frontend, and a three-service compose stack.

| Piece | Path | Responsibility today |
|---|---|---|
| `eika` | `cmd/eika` | Loads configuration, serves HTTP, optionally serves the built frontend |
| `eikad` | `cmd/eikad` | Sandbox daemon placeholder; serves `/healthz` only |
| `config` | `internal/config` | YAML file plus `EIKA_*` environment overrides, validation, redacted `String()` |
| `event` | `internal/event` | The event envelope and the event type name constants |
| `server` | `internal/server` | Routing, the HTTP listener lifecycle, the health handlers |
| frontend | `web/` | Vite, React 19, Tailwind v4, shadcn/ui; an app shell that polls the harness |

`eika` serves `GET /healthz` and `GET /api/healthz`, both returning
`{"status":"ok"}`. `/healthz` is the container health check; `/api/healthz` is
what the frontend calls, so the same URL works behind the Vite dev proxy and in
production. With `-web <dir>` the harness also serves the built frontend and
falls back to `index.html` for unknown paths.

## Configuration

Configuration is loaded once in `main` and passed down as a `config.Config`
value. No other package reads the environment. Precedence, lowest first:

```
config.Default()  ->  the YAML file named by -config  ->  EIKA_* environment variables
```

The deployed file is `deploy/eika.yaml`; secrets come from the environment
(`EIKA_AUTH_TOKEN`, and the per-model `api_key_env` variables). Model API keys
are never stored in configuration: a model declares the *name* of the
environment variable that holds its key. `Config.String()` redacts the auth
token and the database password so a config can be logged.

## Compose topology

```
                 host :8080                      host :8888 (debug only)
                     |                                  |
             +-------v--------+   internal net   +-------v-------+
             |     eika       +------------------>    searxng    |
             |  (harness)     |                  |  JSON format  |
             +---+--------+---+                  +---------------+
                 |        |
   /var/run/     |        | internal net
   docker.sock <-+        v
   (sibling containers)  +----------------+
                         |    postgres    |  volume: eika-postgres
                         |      16        |
                         +----------------+

   volume eika-hub -> /var/lib/eika in the harness (bare git repos, phase 2)
```

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

In development Vite serves the UI on :5173 and proxies `/api` to the harness on
:8080. In production the harness serves the built bundle itself, so the same
relative URLs work in both.

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
(agent)            (session)      (search)
    |                  |
    |                  v
    |              (store)
    v
 (tool)  ------>  (executor)
    |
    v
(provider)      (contextfile)

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

`make check` runs `gofmt` verification, `go vet`, `staticcheck` (pinned through
a `tool` directive in `go.mod`, so no global install is needed),
`golangci-lint` when it is installed, `go test ./...`, ESLint, `tsc`, and
Vitest. Everything runs without Docker and without network access.
