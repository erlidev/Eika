# API reference

The wire contract between the Eika harness and any client. Go types live in
the package that owns them; the TypeScript mirrors live in `web/src/api/`. When
a Go wire type changes, this directory and the TypeScript type change in the
same commit.

## Files

- `http.md` — HTTP routes, one entry per route with request and response
  fields. Lands in phase 4.
- `events.md` — WebSocket event stream: topics, the envelope, and one entry per
  event type. Lands in phase 4.

## What exists now

Two unauthenticated health routes, both returning `{"status": "ok"}`:

| Method | Path | Meaning |
|---|---|---|
| GET | `/healthz` | The harness process is up. Used as the container health check. |
| GET | `/api/healthz` | The same response under the `/api` prefix the frontend uses. |

`eikad` serves `GET /healthz` with the same body.

Every route added from phase 4 on requires `Authorization: Bearer <token>`,
where the token is the configured `auth_token`. `/healthz` is the only
permanent exception.

The event envelope is already fixed, in `internal/event`:

| Field | Type | Meaning |
|---|---|---|
| `type` | string | Event type name, e.g. `tool.call`. |
| `topic` | string | Where it is routed: `global`, `workspace:<id>`, or `session:<id>`. |
| `time` | RFC 3339 timestamp, UTC | When the event was created. |
| `payload` | object, optional | Type-specific body, owned by the emitting package. |

The type names are `turn.start`, `message.delta`, `tool.call`, `tool.output`,
`tool.result`, `turn.end`, `run.error`, `question.asked`, `subagent.started`,
`subagent.finished`, and `workspace.state`.
