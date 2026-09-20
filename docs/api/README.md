# API reference

The wire contract between the Eika harness and any client. Go types live in
the package that owns them; the TypeScript mirrors live in `web/src/api/`. When
a Go wire type changes, this directory and the TypeScript type change in the
same commit.

## Files

- `http.md` — HTTP routes, one entry per route with request and response
  fields.
- `events.md` — the event stream: the WebSocket transport, subscription and
  replay requests, topics, the envelope, and one entry per event type.
- `eikad.md` — the sandbox daemon's API: exec, files, terminal, and the change
  watcher. The harness is its only client.

## What exists now

Two unauthenticated health routes, both returning `{"status": "ok"}`:

| Method | Path | Meaning |
|---|---|---|
| GET | `/healthz` | The harness process is up. Used as the container health check. |
| GET | `/api/healthz` | The same response under the `/api` prefix the frontend uses. |

Everything else under `/api` is the JSON API in `http.md`: projects,
workspaces and their files, commits, and pushes, sessions, runs, questions,
sign-in, providers, models, settings, the system check, and two WebSockets,
the event stream and a workspace's terminal. All of it requires
`Authorization: Bearer <token>`, where the token is a sign-in session's or
the deployment's optional API token; the two WebSockets, `/api/events` and
`/api/workspaces/{id}/terminal`, also accept the token as a query parameter,
because a WebSocket handshake carries no headers a browser can set. The health routes and the three routes that hand out a session are
the only exceptions.

`eikad` serves `GET /healthz` with the same body, plus the sandbox API in
`eikad.md`. The harness also serves the git hub at `/git/<project>.git`, which
speaks git's Smart HTTP protocol and authenticates workspaces with HTTP basic
auth rather than the bearer token.

The event envelope is fixed in `internal/event`:

| Field | Type | Meaning |
|---|---|---|
| `type` | string | Event type name, e.g. `tool.call`. |
| `topic` | string | Where it is routed: `global`, `workspace:<id>`, or `session:<id>`. |
| `time` | RFC 3339 timestamp, UTC | When the event was created. |
| `payload` | object, optional | Type-specific body; the structs live in `internal/event`. |

The type names are `turn.start`, `message.delta`, `message.reset`, `tool.call`,
`tool.output`, `tool.result`, `turn.end`, `run.error`, `question.asked`,
`subagent.started`, `subagent.finished`, `workspace.state`, `session.message`,
and `bus.dropped`.
`subagent.started` and `subagent.finished` are the only ones nothing emits
yet; they land in phase 6.
