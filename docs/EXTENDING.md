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

## Using a custom sandbox image

A workspace runs in the image named by `sandbox_image`, but a `workspace.Spec`
can override it per workspace, either with an image name or with a build
context. Any image works: the harness copies its own static `eikad` binary into
the container at `/usr/local/bin/eikad` before starting it and uses that as the
entrypoint, so an image needs no Eika-specific content at all.

An image must satisfy three things:

- a shell at `/bin/sh`, because `exec` with `shell` and the terminal use it,
- a writable `/workspace`, which is where the volume or the host directory is
  mounted,
- a uid 1000 that owns `/workspace`, because a sandbox never runs as root; an
  image that uses another user must say so in `Spec.User`,
- `git`, if workspaces built from it clone from the hub.

A sandbox container drops all Linux capabilities and runs with
`no-new-privileges`, so an image that needs to install packages at run time
will not work; install them at build time.

Name an existing image:

```go
ws, err := host.Create(ctx, workspace.Spec{Image: "node:22-bookworm"})
```

Or build one from a directory holding a Dockerfile and its context:

```go
ws, err := host.Create(ctx, workspace.Spec{
    BuildContext: "/var/lib/eika/images/rust",
    Dockerfile:   "Dockerfile", // the default
})
```

The built image is tagged `eika-ws-<id>:latest`. Start the workspace with
`host.Start`, then take its executor with `host.Executor(ws)`; every tool call
goes through that.

To change the default image for every workspace, set `sandbox_image` (or
`EIKA_SANDBOX_IMAGE`) and, if you want Eika's own image as a base, extend
`sandbox/Dockerfile` and rebuild it with `make sandbox`.

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
