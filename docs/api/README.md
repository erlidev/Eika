# API reference

The wire contract between the Eika harness and any client. Go types live in
the package that owns them; the TypeScript mirrors live in `web/src/api/`. When
a Go wire type changes, this directory and the TypeScript type change in the
same commit.

## Files

- `http.md` — HTTP routes, one entry per route with request and response
  fields. Lands in phase 4.
- `events.md` — the event stream: topics, the envelope, and one entry per event
  type. The agent run events are documented; the WebSocket transport lands in
  phase 4.
- `eikad.md` — the sandbox daemon's API: exec, files, terminal, and the change
  watcher. The harness is its only client.

## What exists now

Two unauthenticated health routes, both returning `{"status": "ok"}`:

| Method | Path | Meaning |
|---|---|---|
| GET | `/healthz` | The harness process is up. Used as the container health check. |
| GET | `/api/healthz` | The same response under the `/api` prefix the frontend uses. |

`eikad` serves `GET /healthz` with the same body, plus the sandbox API in
`eikad.md`. The harness also serves the git hub at `/git/<project>.git`, which
speaks git's Smart HTTP protocol and authenticates workspaces with HTTP basic
auth rather than the bearer token.

Every route added from phase 4 on requires `Authorization: Bearer <token>`,
where the token is the configured `auth_token`. `/healthz` is the only
permanent exception.

The event envelope is already fixed, in `internal/event`:

| Field | Type | Meaning |
|---|---|---|
| `type` | string | Event type name, e.g. `tool.call`. |
| `topic` | string | Where it is routed: `global`, `workspace:<id>`, or `session:<id>`. |
| `time` | RFC 3339 timestamp, UTC | When the event was created. |
| `payload` | object, optional | Type-specific body; the structs live in `internal/event`. |

The type names are `turn.start`, `message.delta`, `tool.call`, `tool.output`,
`tool.result`, `turn.end`, `run.error`, `question.asked`, `subagent.started`,
`subagent.finished`, and `workspace.state`.
