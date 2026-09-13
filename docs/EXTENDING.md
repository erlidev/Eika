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

Write the handler in `internal/server/` and register the route in
`internal/server/routes.go`. Every route except `/healthz` requires the bearer
token. Document the request and response in `docs/api/`.

Routing exists now; auth and the rest of the API land in phase 4.

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
// internal/event/payload.go

// QuestionAsked is the payload of a question.asked event: the run is waiting
// for the user to answer.
type QuestionAsked struct {
	RunID    string   `json:"run_id"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}
```

Emit it with `event.New(event.TypeQuestionAsked, event.SessionTopic(id), payload)`
and hand the result to an `event.Emitter`.

## Adding a UI panel

Write the component under `web/src/features/<feature>/` and register it in
`web/src/app/panels.tsx`. Cross-feature imports go through
`web/src/features/<name>/index.ts` only.

Lands in phase 5.
