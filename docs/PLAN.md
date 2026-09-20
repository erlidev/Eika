# Eika Implementation Plan

Eika is a Docker-native agentic coding harness. It ports the core of the
[Pi coding agent](https://github.com/badlogic/pi-mono) (agent loop, tools,
session trees, context files) to Go and adds sandboxed workspaces, parallel
subagents, built-in web search, and a web UI. It is built so that agents can
modify Eika itself.

This document is the source of truth for scope and architecture. Update it
when decisions change. Phase status is tracked in the checklist at the end.

## 1. Decisions

| Area | Decision |
|---|---|
| Backend | Go 1.26, stdlib `net/http` router, `log/slog`, official `openai-go` SDK |
| Frontend | Vite, React 19, TypeScript (strict), Tailwind v4, shadcn/ui |
| Visual testing | Playwright against a mock harness (`web/e2e/harness`), not the Go server: screens need no Docker or Postgres and every state is scriptable |
| Providers | OpenAI-compatible only at launch, behind a `provider.Provider` interface |
| Docker | Harness mounts `/var/run/docker.sock`; sandboxes are sibling containers |
| Persistence | PostgreSQL (`pgx`), embedded SQL migrations; no ORM |
| Extensibility | Source-level modularity plus rebuild; no runtime plugin loader |
| Sandbox image | One default `eika-sandbox` image; per-workspace override by image or Dockerfile |
| Search | A port of the localsearch Pi extension. A web search asks one provider, the first usable one in a failover chain: a self-hosted SearXNG container, then Exa, Tavily, and Brave with a key, then Marginalia. Wikipedia, arXiv, and GitHub are sources the model names directly |
| Auth | Single user. A password chosen in the guided setup signs a browser in and returns a session token; an optional `EIKA_AUTH_TOKEN` is a fixed API token for scripts |
| Reasoning is streamed, not just stored | A provider emits reasoning deltas whatever `preserve_thinking` says, and the agent republishes them as `reasoning.delta`. Showing the model think is the client's business; replaying the reasoning to the endpoint is `preserve_thinking`'s, and tying the two would mean a user who wants to watch a model reason has to send its reasoning back to an API that rejects it. The session view shows each block collapsed to one line by default, expanded when the reader asks; the choice is a browser preference, not a harness setting, because two people reading one session can want different things |
| Reasoning efforts belong to the model row | Compatible endpoints disagree on the vocabulary — OpenAI takes minimal through high, others take none, xhigh, max, or a word of their own — so `models.reasoning_efforts` holds the words a model offers and `provider.ValidReasoningEffort` checks only the shape (at most 32 letters, digits, hyphens, underscores). The session's status bar cycles through the list, which writes the model's `reasoning_effort` and so applies wherever that model next runs |
| Context and decode rate are measured, never estimated | `turn.progress` carries the usage an endpoint reported, the configured context window of the model that reported it, and the time since a response's first token; `turn.end` repeats them for the whole turn. A client needs two comparable reports from one attempt and divides their differences. One report is not a rate, and a retry starts a new baseline. Counting characters on screen, or timing deltas, would give a number that looks precise and is not, so an endpoint that never reports usage shows no rate at all. `context` is the last model call's own usage, not the turn's sum: one conversation fills the window, not every prompt in the turn added together. The final context measurement is stored with each assistant entry so replay restores the view after a reload |
| Compaction | Deferred; the session model must support it later |
| Context-window enforcement | Before each model call, the agent compares a conservative JSON byte/token upper bound, including requested output, with the model's configured window. It fails before the provider call when the request cannot fit; compaction remains deferred |
| Skills / templates | Deferred; AGENTS.md is in scope |
| Config format | Deployment configuration (addresses, paths, Docker topology) is YAML via `gopkg.in/yaml.v3` plus `EIKA_*` env vars, and its defaults are the compose stack's, so a deployment sets nothing. Everything a user chooses lives in the database and is edited in the web UI |
| Go tooling | `staticcheck` and `goimports` pinned by `tool` directives in `go.mod`; `golangci-lint` optional |
| `EXTENDING.md` examples | One section per extension point exists from phase 0; the copy-pasteable example lands with the phase that creates the interface |
| `internal/event` in phase 0 | The envelope and type names are the contract the later phases and the frontend agree on, so they are fixed before anything emits events |
| `internal/server` in phase 0 | `main` must stay thin, and both binaries need one listener lifecycle; phase 4 extends `routes.go` rather than creating the package |
| Harness process user | Non-root `eika`. The image's entry point starts as root only to add `eika` to the group that owns the mounted Docker socket, whatever its id on this host, then drops to `eika` with `setpriv`. Socket access is root-equivalent and accepted: sandboxes are sibling containers |
| OpenAI SDK version | `github.com/openai/openai-go/v3`, pinned at v3.61.0, the latest stable major |
| Chat Completions, not Responses | Providers are "OpenAI-compatible" endpoints. Every such endpoint implements Chat Completions; few implement the Responses API. The `Provider` interface hides the choice, so a Responses implementation can be added later as another kind |
| Chat Completions reasoning | `reasoning_effort` uses the standard Chat Completions request field. Streamed `reasoning_content` is always captured and stored as provider-neutral reasoning data. `preserve_thinking` is a per-model switch for compatible endpoints and is off for a new model, because the official OpenAI API rejects it: when on, Eika sends the extension and replays the stored reasoning on later assistant messages. The extension is not part of the OpenAI Chat Completions contract |
| Malformed tool arguments | Keep the model's exact argument text. Valid arguments keep their JSON shape on the API and in storage. Malformed text is safely quoted and carries `arguments_malformed: true`, then is restored before tool decoding. The explicit marker distinguishes malformed text from a valid top-level JSON string. The tool can report a normal argument error and the session remains resumable |
| Retries live in the agent loop | The SDK's retries are switched off (`WithMaxRetries(0)`). One place decides, so the scripted fake provider exercises the same retry path as the real one. Retryable means 408, 409, 429, 5xx, or a transport failure |
| Provider API keys | Entered in the UI, sealed with AES-256-GCM by `internal/secret` before they reach a row, and opened by the server only to build a provider for one run, probe, or test. No package reads a key from the environment; the OpenAI provider sets its key and base URL explicitly so the SDK's `OPENAI_*` defaults never apply. The API returns whether a key is stored and the last four characters of a long one, never the key. A key belongs to the base URL it was entered for: changing a provider's URL without entering a key clears the stored key, and a probe at another URL never carries it, so a typo or a hostile URL cannot collect it |
| Event payload structs | Live in `internal/event`, not in the package that emits them, so that the server and the frontend decode events without importing the agent loop, and one file lists the whole protocol |
| Built-in tool registry | `internal/tool/builtin/registry.go`, not `internal/tool/registry.go`: the tools import `tool` for the interface, so the registry of them cannot live in `tool` without an import cycle. `internal/tool/registry.go` holds the `Registry` type |
| Provider kind registry | `provider.NewRegistry` takes each kind's constructor as a parameter, for the same reason: a provider package imports `provider` |
| Tool registry lifetime | One registry is shared by every session; the executor arrives per call in `tool.CallContext`, so tools stay stateless |
| Context file locations | Discovery is workspace-relative and reads through the executor. The "global" file is a workspace path, `.config/eika/AGENTS.md` by default, because the harness filesystem is not reachable from a sandbox |
| `eikad` injection | The harness copies its static `eikad` into each created container (`CopyToContainer`) before starting it, rather than bind-mounting it or using a shared volume. A bind mount source is resolved by the Docker daemon on the *host*, and the harness's filesystem is inside a container; a shared volume would need a helper container to populate it. The copy needs neither and works for any image |
| `eikad` listener | Port 7000, flag-configurable. `cmd/eikad` reuses `server.Serve`, so both binaries share one listener lifecycle |
| `eikad` dependencies | `github.com/coder/websocket` for `/pty` and `/watch` (context-aware, no global state, the maintained successor to nhooyr.io/websocket) and `github.com/creack/pty` for the terminal (the standard Unix pty wrapper; the standard library has none). The watcher polls with the standard library instead of adding fsnotify |
| Docker client | `github.com/docker/docker/client`, the official Engine API client. The alternative is hand-rolling the socket protocol, which the style guide's "boring technology" rule rejects |
| Sandbox network | Sandboxes join their own network, pinned to `eika_sandbox`, which only the harness also joins, so a sandbox cannot reach postgres or searxng. They are addressed by container name. An empty `sandbox_network` publishes each daemon port on `127.0.0.1`, which is how `make dev` and the Docker tests reach a sandbox from outside compose |
| Sandbox confinement | Docker's init is PID 1, all capabilities are dropped, `no-new-privileges` is set, and the container runs as uid 1000 by default. A custom image must have that uid or set `Spec.User` |
| Hub access scope | A hub grant covers one project: `Grant(workspaceID, project, token)`, checked against the requested project on every request. A workspace's token is useless on any other project, and a stopped workspace holds no grant |
| Hub package | `internal/workspace/hub`, not `internal/hub`: the hub exists to serve workspaces and `workspace` is its only importer |
| Hub credentials | A private remote's username and password or token are entered with the project and sealed like API keys. The API rejects URL userinfo, query strings, and fragments. The hub receives the values only for a git command and supplies them through a credential helper and the process environment, so a token is not written to disk or put in an argument. New credentials are tried with a fetch before they are stored. Migration 0003 drops the earlier environment-variable references, so a private remote from before it gets its credentials entered again. Inside a workspace the same shape reads `EIKA_HUB_USER` and `EIKA_HUB_TOKEN`, so the hub token never appears in a remote URL or in `.git/config` |
| pgx version | `github.com/jackc/pgx/v5`, pinned at v5.11.0, the latest stable major. The pool (`pgxpool`) and the native protocol come with it, so nothing else is needed; adding it upgrades the module graph's `golang.org/x/{mod,sync,telemetry,tools}` entries, which are indirect tool dependencies |
| Migrations | A 40-line migrator in `internal/store/migrate.go` over an embedded `migrations/NNNN_name.sql` directory, applied in file name order under a PostgreSQL advisory lock, one transaction per file, recorded in `schema_migrations`. golang-migrate would be a dependency for less |
| Id format | One generator, `store.NewID`: 20 lowercase base32 characters, 96 bits of randomness. Ids are text in every table, generated before the row exists, so the same id names a row, a container, a volume, and a URL |
| Fork semantics | A fork copies the path from the root to the fork entry into a new session and shares no rows with its parent. Shared ancestry would make either session's deletion or edit reach into the other, and the copy is small: a path is a few dozen rows |
| Entry representation | One `provider.Message` is one entry; an assistant message that carries tool calls stays one assistant entry whose payload is that message's JSON. It round-trips exactly and adds no branch point the agent loop cannot resume from, because a model that asked for three calls needs all three answered |
| Entry kinds | The kind vocabulary (`user`, `assistant`, `tool_call`, `tool_result`, `system`, `event`) is fixed now, like the event names in phase 0, so later phases and the frontend agree. Message conversion writes `user`, `assistant`, and `tool_result`; questions, subagent lifecycle, and compaction write `event` |
| Composition lives in `server` | `server.Run` opens the store, builds the hub, the host, the model set, and the tool registry, and serves. A new `internal/app` package would hold one function and force `server` to export its `Deps` wiring anyway; `cmd/eika` stays three flags and a call |
| Server dependencies | `Workspaces`, `Hub`, and `Models` are interfaces declared in `server`, where they are consumed, so the handler tests run the real API against a host backed by temporary directories. `*store.Store` stays concrete: `session.Tree` takes it, so an interface in `server` alone would buy nothing |
| Server tests need PostgreSQL | Every handler but the health checks and the event stream reads or writes rows, so the handler, run manager, and replay tests carry the `docker` build tag and use `store/storetest`. Auth, error mapping, bus fan-out, and WebSocket subscription are untagged, because they touch no row |
| Event fan-out | One in-process `event.Bus` implementing `event.Emitter`, with a buffered channel per subscriber and drop-slowest. A run must never block on a browser; the client is told what it lost with a `bus.dropped` event and re-requests it with `session.replay` |
| Replay writes to the socket | A replay is written straight to the WebSocket rather than through the subscription, so a session with more entries than the subscriber buffer holds is not silently truncated by the drop policy meant for live events |
| Event stream authentication | `/api/events` is the one route that also accepts the bearer token as a query parameter, because a browser cannot set a header on a WebSocket handshake. Every other route rejects a query token, so a token never has to appear in an ordinary URL |
| One run per session | The run manager keys active runs by session and answers a second `run` message with 409. A second concurrent run would append to the same head and interleave two conversations in one branch; steering and follow-up queues are how a user adds to a run in progress |
| Event stream origin check | The handshake is accepted same-origin or from a host pattern in `allowed_origins`; `Load` always prepends the origin derived from `listen`. A bearer token that a browser holds is reachable from any page the browser also runs, so the token alone is not the whole check |
| Claiming a session for a run | `runs.reserve` puts a placeholder in `bySession` under the same lock the conflict check reads, before the database read and the workspace command that building a run needs. Checking first and registering afterwards let two concurrent requests both start a run on one session |
| `ask_user` question ids | Questions are brokered by `builtin.Questions` and identified by their own `q-<hex>` ids rather than `store.NewID`: a question is never a row, and `tool` importing `store` would point a dependency the wrong way |
| Hub mirrors local projects | Yes. A local project keeps its bind mount for the user's own workspace, and its workspaces still push to a hub repository, so fork-with-workspace and subagents work the same way for both kinds. This resolves the phase 3 open question; nothing in the schema depends on it, and phase 6 implements the push |
| Subagent branch names | `<parent-branch>-<name>-<6 chars of the subagent id>`, joined with dashes, not `<parent-branch>/<name>`. The parent's branch is in the hub by the time the child is cloned, and git stores either `refs/heads/main` or `refs/heads/main/fix`, never both, so the path form fails for every child of a branch that exists. The id tag makes the branch the child's own, so two children a parent gave the same name cannot overwrite each other and `Host.Push` never has to force. Fork-with-workspace uses the same shape, `<branch>-fork-<8 chars of the new id>` |
| Subagent names | Validated against `^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$` before they reach git. The name becomes a branch, a session title, and an event field, so it holds nothing that needs escaping anywhere; `git check-ref-format` still checks the branch that is built from it |
| A child run is an ordinary run | The spawner drives a child through `subagent.Runner`, which the server's run manager implements with `runs.runChild`. A child gets the same tools, events, entries, queues, and retry behavior as any other run, bound to the context of the parent run. A second loop for children would be a second way to do the same thing |
| Spawner and server are built in one cycle | The tool registry every run shares holds `spawn_agent`, so the spawner exists before the server; the spawner drives children through the run manager, which the server owns. `subagent.New` takes no runner and `Spawner.Attach` supplies it once, from `Server.UseSubagents`, before anything serves |
| Subagent tools reach the spawner through an interface | `builtin.Subagents`, declared in `internal/tool/builtin` where it is consumed, the same shape `ask_user` uses for its question broker. `internal/subagent` implements it and imports `builtin` for the request and result types, so nothing under `tool` learns what a workspace is |
| A finished child's workspace is stopped, not destroyed | The branch is in the hub, but the container is where the user looks at what the child actually did, and starting it again is a click. This holds for a child that failed or was aborted as well; only a workspace whose creation did not finish is destroyed |
| An aborted child still reports | Committing, pushing, and recording the result run on a context of their own with a five-minute bound, not the child's cancelled one, so work that was interrupted still lands on a branch the user can read |
| Hub remote name | A workspace reaches the hub on a remote called `eika-hub`, not `origin`: a local project's bind-mounted checkout already has an origin of the user's own, and the hub must not displace it |
| A merge conflict is a normal response | `POST /api/workspaces/{id}/merge` answers `200` with `merged: false` and the conflicted paths. The target's tree is left conflicted on purpose, because resolving it is work for the user or the agent in that workspace, not for the harness |
| Subagent limits are settings | `subagent_max_depth` (default 2, at most 8) and `subagent_max_children` (default 4, at most 16), read through `subagent.Options.Limits` at every spawn so a change applies to the next child. Depth is measured by walking `subagents` rows up from the spawning session, width by counting that session's running children. Both are validated as at least one, so the user cannot disable subagents by setting zero and getting a silent default |
| A spawn claims its slot before it builds anything | `Spawner.reserve` puts the child in the live map under the same lock that counts the parent's children, before the commit, push, container, and clone that building one takes. Counting first and recording afterwards let every concurrent `spawn_agent` call past a limit none of them had reached yet |
| A child runs its parent's image | `spawn_agent` has no `image` parameter. Nothing validates an image name a model invents, and a child that needs different tooling is a workspace the user creates, not one the model names |
| The hub is never force-pushed | `Host.Push` has no force flag. A branch in the hub belongs to whoever created it, and a push that cannot fast-forward is a divergence for the caller to resolve. Unique child branches are what make this possible |
| Shutdown stops the children too | `Server.Close` aborts the runs and then calls `Spawner.Shutdown`, which aborts every child and waits for it to record how it ended. A child spawned with `wait: false` outlives its parent's run, so aborting runs alone would leave it writing to a pool that is closing |
| Frontend shell | A three-pane workbench: project/workspace/session tree, the open session, and a tab strip of context panels. One session at a time, addressed by URL (`/sessions/<id>`), so a reload and a bookmark both work |
| Panel registry | `web/src/app/panels.tsx` holds one array; the right pane's tab strip is that array filtered by what is open. A phase that adds a panel writes one component and one entry. `id` is what the remembered active tab stores, so it never changes once shipped |
| Tool renderer registry | `web/src/features/session/renderers/renderers.tsx` maps a tool name to a `summary` line and a `Body` component. An unregistered tool falls back to formatted JSON, so a new tool renders usefully before anyone writes a renderer |
| Transcript is a pure reducer | `features/session/transcript.ts` folds the event stream into rendered rows and holds no React or network. The Zustand store is a shell around it, so a scripted event sequence from `docs/api/events.md` is the whole test |
| Live turn against stored entries | Run events are keyed by `run_id`, replayed `session.message` events by `entry_id`. A turn that ends is sealed and its live rows are dropped as soon as the replay delivers the entries, and an entry already held is a no-op. That is how the same content arriving twice draws once |
| No polling | Server state is TanStack Query and is invalidated by events: `workspace.state` for a container's lifecycle, `turn.end` and `run.error` for the run status and the outline. `refetchOnWindowFocus` is off |
| One socket, reference-counted topics | `api/stream.ts` multiplexes one WebSocket for the page. A reconnect backs off and re-sends the union of live topics; the first connection carries them in the handshake URL so a client that cannot send a frame still receives events. `bus.dropped` reaches every handler because it reports on the connection, not a topic |
| Token in localStorage, 401 returns to connect | The bearer token and the harness URL live in `api/connection.ts`, a plain module with listeners rather than a store: `api/` may not depend on a feature, and the HTTP client needs the token. Any `401` forgets it, which both logs out and reports a token the deployment rotated |
| Resizer without a dependency | `components/ResizableSplit` is a pointer-events handler over a `role="separator"` element with arrow-key support, in about eighty lines. A split-pane library would have to be configured into the same behaviour |
| Frontend dependencies | `react-router` for URLs, `zustand` for stream state, `react-markdown` with `remark-gfm` and `rehype-highlight` for assistant prose, `cmdk` (through shadcn's command component) for the palette. All pinned exactly, as the existing ones are |
| Configuration lives in the UI | Model providers, models, the default model, the sandbox image, the subagent limits, git credentials, and the sign-in password are rows the web UI edits, not files. A fresh stack needs no `.env`: `docker compose up` and a browser is the whole setup |
| Providers and models are rows | `providers` holds an endpoint (kind, base URL, sealed key); `models` holds what a run needs (the endpoint's identifier, context window, max output, reasoning settings). A model's `name` is unique across providers because a run, a setting, and a `spawn_agent` call name a model alone; the UI suggests `<provider>/<id>` when an id is taken. Renaming the default model keeps it the default; deleting it falls back to the first model |
| Providers are built per use | `server.Providers` (the kind registry) turns a provider row into a client for one run, probe, or test. Nothing caches a client, so a changed key or endpoint applies to the next run and a key is in memory only while it is used |
| Model discovery | `provider.Lister` is an optional interface a provider implements when its endpoint can list models. The OpenAI provider reads the context sizes compatible endpoints add to `/models` (OpenRouter, vLLM, Groq). Limits the endpoint does not report come from a family table in the UI and are always editable |
| Sealing key | 32 random bytes, hex in `secret_key_file` (default `/var/lib/eika/secret.key`, in the hub volume), created on first start with `O_EXCL`. It is apart from the database so a database copy alone opens nothing. Losing it makes stored keys unreadable, which the API reports as "enter the key again" rather than an internal error |
| Sign-in | PBKDF2-HMAC-SHA256 at 600,000 iterations (the standard library's `crypto/pbkdf2`), one row. Setup is claimable once, while no password exists; the stack publishes on 127.0.0.1 only, so only this machine can claim it. A session token is 32 random bytes stored as its SHA-256, valid 30 days; changing the password ends every session. Attempts are checked one at a time rather than locked out, which bounds guessing without locking the owner out |
| Guided setup | The UI walks password, provider, models, sandbox check, and first project, resuming at the first thing missing, until the `setup_complete` setting is true. Every step after the password can be skipped |
| Compose at the repository root | `compose.yaml` is at the root so `docker compose up` works from a clone; `compose.dev.yaml` publishes postgres and searxng on loopback for `make dev`. A `sandbox-image` service builds `eika-sandbox:latest` and exits, and the harness waits for it, so the image exists before the first workspace |
| Search is one provider, not a fan-out | A web search goes to the first provider in `search_order` that has its key, quota to spare, and no cooldown, and stops at the first that answers with anything. Every extra provider would cost quota and buy little, because the pool of 30 results asked for is already larger than the 10 the model sees. A provider that fails, including one that answers with no results, is skipped and the attempt is reported; when none answers, the error lists every attempt and how to recover |
| Search cooldowns and quotas | A failing provider cools down for 15 minutes, doubling per consecutive failure to a 6-hour cap; a `Retry-After` or `X-RateLimit-Reset` deadline wins when later, capped at a day; a success clears it. Quotas count per UTC day and month per bucket (Exa 900 a month, Tavily 1000, Brave 2000, Marginalia 100 a day by default; `search_limits` overrides them). The GitHub sources and web_fetch's GitHub reads share one `github` bucket. Counters live in memory and write through to `search_usage`, so a monthly quota survives a restart; a failed write is logged, never fatal |
| Search settings | `search_order` and `search_limits` are settings the UI edits, validated against the registered providers and buckets. The rest of localsearch's configuration (result count, pool size, token budgets, cache lifetimes, timeouts) are constants: they are budgets and courtesies, not preferences. The SearXNG URL stays deployment configuration (`searxng_url`), since it names a container |
| Search keys | Exa, Tavily, Brave, and a GitHub token are entered in the Search tab and sealed into `search_keys` like provider keys. The API reports whether each is set and the last four characters of a long one. Code search needs the GitHub token; the other GitHub sources work without one at the anonymous rate |
| Search and fetch caches | In memory: a search's pool for 24 hours and a fetched page for 6, bounded LRU caches. localsearch's on-disk cache and its Markdown sidecar file (a path the model could grep) are not ported: the harness disk is not reachable from a sandbox, so a path there is useless to the model |
| Tool names | `web_search` and `web_fetch`, not localsearch's `search` and `fetch`, so the names do not read as grep and find, and match this plan |
| web_fetch reads | A URL is planned before any request: GitHub repository, blob, tree, issue, pull request, release, and gist URLs read through the API or the raw host; source and prose files are fetched verbatim; everything else is HTML. HTML is reduced to the container a documentation generator marks (Docusaurus, MkDocs, Sphinx, ReadTheDocs, VitePress, Mintlify, Furo, rustdoc, GitHub markdown, then `article`, `main`), else a readability score, else the body. Over 10,000 tokens a plain fetch returns the page outline; a section or a filter returns its own content cut on a section boundary |
| HTML parsing | `golang.org/x/net/html` (with `html/charset` for declared and meta-tag encodings) and a converter of our own. The standard library has no HTML parser; a Markdown conversion library would still need the container, sanitising, and table rules localsearch applies |
| fetch filters run in the sandbox | A web_fetch `filter` is JavaScript the model writes, so the harness never runs it: the tool runs `eikad filter` through the workspace's executor, with the page on stdin and the outcome on stdout, and the budget applied inside the sandbox so the answer is small. eikad embeds `github.com/dop251/goja`, a pure-Go ECMAScript interpreter, because the filter's `grep` takes the model's regular expressions, which only a JavaScript engine reads as written. goja is imported by eikad alone; the harness binary does not link it |
| fetch address guard | The fetch client refuses to connect to a non-public address at dial time, after DNS, so a public name that resolves into a private range is refused too; redirects are re-checked on each connection and limited to five. localsearch's `allowPrivateHosts` is not ported: the harness shares a network with postgres and every sandbox |
| Local model servers | The harness container maps `host.docker.internal` to the host gateway, so Ollama or LM Studio on the Docker host is reachable on Linux as on Docker Desktop. A connection failure to a loopback base URL says so |
| Destructive actions confirm in a dialog | `components/ConfirmDialog` wraps shadcn's alert dialog. The browser's `confirm` cannot be styled, cannot say what survives a deletion, and cannot be reached by a test |
| Terminal is not an executor call | The harness relays a browser WebSocket to eikad `/pty` through `Workspaces.Terminal` (`Host.Terminal`, `sandbox.Client.Terminal`), never through `executor.Executor`, so a tool can run commands but can never hold a PTY. Frames pass through unchanged; the token may ride in `?token=` on this path, as on the event stream |
| Workspace edits reuse `workspace.state` | A file saved, a commit, or a push through the API publishes `workspace.state` with the unchanged state instead of a new event type: the payload is the same, and a client refreshes files and changes on either |
| No decode rate is shown | A Chat Completions endpoint reports its token counts when a response ends, never while one streams, so there is nothing to divide while the model writes. Counting stream deltas instead would be a guess wearing a measurement's clothes — one delta is one token on some endpoints and not on others — so the UI shows what was measured (context, turn cost, generation time) and no tok/s at all |
| A cut-off response is kept, not discarded | A `length` or `content_filter` stop means the endpoint ended the response early. What it produced is stored and the turn ends on it, rather than the run failing and rolling back: the user watched that text stream, and a turn rolled back takes the user's own message with it, so the next call shows the model a history in which it never answered and it apologises for a turn it did take. The response's tool calls are dropped — one cut off partway through its arguments must not run, and one left without a result makes the next request malformed — and `turn.end` carries the stop reason so the UI says the answer was cut off |
| Upstream push is from the hub | "Push upstream" pushes the workspace to its hub branch, then the hub pushes that branch to the project's remote with the harness's sealed credentials, so no remote credential enters a sandbox. A pull request is a compare link the UI builds from `remote_url`; there is no forge API |

## 2. Core principle: every agent action runs in a sandbox

The harness process never reads, writes, or executes anything on behalf of
an agent outside a workspace container. All tools that touch files or run
commands do so through an `Executor` interface whose only production
implementation talks to a sandbox. Search and LLM calls run in the harness
because they are network calls, not filesystem or process actions. A web_fetch
filter is code the model wrote, so it runs in the sandbox as `eikad filter`.

## 3. Domain model

```
Project        A git repository known to Eika. Backed by a bare repo in the
               harness "hub" volume and optionally a remote (GitHub etc.) or a
               host directory.
Workspace      A sandbox container + volume holding a clone of a Project on a
               branch. Sessions run inside a workspace. Has a lifecycle:
               creating -> running -> stopped -> archived.
Session        A tree of entries (user, assistant, tool call, tool result,
               system events) inside one workspace. Has a head pointer.
Agent run      One execution of the agent loop on a session from its head.
Subagent       A child agent run in its own Workspace (cloned from the parent's
               workspace at its current commit, on a child branch), with its
               own Session. Reports back to the parent as a tool result.
```

### Git flow

- The harness serves git over HTTP on the internal Docker network
  (`/git/<project>.git`, Smart HTTP via `git http-backend`). Every workspace
  clones from and pushes to the hub. The hub is the single point of exchange
  between workspaces, subagents, and the user.
- **Remote projects** (e.g. GitHub): the hub mirrors the remote. The UI offers
  "push branch to remote" and "open PR" style actions. Credentials stay in the
  harness; sandboxes never hold remote credentials.
- **Local projects**: a host directory is bind-mounted into the workspace at
  `/workspace`. The agent commits there directly. No hub involvement unless
  the user forks a subagent, in which case the hub is used for the child.
- **Subagents**: parent commits its work-in-progress to a `wip` commit on its
  branch and pushes to the hub; child workspace clones at that commit on
  `<parent-branch>/<subagent-name>`. When the child finishes it pushes; the
  parent gets a summary plus the branch name and may fetch and merge.

### Session trees

Entries form a tree (`parent_id`), like Pi. Eika leans into what a sandbox
enables:

- **Branch in place**: move the head to any entry and continue.
- **Fork with workspace**: fork from an entry into a new workspace cloned at
  the commit recorded for that entry. This gives a true "what if" branch where
  the filesystem also rewinds. Each assistant turn records the workspace's
  HEAD commit so this is possible.

### Working with the user (ergonomics)

- **Steering and follow-up** message queues as in Pi, exposed in the UI.
- **`ask_user` tool**: the agent can pose a structured question (options or
  free text). The UI renders it as a form; the run pauses until answered.
- **Shared sandbox**: the user has a terminal and file editor into the same
  container the agent works in. Edits by either side are visible to both.
- **Review surface**: every workspace shows a live diff against its base
  commit with commit, push, and "hand to a subagent" actions.
- **Notifications**: subagent completion, questions, and errors surface as
  events in the UI event stream.

## 4. Repository layout

```
Eika/
  AGENTS.md                  Entry point for agents working on Eika
  docs/
    PLAN.md                  This file
    STYLE_GUIDE.md           Coding and documentation rules (Go, TS, docs)
    ARCHITECTURE.md          How the pieces fit; written in phase 1, kept current
    EXTENDING.md             How to add a tool, provider, search backend, UI panel
    api/                     Event protocol and HTTP API reference
  cmd/
    eika/                    Harness server binary
    eikad/                   Sandbox daemon binary (static, runs inside sandboxes)
  internal/
    agent/                   Agent loop (Pi port): turns, queues, tool dispatch
    provider/                Provider interface; provider/openai implementation
    tool/                    Tool interface, registry, built-in tools
    executor/                Executor interface; executor/sandbox (eikad client),
                             executor/local (tests only)
    session/                 Session tree model and persistence
    workspace/               Workspace lifecycle, Docker client, volumes, git hub
    subagent/                Spawning and reporting
    search/                  Search engine and failover chain; search/web, wikipedia,
                             arxiv, github backends; search/fetch, page, filter
    contextfile/             AGENTS.md discovery and assembly
    server/                  HTTP API, WebSocket event stream, auth
    store/                   Postgres access and migrations
    config/                  Deployment config loading (YAML + env)
    secret/                  Sealing credentials stored in the database
    event/                   Shared event types emitted by the loop and UI
  sandbox/
    Dockerfile               eika-sandbox image
  web/                       Frontend (Vite + React)
  compose.yaml               eika, the sandbox image build, postgres, searxng
  compose.dev.yaml           loopback ports for `make dev`
  .env.example               the optional compose variables
  deploy/
    eika-entrypoint.sh       joins the Docker socket's group, drops to eika
    searxng/settings.yml
  Makefile
```

`internal/` packages depend inward: `server -> agent -> tool -> executor`;
nothing under `tool` imports `workspace`. Dependency direction is enforced by
review and by `go vet`-style checks in `make check` where practical.

## 5. Key interfaces

```go
// provider
type Provider interface {
    Stream(ctx context.Context, req Request) (<-chan Event, error)
}

// tool
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage           // JSON Schema for parameters
    Call(ctx context.Context, c CallContext, args json.RawMessage) (Result, error)
}

// executor: the only way tools touch a workspace
type Executor interface {
    Root() string                                                  // workspace root
    Exec(ctx context.Context, spec ExecSpec) (ExecResult, error)   // streams output
    ReadFile(ctx context.Context, path string, opts ReadOpts) ([]byte, error)
    WriteFile(ctx context.Context, path string, data []byte) error
    Stat(ctx context.Context, path string) (FileInfo, error)
    List(ctx context.Context, path string) ([]FileInfo, error)
}

// search
type Searcher interface {
    Search(ctx context.Context, q Query) ([]Result, error)
}
```

Adding a tool, provider, or search backend means: create a package, implement
the interface, register it in one registry file, add tests, document it in
`docs/EXTENDING.md`. That is the whole malleability story.

## 6. Sandbox daemon (`eikad`)

A small static Go binary baked into `eika-sandbox` and copied into every
container the harness creates, so any image works. It listens on the Docker
network on port 7000 and provides: exec with streaming stdout/stderr and exit
codes, PTY sessions for the web terminal, file read/write/stat/list, and a
file-change watcher for the editor. The harness authenticates with a
per-workspace token passed as `EIKAD_TOKEN` at container start. The API is
documented in `docs/api/eikad.md`.

## 7. Event protocol

One WebSocket per UI client, subscribed to topics (`workspace:<id>`,
`session:<id>`, `global`). Events are the same types the agent loop emits
internally (`internal/event`): `turn.start`, `message.delta`,
`reasoning.delta`, `message.reset`, `tool.call`, `tool.output`,
`tool.result`, `turn.progress`, `turn.end`, `run.error`, `question.asked`,
`subagent.started`, `subagent.finished`, `workspace.state`. Two more exist on
the stream alone: `session.message`, which is what a replay sends, and
`bus.dropped`, which tells one client it read too slowly. Documented in
`docs/api/events.md` and mirrored as TypeScript types in `web/src/api/`.

## 8. Phases

Each phase ends with: tests green, docs updated, `make check` clean. Phases
are sized so one Opus subagent can own one phase (or one half of a large
phase) in a single focused effort. Where phases are independent they run in
parallel.

### Phase 0: Foundations
- Go module, `web/` scaffold, `Makefile` (`build`, `test`, `lint`, `check`, `dev`).
- Static checks: `go vet`, `staticcheck`, `gofmt`, `tsc`, `eslint`, `prettier`.
- `docker-compose.yml` skeleton with eika, postgres, searxng.
- `docs/ARCHITECTURE.md` initial version.

### Phase 1: Agent core (Pi port)
- `provider`: interface + OpenAI implementation (Chat Completions, streaming,
  tool calls, usage). Model registry from config.
- `tool`: interface, registry, built-ins `read`, `write`, `edit`, `bash`,
  `grep`, `find`, `ls` with Pi's semantics (offset/limit reads, old/new
  string edits, output truncation).
- `executor/local` for tests only.
- `agent`: loop with streaming, tool dispatch, steering and follow-up queues,
  abort, error recovery. In-memory session for now; a `Store` interface that
  phase 3 implements against PostgreSQL.
- `contextfile`: AGENTS.md discovery (project, parents, global).
- Table-driven tests with a fake provider.

### Phase 2: Sandboxes and workspaces
- `cmd/eikad` and `sandbox/Dockerfile`.
- `executor/sandbox` client.
- `workspace`: Docker client wrapper, create/start/stop/destroy, volumes,
  custom image and Dockerfile builds, injection of `eikad` for custom images.
- Git hub: bare repos, Smart HTTP endpoint, remote mirroring, local bind mode.
- Integration tests gated behind a `docker` build tag.

### Phase 3: Persistence and sessions
- Postgres schema and migrations: projects, workspaces, sessions, entries,
  runs, subagents, settings.
- `session`: tree model, head management, fork, fork-with-workspace.
- `store`: query layer, transactional writes for entries.

### Phase 4: Server and event stream
- Wiring: `server.Run` builds the store, the hub, the workspace host, the
  model set, and the tool registry from configuration, mounts `hub.Handler`
  at `/git`, and reconciles workspaces with `Host.List` on startup.
- HTTP API for projects, workspaces, sessions, runs, questions, settings.
- WebSocket event stream with topics and replay from an entry id.
- Bearer token auth.
- API docs in `docs/api/`.

### Phase 5: Web UI (core)
- App shell, workspace list and creation, session view with streaming
  messages, tool call rendering (collapsible, per-tool renderers), queues,
  `ask_user` forms, session tree navigation, settings.

### Phase 6: Subagents
- `subagent`: `spawn_agent` tool, child workspace creation via hub, branch
  naming, concurrency limits, cancellation propagation, result reporting.
- UI: agent tree panel, jump into a child session.

### Phase 7: Search
- `search`: a port of localsearch. Web failover chain (SearXNG, Exa, Tavily,
  Brave, Marginalia) with quotas and cooldowns; Wikipedia, arXiv, and GitHub
  sources; `fetch` (URL planning, GitHub reads, HTML to Markdown, section
  selection, outline and budget). Tools `web_search` and `web_fetch`, the
  filter run as `eikad filter` in the sandbox.
- Settings: provider order, quotas, and sealed keys in the Search tab, with
  usage persisted. Result and page caching.

### Phase 8: Terminal, editor, diff
- `eikad` PTY endpoint, xterm.js terminal panel.
- File tree and Monaco editor with save through `eikad`.
- Diff view against base commit; commit, push, open-PR-style actions.

### Phase 9: Hardening and docs
- End-to-end smoke test in compose.
- Docs pass: `ARCHITECTURE.md`, `EXTENDING.md`, API docs current.
- Resource limits for sandboxes (CPU, memory, pids), network policy option.

## 9. Testing strategy

- Unit tests everywhere with the standard library; `executor/local` and a
  fake provider make the agent loop fully testable without Docker or a model.
- Docker-dependent tests use build tag `docker` and run only when a socket
  is available.
- Frontend: Vitest for logic. Visual tests in `web/e2e/`: Playwright drives the UI
  against an in-browser mock of the HTTP API and event stream, compares screens
  with committed baselines (`make visual`), and gives agents a screenshot CLI
  (`npm run shot`). A Playwright smoke test against compose follows in phase 9.

## 10. Resolved and open questions

- Sandbox network policy defaults to open egress, configurable per workspace.
- Resolved in phase 3: the hub mirrors local projects as well, so
  fork-with-workspace and subagents behave the same for both project kinds.
  See the decision table.

## 11. Phase checklist

- [x] Phase 0: Foundations
- [x] Phase 1: Agent core
- [x] Phase 2: Sandboxes and workspaces
- [x] Phase 3: Persistence and sessions
- [x] Phase 4: Server and event stream
- [x] Phase 5: Web UI core
- [x] Phase 6: Subagents (backend; the agent tree panel is UI work)
- [x] UI configuration: providers, models, sign-in, and the guided setup
- [x] Phase 7: Search
- [x] Phase 8: Terminal, editor, diff
- [x] Transcript and configuration UI: streamed reasoning, per-model
      reasoning efforts, the measured context and decode meter, and a pass
      over the shell's legibility (prose typography, click affordances,
      scrollbars, divider hit targets, a fixed-height settings dialog)
- [ ] Phase 9: Hardening and docs
