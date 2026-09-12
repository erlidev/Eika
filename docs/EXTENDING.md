# Extending Eika

One section per extension point. Each one gets a complete, copy-pasteable
minimal example when the phase that introduces the extension point lands; see
`docs/PLAN.md` for phase status. Until then a section states where the code
goes and where it is registered.

Registration is always explicit, in one registry file per extension point.
Eika never registers anything from `init()`.

## Adding a tool

Implement `tool.Tool` in `internal/tool/` (or a subpackage) and register it in
`internal/tool/registry.go`. Tools touch a workspace only through an
`executor.Executor`; a tool that imports `internal/workspace` is a bug.

Lands in phase 1.

## Adding a provider

Implement `provider.Provider` in `internal/provider/<name>/` and register it in
`internal/provider/registry.go`. Models are declared in configuration, not in
code: a provider reads its base URL and API key environment variable name from
`config.Model`.

Lands in phase 1.

## Adding a search source

Implement `search.Source` in `internal/search/<name>/` and register it in
`internal/search/registry.go`. Search runs in the harness, not in a sandbox,
because it is a network call rather than a filesystem or process action.

Lands in phase 7.

## Adding an API endpoint

Write the handler in `internal/server/` and register the route in
`internal/server/routes.go`. Every route except `/healthz` requires the bearer
token. Document the request and response in `docs/api/`.

Routing exists now; auth and the rest of the API land in phase 4.

## Adding an event type

Add the type name constant to `internal/event/event.go`, define the payload
struct in the package that emits the event, and mirror both in
`docs/api/events.md` and `web/src/api/events.ts` in the same change. The
envelope (`Type`, `Topic`, `Time`, `Payload`) never changes per event type.

The envelope and the constants exist now; payload structs arrive with the
phases that emit them.

## Adding a UI panel

Write the component under `web/src/features/<feature>/` and register it in
`web/src/app/panels.tsx`. Cross-feature imports go through
`web/src/features/<name>/index.ts` only.

Lands in phase 5.
