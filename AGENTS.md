# Working on Eika

Eika is a Docker-native agentic coding harness written in Go (backend) and
React + TypeScript (frontend). This file is the entry point for any agent or
human changing the codebase. Read it fully before editing.

## Start here

1. `docs/STYLE_GUIDE.md`: coding and documentation rules. Mandatory.
2. `docs/ARCHITECTURE.md`: how the system works and fits together.
3. `docs/DECISIONS.md`: why it is built that way. Read the section for the
   area you change before reversing anything.
4. `docs/EXTENDING.md` when adding a tool, provider, search backend, or UI panel.

## Rules that are never optional

- Every agent action on files or processes goes through `internal/executor`.
  Never add code that lets a tool touch the harness filesystem or shell.
- Run `make check` before declaring work done. It formats, vets, lints, and
  tests both Go and the frontend, then compares the UI with its visual
  baselines (about a minute in all). Fix everything it reports. There is
  no CI: `make check` is the only gate, so run it locally before every push.
- New behavior needs tests. A bug fix needs a regression test. See the style
  guide for what a good test looks like here.
- Keep documentation current in the same change: `ARCHITECTURE.md` if
  structure moved, `DECISIONS.md` for a new decision, `EXTENDING.md` if an
  extension point changed, `docs/api/` if the API or event protocol changed.
  A changed wire type also means `make contract`, which the tests require.
- Run `make smoke` after changing a compose file, a Dockerfile, `deploy/`,
  or how `server.Run` wires the process: `make check` covers none of them.
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
internal/search    web search, fetch internal/server    HTTP + WebSocket
internal/store     Postgres          internal/config    deployment config
internal/secret    sealed secrets    web/               frontend
internal/egress    sandbox egress    internal/netguard  public-only dials
internal/mcp       MCP client        internal/subagent  child agents
internal/utility   utility model tasks
sandbox/           sandbox image     compose.yaml       the whole stack
deploy/            entrypoint, searxng  docs/           documentation
```

Dependencies point inward. `server` may import `agent`; `agent` may import
`tool`; `tool` may import `executor`. Nothing imports `server`. `tool` never
imports `workspace`.

## Commands

```
make local      build and run the whole stack in Docker, as a deployment does
make dev        run harness + frontend in watch mode against compose services
make check      fmt, vet, lint, test (Go and web) side by side, then visual
make test       tests only
make build      build harness, eikad, and frontend
make sandbox    build the eika-sandbox image
make visual     compare UI screens with their baselines (web/e2e, <1 min)
make contract   rewrite docs/api/contract.json after a Go wire type changes
make smoke      the real stack in compose, setup to a sandboxed run (minutes)
```

To see the UI, run `cd web && npm run shot -- --help`: it opens a scenario
against a mock harness, runs steps like `click Settings`, and saves
screenshots plus the page's accessibility tree. `web/e2e/README.md` has the
details. Check UI changes this way before declaring them done.

## How to add something

Each extension point has one registry file. Add your package, register it
there, test it, document it. Full walkthroughs are in `docs/EXTENDING.md`.

| Adding a...     | Implement                    | Register in                        |
|-----------------|------------------------------|------------------------------------|
| tool            | `tool.Tool`                  | `internal/tool/builtin/registry.go`|
| provider        | `provider.Provider`          | `internal/provider/registry.go`    |
| search backend  | `search.Searcher`            | `internal/search/registry.go`      |
| utility task    | function in `internal/utility` | `Known` in `internal/utility/utility.go` + `web/src/features/settings/UtilityModels.tsx` |
| API endpoint    | handler in `internal/server` | `internal/server/routes.go` + `routeWire` in `contract_test.go` |
| event type      | struct in `internal/event`   | `docs/api/events.md` + `web/src/api/events.ts` + `eventPayloads` in `internal/server/contract_test.go` |
| UI panel        | component in `web/src/`      | `web/src/app/panels.tsx`           |

## When unsure

Prefer the smaller change. Prefer the existing pattern. If a decision is
architectural (new package, new dependency, new persistence shape, changed
interface), add it to `docs/DECISIONS.md` before implementing.
