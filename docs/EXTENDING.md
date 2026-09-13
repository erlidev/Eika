# Extending Eika

One section per extension point. Each one gets a complete, copy-pasteable
minimal example when the phase that introduces the extension point lands; see
`docs/PLAN.md` for phase status. Until then a section states where the code
goes and where it is registered.

Registration is always explicit, in one registry file per extension point.
Eika never registers anything from `init()`.

## Adding a tool

Implement `tool.Tool` in `internal/tool/builtin/` (one file per tool) and add it
to the list in `internal/tool/builtin/registry.go`. Tools touch a workspace only
through the `executor.Executor` in their call context; a tool that imports
`internal/workspace`, or that reaches the filesystem with `os`, is a bug.

A tool is stateless: everything one call needs arrives in the `tool.CallContext`
and the arguments. Return an error only when the harness itself failed. A bad
argument, a missing file, or a command that exited non-zero is a `tool.Result`
with `IsError` set, which the model sees and can recover from.

```go
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/tool"
)

// countLinesTool counts the lines of a file.
type countLinesTool struct{}

// Name identifies the tool to the model.
func (countLinesTool) Name() string { return "count_lines" }

// Description tells the model what the tool does.
func (countLinesTool) Description() string { return "Count the lines of a text file." }

// Schema describes the parameters of a count_lines call.
func (countLinesTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path relative to the workspace root."}
  },
  "required": ["path"],
  "additionalProperties": false
}`)
}

// Call reads the file through the executor and counts its lines.
func (countLinesTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return tool.Errorf("count_lines: %v", err), nil
	}
	data, err := c.Exec.ReadFile(ctx, args.Path, executor.ReadOpts{MaxBytes: 1 << 20})
	if err != nil {
		return tool.Errorf("count_lines %s: %v", args.Path, err), nil
	}
	return tool.Text(fmt.Sprintf("%d", strings.Count(string(data), "\n"))), nil
}
```

Register it in `internal/tool/builtin/registry.go`:

```go
func Registry() (*tool.Registry, error) {
	r, err := tool.NewRegistry(
		readTool{},
		// the other built-ins
		countLinesTool{},
	)
	if err != nil {
		return nil, fmt.Errorf("build built-in tool registry: %w", err)
	}
	return r, nil
}
```

Test it through `executor/local`, which runs against a temporary directory:
`internal/tool/builtin/builtin_test.go` holds the fixture and
`example_test.go` keeps this example compiled. A tool that produces output
while it runs reports it with `c.Output(ctx, chunk)`, which becomes a
`tool.output` event.

Put every bound a tool applies in `internal/tool/builtin/limits.go`, so that one
file answers "how much can a tool return".

## Adding a provider

Implement `provider.Provider` in `internal/provider/<name>/` and register it in
`internal/provider/registry.go`. Models are declared in configuration, not in
code: a provider reads its model name, base URL, and the *name* of the
environment variable holding its API key from `config.Model`, and resolves the
key once in its constructor.

A provider converts a `provider.Request` into a stream of `provider.Event`
values and never leaks a vendor SDK type upwards. It closes the channel after a
`KindDone` or `KindError` event and stops when the context is cancelled.
Retrying is the agent loop's job: classify a failure as a `provider.Error` and
let the loop decide.

```go
package echo

import (
	"context"
	"fmt"
	"os"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/provider"
)

// Provider answers with the last user message, for local experiments.
type Provider struct{ model config.Model }

// New returns a Provider for one configured model.
func New(m config.Model) (provider.Provider, error) {
	if os.Getenv(m.APIKeyEnv) == "" {
		return nil, fmt.Errorf("build echo provider for model %s: environment variable %s is empty", m.Name, m.APIKeyEnv)
	}
	return &Provider{model: m}, nil
}

// Stream echoes the last message back as one text delta.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	out := make(chan provider.Event)
	go func() {
		defer close(out)
		text := ""
		if n := len(req.Messages); n > 0 {
			text = req.Messages[n-1].Content
		}
		for _, e := range []provider.Event{provider.TextDelta(text), provider.Done("stop")} {
			select {
			case out <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
```

Register it in `internal/provider/registry.go`. Constructors are parameters of
`NewRegistry` because every provider package imports `provider`, so the registry
file cannot import them back:

```go
func NewRegistry(openAI, echo Constructor) *Registry {
	return &Registry{kinds: map[string]Constructor{
		"openai": openAI,
		"echo":   echo,
	}}
}
```

Test a provider against an `httptest` server that serves its wire format, as
`internal/provider/openai/openai_test.go` does. Test everything that consumes a
provider with `provider/providertest`, the scripted fake.

`config.Model` also supplies `reasoning_effort` and `preserve_thinking`.
`reasoning_effort` maps to the standard Chat Completions request field.
`preserve_thinking` is a compatible-endpoint extension, not an OpenAI API
field. It is enabled when omitted; set it to false for an endpoint that does
not accept it. When enabled, the OpenAI-compatible provider reads streamed
`reasoning_content` into `KindReasoningDelta` events. The agent stores the
assembled value in `Message.Reasoning`, and the provider sends it back as
`reasoning_content` on later assistant messages. A new provider can map the
provider-neutral reasoning value to its own wire format.

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

## Adding a migration

Write one file in `internal/store/migrations/`, named `NNNN_what_it_does.sql`
with the next free number. It is embedded into the binary and applied in file
name order, in a transaction, the next time `store.Open` runs. Migrations are
applied once and never edited afterwards: `schema_migrations` records the file
name without its extension, so changing a file that a database already carries
changes nothing. Correct a mistake with another migration.

```sql
-- internal/store/migrations/0002_session_labels.sql
ALTER TABLE sessions ADD COLUMN label text NOT NULL DEFAULT '';

CREATE INDEX sessions_label_idx ON sessions (label) WHERE label <> '';
```

Add the column to the domain struct and to the column list its queries share,
in the same change:

```go
// internal/store/sessions.go

const sessionColumns = `id, workspace_id, title, head_entry_id, parent_session_id,
	label, created_at, updated_at`
```

Then update the schema table in `docs/ARCHITECTURE.md` and cover the new
behavior in `internal/store` or `internal/session`. Those tests carry the
`docker` build tag and run against a real PostgreSQL: `storetest.Main` uses
`EIKA_TEST_DATABASE_URL` when it is set and starts a throwaway `postgres:16`
container otherwise, and `storetest.Open` hands each test a migrated database
of its own.

```
go test -tags docker ./internal/store/... ./internal/session/...
```

## Adding an API endpoint

One file per resource in `internal/server/`, one registration in
`internal/server/routes.go`, one entry in `docs/api/http.md`. Everything under
`/api` is behind the bearer token already, so a handler never checks it.

A handler reads its input, calls the packages that do the work, and writes one
of two things: a JSON body with `writeJSON`, or an error with `s.fail`. It
never writes a status code by hand except `204`.

This example exposes the session label the migration above added.

```go
// internal/server/labels.go
package server

import (
	"net/http"
	"strings"
)

// maxLabel bounds a label, because everything the API accepts is small.
const maxLabel = 64

// labelResponse is the body of GET and PUT /api/sessions/{id}/label.
type labelResponse struct {
	SessionID string `json:"session_id"`
	Label     string `json:"label"`
}

// setLabelRequest is the body of PUT /api/sessions/{id}/label.
type setLabelRequest struct {
	Label string `json:"label"`
}

// handleSessionLabel returns what the user filed a session under.
func (s *Server) handleSessionLabel(w http.ResponseWriter, r *http.Request) {
	sess, err := s.deps.Store.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err) // store.ErrNotFound becomes a 404
		return
	}
	writeJSON(w, s.log, http.StatusOK, labelResponse{SessionID: sess.ID, Label: sess.Label})
}

// handleSetSessionLabel files a session under a label.
func (s *Server) handleSetSessionLabel(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[setLabelRequest](r)
	if err != nil {
		s.fail(w, r, err) // a malformed body is already a 400
		return
	}
	label := strings.TrimSpace(req.Label)
	if len(label) > maxLabel {
		s.fail(w, r, invalidf("a label may be at most %d bytes", maxLabel))
		return
	}
	if err := s.deps.Store.SetSessionLabel(r.Context(), r.PathValue("id"), label); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, labelResponse{SessionID: r.PathValue("id"), Label: label})
}
```

Register both in `resourceRoutes`, which holds every route that reads or
writes the database:

```go
// internal/server/routes.go, in resourceRoutes
api.HandleFunc("GET /api/sessions/{id}/label", s.handleSessionLabel)
api.HandleFunc("PUT /api/sessions/{id}/label", s.handleSetSessionLabel)
```

Rules that keep the surface one surface:

- Request and response types live next to the handler, with `snake_case` JSON
  tags, and are named `<thing>Request` and `<thing>Response`.
- `decodeJSON` bounds the body and rejects unknown fields, so a client and a
  harness that disagree find out instead of losing a field silently.
- Failures go through `s.fail`, which produces the one error shape,
  `{"error":{"code","message"}}`. `invalidf`, `notFoundf`, and `conflictf` are
  for a handler's own rules; the sentinel errors of `store`, `workspace`,
  `hub`, and `builtin` map themselves in `statusOf`. Add a new sentinel there
  rather than mapping it in a handler.
- A handler that needs a workspace's files gets an executor with
  `s.executorFor`, which refuses a workspace that is not running. The server
  reaches a workspace no other way.
- New dependencies belong in `Deps`, as an interface declared in `server` when
  a test has to stand in for them.

Tests go in `internal/server/api_docker_test.go` and use the fakes in
`fakes_docker_test.go`: a real database from `storetest` and a workspace host
backed by temporary directories.

```
go test -tags docker ./internal/server/...
```

## Adding an event type

Add the type name constant to `internal/event/event.go`, the payload struct to
`internal/event/payload.go`, and the matching entries to `docs/api/events.md`
and `web/src/api/events.ts` in the same change. The envelope (`Type`, `Topic`,
`Time`, `Payload`) never changes per event type.

Payload structs live in `internal/event` rather than in the package that emits
the event, so that the server and the frontend decode events without importing
the agent loop, and so that one file lists the whole protocol. JSON tags are
`snake_case`.

```go
// internal/event/event.go

// TypeSessionCompacted reports that a session's history was summarised.
const TypeSessionCompacted = "session.compacted"
```

```go
// internal/event/payload.go

// SessionCompacted is the payload of a session.compacted event: older entries
// were replaced by a summary, and a client that holds them drops them.
type SessionCompacted struct {
	SessionID string `json:"session_id"`
	// FirstKeptEntryID is the oldest entry that survived.
	FirstKeptEntryID string `json:"first_kept_entry_id"`
	Summary          string `json:"summary"`
}
```

Emit it with `event.New(event.TypeSessionCompacted, event.SessionTopic(id), payload)`
and hand the result to an `event.Emitter`. The bus is one, so an event emitted
anywhere reaches every client subscribed to its topic. Mirror the type name in
`eventTypes` in `web/src/api/events.ts`, or `parseEvent` rejects it.

## Adding a UI panel

The right-hand pane of the workbench is a tab strip built from one array. A
panel is a component plus one entry in `web/src/app/panels.tsx`; nothing else
in the shell changes.

Write the component in the feature that owns its data. A panel receives the
open session and its workspace, both empty strings when nothing is open:

```tsx
// web/src/features/workspaces/SandboxPanel.tsx
/** What the session's sandbox is: its image, its branch, and its state. */

import { useWorkspace } from "@/features/workspaces/queries";
import { useWorkspaceEvents } from "@/features/workspaces/useWorkspaceEvents";

export type SandboxPanelProps = {
  workspaceId: string;
};

export function SandboxPanel({ workspaceId }: SandboxPanelProps) {
  useWorkspaceEvents(workspaceId);
  const workspace = useWorkspace(workspaceId);
  if (workspace.isPending) {
    return <p className="text-muted-foreground p-3 text-xs">Loading the sandbox…</p>;
  }
  if (workspace.isError) {
    return (
      <p role="alert" className="text-destructive p-3 text-xs">
        {workspace.error.message}
      </p>
    );
  }
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-3 p-3 font-mono text-xs">
      <dt className="text-muted-foreground">state</dt>
      <dd>{workspace.data.state}</dd>
      <dt className="text-muted-foreground">branch</dt>
      <dd className="truncate">{workspace.data.branch}</dd>
      <dt className="text-muted-foreground">image</dt>
      <dd className="truncate">{workspace.data.image}</dd>
    </dl>
  );
}
```

`useWorkspaceEvents` is what keeps it current: nothing in the frontend polls,
so a panel showing something the harness changes subscribes to the topic that
reports the change.

Export it from the feature's `index.ts`, because `app/` imports a feature only
through that file:

```ts
// web/src/features/workspaces/index.ts
export { SandboxPanel } from "@/features/workspaces/SandboxPanel";
```

Register it. `id` is what the layout remembers, so it never changes once it
ships. `available` hides the tab when its data cannot exist yet:

```tsx
// web/src/app/panels.tsx
import { Container } from "lucide-react";

import { SandboxPanel } from "@/features/workspaces";

const sandboxPanel: Panel = {
  id: "sandbox",
  title: "Sandbox",
  icon: Container,
  available: (context) => context.workspaceId !== "",
  Component: ({ workspaceId }) => <SandboxPanel workspaceId={workspaceId} />,
};

export const panels: readonly Panel[] = [sessionTreePanel, runPanel, sandboxPanel];
```

The tab strip, the keyboard handling, the remembered active tab, and the narrow
layout all follow from the array. Add a README line to the feature folder and
you are done.

## Adding a tool renderer

A tool call is drawn as a collapsible card: a header the registry fills with a
one-line summary, and a body the renderer owns. A tool with no renderer falls
back to formatted JSON, so writing one is an improvement, never a requirement.

Renderers live in `web/src/features/session/renderers/`. A renderer is a
`summary` function and a `Body` component over the same `ToolItem`, which
carries the call's arguments, the output it streamed, and its result:

```tsx
// web/src/features/session/renderers/renderers.tsx
import { FieldList, ResultBlock } from "@/features/session/renderers/parts";
import { detail, stringArg } from "@/features/session/renderers/registry";
import type { ToolRenderer, ToolRendererProps } from "@/features/session/renderers/registry";
import { firstLine } from "@/lib/format";

/** webSearchRenderer shows the query and the results it returned. */
const webSearchRenderer: ToolRenderer = {
  summary: (call) => firstLine(stringArg(call, "query")),
  Body: ({ call }: ToolRendererProps) => (
    <div className="space-y-2">
      <FieldList
        fields={[
          ["query", stringArg(call, "query")],
          ["source", stringArg(call, "source")],
          ["results", String(detail(call, "result_count") ?? "")],
        ]}
      />
      <ResultBlock call={call} label="search results" />
    </div>
  ),
};
```

Read arguments with the helpers in `registry.ts` (`stringArg`, `numberArg`,
`boolArg`, `args`) rather than reaching into `call.arguments`: a model can send
anything, including malformed JSON the harness quotes as a string, and the
helpers answer with an empty value instead of throwing. Read a tool's
structured result the same way, with `detail(call, "exit_code")`.

Register it under the tool's name, which is the name the Go tool reports:

```ts
export const toolRenderers: Record<string, ToolRenderer> = {
  bash: bashRenderer,
  edit: editRenderer,
  // ...
  web_search: webSearchRenderer,
};
```

Two rules. The summary is one short line: it is truncated, not wrapped. The
body must render a call that has not finished, because `tool.call` arrives
before any output does; `ResultBlock` already says "running…" for you.
