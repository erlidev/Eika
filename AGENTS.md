# Working on Eika

Eika is a personal assistant that runs locally in Docker, written in Go
(backend) and React + TypeScript (frontend). It is being built in phases
(`docs/PLAN.md`); today it runs chats, the `home` computer and a sandboxed
`bash`. This file is the entry point for any agent or human changing the
codebase. Read it fully before editing.

## Start here

1. `docs/STYLE_GUIDE.md`: coding and documentation rules. Mandatory.
2. `docs/ARCHITECTURE.md`: how the system works and fits together.
3. `docs/DECISIONS.md`: why it is built that way. Read the section for the
   area you change before reversing anything.
4. `docs/EXTENDING.md` when adding a tool, provider, search backend, or UI page.
5. `docs/PLAN.md` and `docs/design/`: the phased plan that turns Eika into a
   persistent local assistant. Work on it starts from the plan's ground rules.
   `docs/DEFERRED.md` holds what was cut from it: do not start that work unless it
   has been moved into the plan. `docs/PLAN_LOG.md` is the plan's log: add to it,
   and read it only for the entry an item points to.

## Rules that are never optional

- Every agent action on files or processes goes through `internal/executor`.
  Never add code that lets a tool touch the harness filesystem or shell.
- Run `make check` before declaring work done. It formats, vets, lints, and
  tests both Go and the frontend, then compares the UI with its visual
  baselines (about a minute in all). Fix everything it reports. There is
  no CI: `make check` is the only gate, so run it locally before every push.
- Test what matters, and no more: see "Testing" below. Test code is code
  someone has to read, run and keep working, so a test must earn its place.
- Keep documentation current in the same change: `ARCHITECTURE.md` if
  structure moved, `DECISIONS.md` for a new decision, `EXTENDING.md` if an
  extension point changed, `docs/api/` if the API or event protocol changed.
  A changed wire type also means `make contract`, which the tests require.
- Run `make smoke` after changing a compose file, a Dockerfile, `deploy/`,
  or how `server.Run` wires the process. `make check` builds
  `sandbox/Dockerfile` for its Docker tests, but covers no compose file, no
  other Dockerfile, no `deploy/`, and not the wiring.
- Do not add a dependency without a one-line justification in the commit
  message and confirmation that nothing in the standard library covers it.
- Do not introduce a second way to do something that already has one way
  (a second config loader, a second logger, a second HTTP client wrapper).

## Testing

Write the feature first, then the few tests that would catch it breaking. A
change normally carries less test code than product code. If your tests are
longer than the code they cover, stop and cut them before going on.

Write a test for:

- behavior a user or a caller would notice breaking, once, through the public
  API of the package that owns it
- security and isolation boundaries: the sandbox and executor, egress,
  `netguard`, secrets, authentication, redaction
- data safety: migrations of a populated database, restart recovery, anything
  that could lose work or run it twice
- concurrency that is easy to get wrong: queue order, leases, cancellation
- parsers and protocol code, one case per distinct real input
- a bug you fixed, when it is subtle enough to come back

Do not write a test for:

- behavior another test already covers. Do not repeat a check at the store,
  session and handler layers: test it where it lives
- trivial code: getters, constructors, defaults, struct mapping, formatting
- the exact wording of an error, prompt, log line or description, call counts
  on a fake, or any other implementation detail
- every permutation of an input. A table holds the cases that take different
  branches, not fifteen that take the same one
- the fakes, mocks and test helpers themselves
- branches that cannot happen, or lines that only lack coverage. Coverage is
  not a goal and no number is tracked

A mistake you made and fixed while building a feature needs no regression test
of its own: the feature's test covers the corrected behavior. When a test gets
in the way of a legitimate change and guards nothing a user would notice,
delete it in that change and say so in the commit body.

`docs/STYLE_GUIDE.md` has the mechanics (table tests, fakes, the Docker tag,
Vitest and Playwright).

## Layout

```
cmd/eika      harness server         internal/agent     agent loop
cmd/eikad     sandbox daemon         internal/tool      tools and registry
internal/provider  LLM providers     internal/executor  sandbox I/O
internal/computer  containers        internal/session   session trees
internal/search    web search, fetch internal/server    HTTP + WebSocket
internal/store     Postgres          internal/config    deployment config
internal/secret    sealed secrets    web/               frontend
internal/egress    sandbox egress    internal/netguard  public-only dials
internal/mcp       MCP client        internal/utility   utility model tasks
internal/event     event bus         internal/eikad     sandbox daemon wire
internal/imaging   image input       sandbox/           sandbox image
internal/skill     skill parser, zip reader
internal/inference model-call queue  (standard library only)
compose.yaml       the whole stack   deploy/            entrypoint, searxng
docs/              documentation     evals/             live tests, model bench
```

Dependencies point inward. `server` may import `agent`; `agent` may import
`tool`; `tool` may import `executor`. Nothing but the binaries imports `server`.
`tool` never imports `computer`. `internal/imports_test.go` enforces these
rules; an exception is listed there with its reason.

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
make live       the live tests against the real model (evals/); never in check
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
| utility task    | function in `internal/utility` | its trigger in `internal/server`, which resolves a class with `resolveClass` (`internal/server/classes.go`) |
| API endpoint    | handler in `internal/server` | `internal/server/routes.go` + `routeWire` in `contract_test.go` + `docs/api/http.md` |
| event type      | struct in `internal/event`   | `docs/api/events.md` + `web/src/api/events.ts` + `eventPayloads` in `internal/server/contract_test.go` |
| UI page         | component in `web/src/features/` | route in `web/src/app/App.tsx` + `web/src/app/pages.ts` |

## When unsure

Prefer the smaller change. Prefer the existing pattern. If a decision is
architectural (new package, new dependency, new persistence shape, changed
interface), add it to `docs/DECISIONS.md` before implementing.
