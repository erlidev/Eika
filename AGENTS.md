# Working on Eika

Eika is a Docker-native agentic coding harness written in Go (backend) and
React + TypeScript (frontend). This file is the entry point for any agent or
human changing the codebase. Read it fully before editing.

## Start here

1. `docs/PLAN.md` for scope, architecture, and phase status.
2. `docs/STYLE_GUIDE.md` for coding and documentation rules. These are
   mandatory.
3. `docs/ARCHITECTURE.md` for how the packages fit together.
4. `docs/EXTENDING.md` when adding a tool, provider, search source, or UI panel.

## Rules that are never optional

- Every agent action on files or processes goes through `internal/executor`.
  Never add code that lets a tool touch the harness filesystem or shell.
- Run `make check` before declaring work done. It formats, vets, lints, and
  tests both Go and the frontend. Fix everything it reports.
- New behavior needs tests. A bug fix needs a regression test. See the style
  guide for what a good test looks like here.
- Keep documentation current in the same change: `PLAN.md` checklist,
  `ARCHITECTURE.md` if structure moved, `EXTENDING.md` if an extension point
  changed, `docs/api/` if the API or event protocol changed.
- Do not add a dependency without a one-line justification in the commit
  message and confirmation that nothing in the standard library covers it.
- Do not introduce a second way to do something that already has one way
  (a second config loader, a second logger, a second HTTP client wrapper).

## Layout

```
cmd/eika      harness server         internal/agent     agent loop
cmd/eikad     sandbox daemon         internal/tool      tools and registry
internal/provider  LLM providers     internal/executor  sandbox I/O
internal/workspace containers, hub   internal/session   session trees
internal/search    search sources    internal/server    HTTP + WebSocket
internal/store     Postgres          internal/config    configuration
web/          frontend               sandbox/           sandbox image
deploy/       compose, searxng       docs/              documentation
```

Dependencies point inward. `server` may import `agent`; `agent` may import
`tool`; `tool` may import `executor`. Nothing imports `server`. `tool` never
imports `workspace`.

## Commands

```
make dev        run harness + frontend in watch mode against compose services
make check      fmt, vet, lint, test (Go and web)
make test       tests only
make build      build harness, eikad, and frontend
make sandbox    build the eika-sandbox image
```

## How to add something

Each extension point has one registry file. Add your package, register it
there, test it, document it. Full walkthroughs are in `docs/EXTENDING.md`.

| Adding a...     | Implement                    | Register in                        |
|-----------------|------------------------------|------------------------------------|
| tool            | `tool.Tool`                  | `internal/tool/builtin/registry.go`|
| provider        | `provider.Provider`          | `internal/provider/registry.go`    |
| search source   | `search.Source`              | `internal/search/registry.go`      |
| API endpoint    | handler in `internal/server` | `internal/server/routes.go`        |
| event type      | struct in `internal/event`   | `docs/api/events.md` + `web/src/api/events.ts` |
| UI panel        | component in `web/src/`      | `web/src/app/panels.tsx`           |

## When unsure

Prefer the smaller change. Prefer the existing pattern. If a decision is
architectural (new package, new dependency, new persistence shape, changed
interface), write it into `docs/PLAN.md` under Decisions before implementing.
