# Extending Eika

One section per extension point, each with a minimal example that compiles.
Registration is always explicit, in one registry file per extension point,
never from `init()`.

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
		bashTool{},
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

A tool that needs no workspace, because it only makes a network call or asks
the user something, can also be offered in a chat. Say so by implementing
`tool.Standalone`; its `CallContext.Exec` is then nil in a chat, and the tool
works without it or answers the model with what it cannot do there. This is
web_search's, in `internal/tool/builtin/web.go`:

```go
// Standalone marks web_search as a tool a chat may offer: a search is a
// network call the harness makes.
func (webSearchTool) Standalone() {}

var _ tool.Standalone = webSearchTool{}
```

A tool that says nothing needs a workspace and never reaches a chat. Add it to
the list in `TestOnlyToolsThatNeedNoWorkspaceAreStandalone`, which is what
keeps a file or command tool from being marked by mistake. The chat's Tools
panel lists it with a switch as soon as it is registered.

A tool that belongs to a service rather than to Eika needs no code here: run
it as an MCP server and add the server under Settings, MCP. Its tools reach
runs as `mcp__<server>__<tool>`, built by `internal/mcp` from what the server
lists, and obey a profile's or a session's tool choice like a built-in; a
choice can also take every tool of a server with `mcp__<server>__*`, which
`toolChosen` in `internal/server/tools.go` resolves. Do not add a
built-in with a name that starts with `mcp_`: the server treats that prefix
as the pool's (`isMCPTool` in `internal/server/tools.go`).

## Adding a provider

Implement `provider.Provider` in `internal/provider/<name>/` and register it in
`internal/provider/registry.go`. Providers and models are not declared in
code or configuration: the user adds them in the web UI, and each provider
row names its kind. The server builds a client for one run, probe, or test by
calling the kind's constructor with a `provider.Endpoint`, the row's base URL
and its key opened from the database, and drops the client afterwards.

A provider converts a `provider.Request` into a stream of `provider.Event`
values and never leaks a vendor SDK type upwards. It closes the channel after a
`KindDone` or `KindError` event and stops when the context is cancelled. The
request names the model by the endpoint's identifier. Retrying is the agent
loop's job: classify a failure as a `provider.Error` and let the loop decide.

A provider whose endpoint can list its models also implements
`provider.Lister`. The setup screens call it to offer models to tick, with
the context sizes it reports; without it, the user types model identifiers.

```go
package echo

import (
	"context"

	"github.com/erlidev/eika/internal/provider"
)

// Provider answers with the last user message, for local experiments.
type Provider struct{ endpoint provider.Endpoint }

// New returns a Provider on one endpoint.
func New(e provider.Endpoint) (provider.Provider, error) {
	return &Provider{endpoint: e}, nil
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

// Models lists the one model this provider serves.
func (p *Provider) Models(context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "echo", ContextWindow: 8192, MaxOutput: 1024}}, nil
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

`GET /api/providers` then lists the kind and `POST /api/providers` accepts it.
The UI's provider form creates OpenAI-compatible providers only, so offering
the new kind there means giving `web/src/features/providers/presets.ts` a
preset for it and sending its kind from `ProviderForm.tsx`.

Test a provider against an `httptest` server that serves its wire format, as
`internal/provider/openai/openai_test.go` does. Test everything that consumes a
provider with `provider/providertest`, the scripted fake.

What a provider maps from the request:

- `Request.Sampling`: temperature, top_p, top_k, min_p, penalties, seed,
  stop, max output, reasoning effort. Nil means the endpoint's default, so
  send a field only when set; one the wire format lacks goes in the
  endpoint's extension or is left out.
- `provider.EffortNone` turns thinking off, in the field
  `Request.ThinkingSwitch` names, or however the API disables thinking.
- Emit `KindReasoningDelta` for reasoning always; the agent streams it and
  stores it in `Message.Reasoning`. When `preserve_thinking` is on, send
  stored reasoning back on later assistant messages.
- Emit `KindUsage` as soon as the endpoint reports usage, with
  `Event.Timings` when it measures its own speed (token count and
  milliseconds per phase; see `openai/timings.go`). Leave an unmeasured phase
  zero: the agent times generation itself.

## Adding a search backend

A search backend is a web provider in the failover chain (SearXNG, Exa,
Tavily, Brave, Marginalia) or a source the model names in `web_search`'s
`source` argument (Wikipedia, arXiv, the GitHub searches). Both implement
`search.Searcher` in `internal/search/<package>/` and register in
`internal/search/registry.go`. Search runs in the harness, not in a sandbox,
because it is a network call rather than a filesystem or process action.

A searcher only speaks its backend's wire format. It returns at most
`q.Limit` results, reduced to `search.Result`, with descriptions passed
through `search.Clean`. The Engine does everything else: the key, quotas,
cooldowns, pacing, caching, deduplication, and what the model reads. Build
requests with `search.Get` or `search.Post` and send them with `search.JSON`
(or `search.Do` for a body that is not JSON): they identify Eika, apply the
timeout, bound the body, and turn a failed status into a `*search.HTTPError`
that carries the header, which is how a `Retry-After` becomes a cooldown.
Refuse a query you can tell will fail with `search.Errorf`, before spending a
request.

A keyed web provider, whose key the user enters in the Search tab:

```go
package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/erlidev/eika/internal/search"
)

// Kagi searches Kagi's search API. It needs a key.
type Kagi struct{ client *http.Client }

// NewKagi returns a Kagi provider.
func NewKagi(client *http.Client) *Kagi { return &Kagi{client: client} }

// Search runs the query on Kagi's search API.
func (k *Kagi) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	params := url.Values{"q": {q.Text}, "limit": {strconv.Itoa(q.Limit)}}
	req, err := search.Get(ctx, "https://kagi.com/api/v0/search?"+params.Encode())
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+q.Key)
	var body struct {
		Data []struct {
			T       int    `json:"t"`
			URL     string `json:"url"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
		} `json:"data"`
	}
	if err := search.JSON(ctx, k.client, req, &body); err != nil {
		return nil, err
	}
	var out []search.Result
	for _, d := range body.Data {
		if d.T == 0 { // 0 is a search result; other types are related searches
			out = append(out, search.Result{Title: d.Title, URL: d.URL, Description: search.Clean(d.Snippet)})
		}
	}
	return out, nil
}
```

Register it with one entry in `Registry`, and add its field to
`search.Searchers`, which the wiring fills in `internal/server/wire.go`
(`Kagi: web.NewKagi(client)`), because this package cannot import the
backends that import it:

```go
{Name: "kagi", Searcher: s.Kagi, Web: true, Key: "kagi", KeyRequired: true, Limit: Limit{Month: 100}},
```

`Name` is what the settings and the API store, so it never changes once
shipped. `Web` puts it in the chain; its position in `Registry` is its place
in the default order, and the user reorders from there. `Key` names the key
it is sent in `q.Key`; `PUT /api/search/keys/kagi` accepts it from then on,
and the Search tab lists it (give it a label in
`web/src/features/search/search.ts`). `Limit` is its default quota, which
`search_limits` overrides. A source leaves `Web` false and names itself in
`web_search`'s `source` enum in `internal/tool/builtin/web.go`; `Bucket`
makes it count against a quota, `Pool` caches a larger pool so a repeated
query is free, and `Interval` spaces its requests for a backend that asks
for it, as arXiv does.

Test a backend with `searchtest.Client`, which answers requests in-process
and records them; `internal/search/web/example_test.go` holds this example
and its test. `searchtest.New` is a scripted searcher for testing whatever
consumes one.

## Adding a utility task

A utility task is a small job the harness gives a model for itself, outside
any run: naming a session is one. The user assigns each task a model in
Settings, General (the `utility_models` setting); a task with no model does
not run. A task is a `utility.Task` in `internal/utility/utility.go`, listed
in `Known`, and a function in its own file of that package that sends one
request with `Model.request`, which turns thinking off the way a run does:

```go
package utility

import (
	"context"
	"strings"

	"github.com/erlidev/eika/internal/provider"
)

// SummaryPrompt is the system prompt of the run summary task.
const SummaryPrompt = "Summarise what the assistant did in one sentence. Respond with only the summary."

// Summary asks m for a one-sentence summary of a run's last answer.
func Summary(ctx context.Context, m Model, answer string) (string, error) {
	reply, _, err := provider.Complete(ctx, m.Provider, m.request(SummaryPrompt, truncate(answer, 8000), 256))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(reply), nil
}
```

The server calls it where its trigger happens: `s.utilityModel(ctx, task)`
reads the assigned model (false for none), and `s.utilityClient` builds it.
A task that must not hold up a request runs in a goroutine with an owner that
`Server.Close` stops, as `titles` in `internal/server/titles.go` does. Test
the function with `providertest`, as `internal/utility/title_test.go` does,
and the trigger with a `docker`-tagged server test. Give the task a label in
`utilityTasks` in `web/src/features/settings/UtilityModels.tsx`, add it to
the `utility_models` row in `docs/api/http.md`, and mirror it in the mock
harness.

## Using a custom sandbox image

A workspace runs the image the sandbox image setting names (Settings,
General; `eika-sandbox:latest` by default), but a `workspace.Spec` can
override it per workspace, either with an image name or with a build
context. Any image works: the harness copies its own static `eikad` binary into
the container at `/usr/local/bin/eikad` before starting it and uses that as the
entrypoint, so an image needs no Eika-specific content at all.

An image needs:

- a shell at `/bin/sh`, because `exec` with `shell` and the terminal use it,
- a writable `/workspace`, which is where the volume or the host directory is
  mounted,
- a uid 1000 that owns `/workspace`, because a sandbox never runs as root; an
  image that uses another user must say so in `Spec.User`,
- `git`, if workspaces built from it clone from the hub.

A sandbox container drops all Linux capabilities and runs with
`no-new-privileges`, so an image that needs to install packages at run time
will not work; install them at build time. When the workspace's egress is
restricted, its tools reach the internet only through the harness's proxy,
which they find in `HTTP_PROXY` and `HTTPS_PROXY`; a tool in the image that
ignores those variables reaches nothing, and pre-installing what it would
download is the way around that.

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

To change the default image for every workspace, set it under Settings,
General. To use Eika's own image as a base, extend `sandbox/Dockerfile` and
rebuild it with `docker compose build sandbox-image` or `make sandbox`.

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
`/api` is behind authentication already, a sign-in session or the API token,
so a handler never checks it.

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

Add both routes to `routeWire` in `internal/server/contract_test.go` with
their status and wire types, then run `make contract` to write them into
`docs/api/contract.json`. `TestContractCoversEveryRoute` fails until a route
routes.go registers is in the table, and every handler test checks the
responses it gets against the contract, so a wrong type there fails too:

```go
// internal/server/contract_test.go, in routeWire
"GET /api/sessions/{id}/label": {status: http.StatusOK, response: labelResponse{}},
"PUT /api/sessions/{id}/label": {status: http.StatusOK, request: setLabelRequest{}, response: labelResponse{}},
```

A type with a `MarshalJSON` of its own needs a stand-in in `standIns` there,
since reflection cannot see what it writes.

Tests go in `internal/server/api_docker_test.go` and use the fakes in
`fakes_docker_test.go`: a real database from `storetest` and a workspace host
backed by temporary directories. A method added to `Workspaces` is added to
`fakeHost` too; its `Process` starts what the test sets in `process`, which is
how `mcp_docker_test.go` runs a scripted stdio MCP server
(`internal/mcp/mcptest`) in a workspace.

```
go test -tags docker ./internal/server/...
```

If the UI calls the route, serve it in the mock harness,
`web/e2e/harness/mock.ts`, and add a request for it to
`web/e2e/harness/mock.test.ts`, which checks the mock against the contract
route by route. A route the UI never calls goes in `unmocked` there instead.

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

Add the type to `eventPayloads` in `internal/server/contract_test.go` and run
`make contract`. `TestContractCoversEveryEvent` fails until every constant is
listed there, and the mock harness then holds any event it plays of that type
to the payload's shape.

## Adding a UI panel

The right-hand pane of the workbench is a tab strip built from one array. A
panel is a component plus one entry in `web/src/app/panels.tsx`; nothing else
in the shell changes.

Write the component in the feature that owns its data. A panel receives the
open session and its workspace, both empty strings when nothing is open, and
whether the session is a chat, which has no workspace:

```tsx
// web/src/features/workspaces/InfoPanel.tsx
/** What the session's workspace is: its image, its branch, and its state. */

import { useWorkspace } from "@/features/workspaces/queries";
import { useWorkspaceEvents } from "@/features/workspaces/useWorkspaceEvents";

export type InfoPanelProps = {
  workspaceId: string;
};

export function InfoPanel({ workspaceId }: InfoPanelProps) {
  useWorkspaceEvents(workspaceId);
  const workspace = useWorkspace(workspaceId);
  if (workspace.isPending) {
    return <p className="text-muted-foreground p-3 text-xs">Loading the workspace…</p>;
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
export { InfoPanel } from "@/features/workspaces/InfoPanel";
```

Register it. `id` is what the layout remembers, so it never changes once it
ships. `available` hides the tab when its data cannot exist yet:

```tsx
// web/src/app/panels.tsx
import { Info } from "lucide-react";

import { InfoPanel } from "@/features/workspaces";

const infoPanel: Panel = {
  id: "info",
  title: "Info",
  icon: Info,
  available: (context) => context.workspaceId !== "",
  Component: ({ workspaceId }) => <InfoPanel workspaceId={workspaceId} />,
};

export const panels: readonly Panel[] = [sessionTreePanel, runPanel, infoPanel];
```

The tab strip, the keyboard handling, the remembered active tab, and the narrow
layout all follow from the array. Add a README line to the feature folder and
you are done.

The pane body scrolls, which suits a list. A panel that sizes and scrolls its
own content, such as a terminal or an editor, sets `fill: true` instead: its
component then gets the whole body at a fixed height (lay it out with `h-full`
and `min-h-0`), and the pane may be dragged wider than a scrolling panel
allows. The Files and Terminal panels are the examples:

```tsx
const terminalPanel: Panel = {
  id: "terminal",
  title: "Terminal",
  icon: SquareTerminal,
  available: (context) => context.workspaceId !== "",
  fill: true,
  Component: ({ workspaceId }) => <TerminalPanel workspaceId={workspaceId} />,
};
```

A panel that works inside the sandbox wraps its content in `RunningWorkspace`
from `features/workspaces`, which shows the workspace's state and a Start
button until it runs. A panel whose component pulls in a large library loads
that part with `React.lazy`, as the Files panel does for Monaco and the
Terminal panel for xterm, so the main bundle stays small.

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
import { stringArg } from "@/features/session/renderers/registry";
import type { ToolRenderer, ToolRendererProps } from "@/features/session/renderers/registry";
import { firstLine } from "@/lib/format";

/** countLinesRenderer shows the file the example tool counted, and its count. */
const countLinesRenderer: ToolRenderer = {
  summary: (call) => firstLine(stringArg(call, "path")),
  Body: ({ call }: ToolRendererProps) => (
    <div className="space-y-2">
      <FieldList fields={[["path", stringArg(call, "path")]]} />
      <ResultBlock call={call} label="line count" />
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
  // ...
  count_lines: countLinesRenderer,
};
```

A body with more than a few lines of markup belongs in a file of its own that
exports only the component, as `WebRenderer.tsx` does for `web_search` and
`web_fetch`; `renderers.tsx` keeps the summary and names the body.

Two rules. The summary is one short line: it is truncated, not wrapped. The
body must render a call that has not finished, because `tool.call` arrives
before any output does; `ResultBlock` already says "running…" for you.
